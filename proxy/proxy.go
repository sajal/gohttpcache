package goproxy

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/valyala/ybc/bindings/go/ybc"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

//http messages
var (
	confignotfound = []byte("Requested hostname is not configured.\n")
	backenderr     = []byte("Error requesting to backend.\n")
	backendslow    = []byte("Backend too slow.\n")
)

//Cached items have this preceeding the object body
type MetaItem struct {
	Header  http.Header
	ObjKey  []byte
	Fetched time.Time
	Status  int
}

//We use this object to pass around args thru the stack
type transaction struct {
	clientreq  *http.Request //Stashing the original client req
	respwriter http.ResponseWriter
	started    time.Time
	logid      string        //A unique identifier in logs and resp header
	hit        bool          //true if it was cache hit
	origintime time.Duration //Time taken to fetch from origin
	metakey    []byte
	objkey     []byte
}

func (self *transaction) log(args ...interface{}) {
	log.Println(self.logid, args)
}

func (self *transaction) stamp() {
	self.respwriter.Header().Set("X-GP-Timetaken", time.Since(self.started).String())
	self.respwriter.Header().Set("X-GP-Debug", self.logid)
	if self.hit {
		self.respwriter.Header().Set("X-GP-Cache", "HIT")
	} else {
		self.respwriter.Header().Set("X-GP-Cache", "MISS in "+self.origintime.String())
	}
}

func (self *transaction) servebody(meta MetaItem, item io.ReadCloser) {
	defer item.Close()
	for k, v := range meta.Header {
		if k != http.CanonicalHeaderKey("Date") || k != http.CanonicalHeaderKey("Transfer-Encoding") { //Strip out Date header from cache. Let Go put that in
			for _, val := range v {
				self.respwriter.Header().Add(k, val)
			}
		}
	}
	self.respwriter.Header().Set("Age", fmt.Sprintf("%9.f", time.Since(meta.Fetched).Seconds()))
	self.stamp()
	self.respwriter.WriteHeader(meta.Status)
	/*
		if f, ok := self.respwriter.(http.Flusher); ok {
			f.Flush()
			self.log("Flushed")
		} else {
			self.log("No flush available")
		}
	*/
	io.Copy(self.respwriter, item)

	//Stamp response
}

func (self *transaction) serve(item io.ReadCloser) {
	//Write headers...
	meta, err := loadmeta(item)
	if err != nil {
		self.log(err)
		self.fail(err)
		item.Close()
		return
	}
	self.servebody(meta, item)
}

func (self *transaction) fail(err error) {
	//TODO: send booboo to user.
}

func newrequest(w http.ResponseWriter, r *http.Request) *transaction {
	txn := &transaction{}
	txn.clientreq = r
	txn.hit = false
	txn.respwriter = w
	txn.started = time.Now()
	txn.logid = uniuri.NewLen(12) // Generate some sort of uuid
	txn.origintime = time.Duration(0)
	return txn
}

//Example to use if you dont want anything fancy
func DefaultBaseKeyFunc(r *http.Request, id string) (key []byte) {
	key = append([]byte(r.Method), []byte(id)...)
	key = append(key, []byte(r.RequestURI)...)
	return

}

//Struct that a user defines for a service
type Service struct {
	Name        string                                        //Descriptive name of the service
	Id          string                                        //ID Unique..
	Origin      string                                        //Hostname to resolve for connection to origin
	OriginHost  string                                        //Host header used when requesting to origin
	OriginTLS   bool                                          //Weather to use https when connecting to origin or not.
	Hostnames   []string                                      //Hostnames to serve content from
	BaseKeyFunc func(r *http.Request, id string) (key []byte) //Default key building function
	client      http.RoundTripper                             //One client per service
}

//Call the BaseKeyFunc
func (self *Service) getbasekey(r *http.Request) []byte {
	return self.BaseKeyFunc(r, self.Id)
}

//The main proxyserver handler
type ProxyServer struct {
	configs     map[string]*Service //Thread safe for read only. TODO: locking for updates...
	objcache    *ybc.Cache          //Thread safe
	metacache   *ybc.Cache          //Thread safe
	configmutex sync.RWMutex        //FUTURE: We will use this for locking to do updates
}

