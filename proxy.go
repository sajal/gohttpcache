package main

import (
	"log"
	"net/http"
	"time"
)

var originmapping map[string]string

func buildcachekey(r *http.Request, origin string) (key []byte) {
	//for now ignore vary and things to keep simple
	key = append([]byte(origin), []byte(r.RequestURI)...)
	return
}

func cachecheckhandler(w http.ResponseWriter, r *http.Request, origin string) {
	key := buildcachekey(r, origin)
	log.Println(string(key))
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

	log.Println("Hello Proxy")
	srv := &http.Server{
		Addr:        ":8066",
		Handler:     http.HandlerFunc(hosthandler),
		ReadTimeout: 300 * time.Second,
	}
	log.Fatal(srv.ListenAndServe()) //blocks forever
}
