package main

import (
	"fmt"
	"github.com/sajal/gohttpcache/cache"
	"net/http"
)

func main() {
	fmt.Println("Hello World")
	d := gohttpcache.NewPublicDeterminer()
	req := make(map[string][]string)
	res := make(map[string][]string)
	res[http.CanonicalHeaderKey("Cache-Control")] = []string{"public, s-maxage=300, max-age=3600"}
	fmt.Println(d.Determine("GET", 200, req, res))
}