//Creates a new ProxyServer
//services : list of ServiceConfig
//cachedir: directory to store the cache
//metacachesize: Size (in MB) of metadata particularly vary info
//objcachesize: Size (in MB) of actual cache objects
//maxitems: Max items in each cache
func NewProxyServer(services []Service, cachedir string, metacachesize, objcachesize, maxitems int) *ProxyServer {
	proxy := &ProxyServer{}
	proxy.configs = make(map[string]*Service)
	for _, service := range services {
		if service.BaseKeyFunc == nil {
			log.Println(service.Id, "BaseKeyFunc not found using DefaultBaseKeyFunc")
			service.BaseKeyFunc = DefaultBaseKeyFunc
		}
		service.client = &http.Transport{
			MaxIdleConnsPerHost: 10, //10 idle connections max
			//TLSHandshakeTimeout:   time.Minute, //1 minute timeout for TLS handshake.
			ResponseHeaderTimeout: time.Minute, //1 minute timeout for starting to receive response.
		}
		for _, hostname := range service.Hostnames {
			proxy.configs[hostname] = &service
		}
	}

	metacfg := ybc.Config{
		MaxItemsCount: ybc.SizeT(maxitems),
		DataFileSize:  ybc.SizeT(metacachesize) * ybc.SizeT(1024*1024),
		DataFile:      cachedir + "goproxy-meta.data",
		IndexFile:     cachedir + "goproxy-meta.index",
	}
	var err error
	proxy.metacache, err = metacfg.OpenCache(true)
	if err != nil {
		log.Fatal(err)
	}
	objcfg := ybc.Config{
		MaxItemsCount: ybc.SizeT(maxitems),
		DataFileSize:  ybc.SizeT(objcachesize) * ybc.SizeT(1024*1024),
		DataFile:      cachedir + "goproxy-obj.data",
		IndexFile:     cachedir + "goproxy-obj.index",
	}
	proxy.objcache, err = objcfg.OpenCache(true)
	if err != nil {
		log.Fatal(err)
	}
	return proxy
}

//default handler
func (self *ProxyServer) handler(w http.ResponseWriter, r *http.Request) {
	service, serviceok := self.configs[r.Host]
	req := newrequest(w, r)
	if !serviceok {
		//Hostname is not configured..
		req.log(r.Host, "not configured")
		req.stamp()
		w.WriteHeader(http.StatusNotFound)
		w.Write(confignotfound)
	} else {
		self.cachehandler(req, service)
	}
}

//Service is known, now proceed to check cache
func (self *ProxyServer) cachehandler(req *transaction, service *Service) {
	req.metakey = service.getbasekey(req.clientreq)
	req.log("basekey", string(req.metakey))
	vary, err := self.getvary(req.metakey)
	req.objkey = req.metakey
	if err == nil {
		//Known vary keys exists, apply it
		req.objkey = self.appendvary(req.metakey, req.clientreq.Header, vary)
	} else {
		req.log("getvary", err)
	}
	req.log("objkey", string(req.objkey))
	item, err := self.objcache.GetItem(req.objkey)
	req.log("cachehandler exit")
	if err == nil {
		//Yay cache hit...
		req.hit = true
		req.serve(item)
		//item.Close()
	} else {
		self.handlecachemiss(req, service)
	}
}

//Object not in cache... fetch from origin...
func (self *ProxyServer) handlecachemiss(req *transaction, service *Service) {
	req.hit = false
	//TODO: Get binary stream from fetchorigin, and using buffers, tee one stream to serve and one to insert in cache
	_, _, _, err := self.fetchfromorigin(req, service)
	if err != nil {
		req.fail(err)
	} else {
		//req.serve(&stream)
	}
}

