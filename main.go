package main

import (
	"flag"
	"fmt"
	"github.com/sajal/gohttpcache/cache"
	"net/http"
	"net/http/httputil"
	"time"
)

func printresponse(ctype string, cache, store, stale, heuristics bool, ttl time.Duration, err error) {
	fmt.Println(ctype)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Printf("Cachable: %v\n", cache)
		fmt.Printf("Store: %v\n", store)
		fmt.Printf("Allow Stale: %v\n", stale)
		fmt.Printf("Allow heuristics: %v\n", heuristics)
		fmt.Printf("Cache TTL: %s\n", ttl)
	}
}

func main() {
	var url = flag.String("url", "", "url to analyze")
	flag.Parse()
	if *url == "" {
		panic("Url should be supplied")
	}
	client := &http.Client{}
	req, err := http.NewRequest("GET", *url, nil)
	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	reqraw, err := httputil.DumpRequest(req, false)
	if err == nil {
		fmt.Println(string(reqraw))
	}
	respraw, err := httputil.DumpResponse(resp, false)
	if err == nil {
		fmt.Println(string(respraw))
	}

	pub := gohttpcache.NewPublicDeterminer()
	pri := gohttpcache.NewPublicDeterminer()
	cache, store, stale, heuristics, ttl, err := pub.Determine("GET", 200, req.Header, resp.Header)
	printresponse("public", cache, store, stale, heuristics, ttl, err)
	cache, store, stale, heuristics, ttl, err = pri.Determine("GET", 200, req.Header, resp.Header)
	printresponse("private", cache, store, stale, heuristics, ttl, err)
}
