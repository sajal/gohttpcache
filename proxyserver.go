package main

import (
	"github.com/sajal/gohttpcache/proxy"
	"log"
	"time"
)

func main() {
	services := make([]goproxy.Service, 1)
	services[0].Id = "foo"
	services[0].Name = "bar"
	services[0].Origin = "www.cdnplanet.com"
	services[0].OriginHost = "www.cdnplanet.com"
	services[0].Hostnames = []string{"cdn.cdnplanet.com", "foo.cdnplanet.com", "bar.cdnplanet.com"}
	services[0].BaseKeyFunc = goproxy.QSIgnoreKeyFunc
	proxy := goproxy.NewProxyServer(services, "/tmp/", 50, 500, 500000)
	log.Fatal(proxy.ListenAndServe(":8066", 300*time.Second))
}