func (self *ProxyServer) fetchfromorigin(req *transaction, service *Service) (key []byte, ttl time.Duration, buf bytes.Buffer, err error) {
	//TODO... fetch from origin, and push data in binary stream as per our item thing...
	fetchstart := time.Now()
	var url string
	if service.OriginTLS {
		url = fmt.Sprintf("https://%s%s", service.Origin, req.clientreq.RequestURI)
	} else {
		url = fmt.Sprintf("http://%s%s", service.Origin, req.clientreq.RequestURI)
	}
	originreq, err := http.NewRequest(req.clientreq.Method, url, nil)
	if err != nil {
		return
	}
	originreq.Host = service.OriginHost
	for k, v := range req.clientreq.Header {
		if k != http.CanonicalHeaderKey("Host") {
			for _, val := range v {
				originreq.Header.Add(k, val)
			}
		}
	}
	resp, err := service.client.RoundTrip(originreq)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var buffer bytes.Buffer
	hdrobj := &MetaItem{Header: resp.Header, Status: resp.StatusCode, Fetched: time.Now()}
	enc := gob.NewEncoder(&buffer)
	err = enc.Encode(hdrobj)
	if err != nil {
		return
	}
	hdrbyt := buffer.Bytes()

	//Update vary things...
	var vary []string
	for _, k := range strings.Split(resp.Header.Get("Vary"), ",") {
		vary = append(vary, http.CanonicalHeaderKey(strings.Trim(k, " ")))
	}

	var metabuffer bytes.Buffer
	metaenc := gob.NewEncoder(&metabuffer)
	err = metaenc.Encode(vary)
	if err != nil {
		return
	}
	self.metacache.Set(req.metakey, metabuffer.Bytes(), ybc.MaxTtl)
	key = self.appendvary(req.metakey, req.clientreq.Header, vary)

	if err != nil {
		return
	}
	req.origintime = time.Since(fetchstart)
	//_, err = io.Copy(&buf, resp.Body)
	rdr := NewSpyReader(resp.Body)
	cdone := make(chan bool)
	go func() {
		//Spoonfeed maybe
		req.servebody(*hdrobj, rdr)
		cdone <- true
	}()
	storeincache(key, time.Minute, hdrbyt, rdr, self.objcache)
	if err != nil {
		return
	}
	<-cdone
	return
}

func storeincache(key []byte, ttl time.Duration, hdrbyt []byte, item *SpyReader, cache *ybc.Cache) {
	body, err := item.Dump()
	if err != nil {
		log.Println(string(key), err)
	}
	itemsize := len(body) + len(hdrbyt) + 2

	txn, err := cache.NewSetTxn(key, itemsize, ttl)
	if err != nil {
		log.Println(string(key), err)
		return
	}
	err = storeheaders(hdrbyt, txn)
	if err != nil {
		log.Println(string(key), err)
		txn.Rollback()
		return
	}
	n, err := txn.Write(body)
	if err != nil {
		txn.Rollback()
		return
	}
	log.Println(string(key), n, "bytes written")
	err = txn.Commit()
	if err != nil {
		log.Println(string(key), err)
	} else {
		log.Println(string(key), "stored successfully in cache. yay")
	}
}

func storeheaders(hdrbyt []byte, w io.Writer) (err error) {
	if len(hdrbyt) > 65025 {
		//TODO err
	}
	var hdrsize int16 = int16(len(hdrbyt))
	err = binary.Write(w, binary.LittleEndian, hdrsize)
	if err != nil {
		return
	}
	_, err = w.Write(hdrbyt)
	return
}

func loadmeta(r io.Reader) (hdr MetaItem, err error) {
	var sizebuf [2]byte
	_, err = r.Read(sizebuf[:])
	if err != nil {
		return
	}
	buf := bytes.NewReader(sizebuf[:])
	var hdrsize int16
	err = binary.Read(buf, binary.LittleEndian, &hdrsize)
	if err != nil {
		return
	}
	hdrbytes := make([]byte, hdrsize)
	_, err = r.Read(hdrbytes)
	if err != nil {
		return
	}
	rdr := bytes.NewReader(hdrbytes)
	dec := gob.NewDecoder(rdr)
	//start := time.Now()
	err = dec.Decode(&hdr)
	return
}

//Service is known, now proceed to check cache
func (self *ProxyServer) getvary(key []byte) (vary []string, err error) {
	m, err := self.metacache.Get(key)
	if err != nil {
		return
	}
	rdr := bytes.NewReader(m)
	dec := gob.NewDecoder(rdr)
	err = dec.Decode(&vary)
	if err != nil {
		return
	}
	return
}

func (self *ProxyServer) appendvary(key []byte, headers http.Header, vary []string) []byte {
	for _, k := range vary {
		key = append([]byte(headers.Get(k)), key...)
	}
	return key
}

//Start the proxy server
func (self *ProxyServer) ListenAndServe(addr string, readtimeout time.Duration) (err error) {
	srv := &http.Server{
		Addr:        addr,
		Handler:     http.HandlerFunc(self.handler),
		ReadTimeout: readtimeout,
	}
	err = srv.ListenAndServe() //blocks forever
	return
}
