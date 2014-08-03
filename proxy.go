package main

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"github.com/sajal/gohttpcache/cache"
	"github.com/valyala/ybc/bindings/go/ybc"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"
)

var originmapping map[string]string
var determiner gohttpcache.Determiner

var (
	objcache  ybc.Cacher
	metacache ybc.Cacher
)

var client http.RoundTripper

type MetaItem struct {
	Header  http.Header
	ObjKey  []byte
	Fetched time.Time
	Status  int
}

type MetaObj struct {
	Vary []string
}

func buildcachekey(r *http.Request, origin string) (key []byte) {
	//for now ignore vary and things to keep simple
	key = append([]byte(r.Method), []byte(origin)...)
	key = append(key, []byte(r.RequestURI)...)
	return
}

func getvary(key []byte) (vary []string, err error) {
	m, err := metacache.Get(key)
	if err != nil {
		log.Println(err)
		return
	}
	rdr := bytes.NewReader(m)
	dec := gob.NewDecoder(rdr)
	//start := time.Now()
	meta := &MetaObj{}
	err = dec.Decode(meta)
	if err != nil {
		log.Println(err)
		return
	}
	vary = meta.Vary
	return
}

func fetchupstream(r *http.Request, origin string) (item *ybc.Item, err error) {
	log.Println(r)
	url := fmt.Sprintf("http://%s%s", origin, r.RequestURI)
	log.Println(url)
	req, err := http.NewRequest(r.Method, url, nil)
	if err != nil {
		return
	}
	for k, v := range r.Header {
		if k != http.CanonicalHeaderKey("Host") {
			for _, val := range v {
				req.Header.Add(k, val)
			}
		}
	}
	resp, err := client.RoundTrip(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	// load into cache...

	_, _, _, heuristics, ttl, err := determiner.Determine(r.Method, resp.StatusCode, req.Header, resp.Header)
	if ttl == time.Duration(0) && heuristics {
		ttl = time.Duration(24) * time.Hour
	}
	if ttl < time.Minute {
		ttl = time.Minute
	}

	var buffer bytes.Buffer
	hdrobj := &MetaItem{Header: resp.Header, Status: resp.StatusCode, Fetched: time.Now()}
	enc := gob.NewEncoder(&buffer)
	err = enc.Encode(hdrobj)
	if err != nil {
		return
	}
	hdrbyt := buffer.Bytes()
	key := buildcachekey(r, origin)
	metaobj := &MetaObj{}
	for _, k := range strings.Split(resp.Header.Get("Vary"), ",") {
		metaobj.Vary = append(metaobj.Vary, http.CanonicalHeaderKey(strings.Trim(k, " ")))
	}

	var metabuffer bytes.Buffer
	metaenc := gob.NewEncoder(&metabuffer)
	err = metaenc.Encode(metaobj)
	if err != nil {
		return
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	itemsize := len(body) + len(hdrbyt) + 2
	metacache.Set(key, metabuffer.Bytes(), ybc.MaxTtl)
	key = appendvary(key, r.Header, metaobj.Vary)
	txn, err := objcache.NewSetTxn(key, itemsize, ttl)
	if err != nil {
		return
	}
	err = storeheaders(hdrbyt, txn)
	if err != nil {
		txn.Rollback()
		return
	}
	_, err = txn.Write(body)
	if err != nil {
		txn.Rollback()
		return
	}
	item, err = txn.CommitItem()
	return
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

func getitem(key []byte, r *http.Request, origin string) (hit bool, item *ybc.Item, err error) {
	item, err = objcache.GetDeItem(key, time.Second)
	hit = true
	if err != nil {
		hit = false
		item, err = fetchupstream(r, origin)
	}
	return
}

func serveitem(hit bool, w http.ResponseWriter, item *ybc.Item) {
	defer item.Close()
	meta, err := loadmeta(item)
	if err != nil {
		log.Println(err)
		servefail(w)
		return
	}
	for k, v := range meta.Header {
		if k != http.CanonicalHeaderKey("Date") || k != http.CanonicalHeaderKey("Transfer-Encoding") { //Strip out Date header from cache. Let Go put that in
			for _, val := range v {
				w.Header().Add(k, val)
			}
		}
	}
	if hit {
		w.Header().Set("X-Gocache", "HIT")
	} else {
		w.Header().Set("X-Gocache", "MISS")
	}
	w.Header().Set("Age", fmt.Sprintf("%9.f", time.Since(meta.Fetched).Seconds()))
	w.WriteHeader(meta.Status)
	io.Copy(w, item)
}

func servefail(w http.ResponseWriter) {
	w.WriteHeader(http.StatusBadGateway)
}

func appendvary(key []byte, headers http.Header, vary []string) []byte {
	for _, k := range vary {
		key = append([]byte(headers.Get(k)), key...)
	}
	return key
}

func cachecheckhandler(w http.ResponseWriter, r *http.Request, origin string) {
	key := buildcachekey(r, origin)
	log.Println(string(key))
	vary, err := getvary(key)
	key = appendvary(key, r.Header, vary)
	log.Println(string(key))
	hit, item, err := getitem(key, r, origin)
	if err != nil {
		log.Println(err)
		servefail(w)
	} else {
		serveitem(hit, w, item)
	}
}

func hosthandler(w http.ResponseWriter, r *http.Request) {
	log.Println(r.Host)
	origin, mapok := originmapping[r.Host]
	if !mapok {
		w.WriteHeader(http.StatusNotFound)
	} else {
		cachecheckhandler(w, r, origin)
	}
}

func main() {
	originmapping = make(map[string]string, 2)
	originmapping["cdn.cdnplanet.com"] = "www.cdnplanet.com"
	originmapping["cdn.turbobytes.com"] = "www.turbobytes.com"
	client = &http.Transport{}
	determiner = gohttpcache.NewPublicDeterminer()
	metacacheconfig := ybc.Config{
		MaxItemsCount: ybc.SizeT(500 * 1000), //500k keys..
	}
	metacache, _ = metacacheconfig.OpenCache(true)

	objcacheconfig := ybc.Config{
		MaxItemsCount: ybc.SizeT(500 * 1000),        //500k keys..
		DataFileSize:  ybc.SizeT(500 * 1024 * 1024), //500 MB
	}
	objcache, _ = objcacheconfig.OpenCache(true)

	log.Println("Hello Proxy")
	srv := &http.Server{
		Addr:        ":8066",
		Handler:     http.HandlerFunc(hosthandler),
		ReadTimeout: 300 * time.Second,
	}
	log.Fatal(srv.ListenAndServe()) //blocks forever
}
