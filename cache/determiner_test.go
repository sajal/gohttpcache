package gohttpcache

import (
	"net/http"
	"testing"
	"time"
)

func test_PublicDeterminer(t *testing.T) {
	determiner := NewPublicDeterminer()
	req := make(http.Header)
	res := make(http.Header)
	res.Set("Cache-Control", "public, s-maxage=300, max-age=3600")
	cache, store, stale, heuristics, ttl, err := determiner.Determine("GET", 200, req, res)
	if cache {
		t.Error("This should be cachable")
	}
	if store {
		t.Error("This should be storable")
	}
	if stale {
		t.Error("This should be allowed stale")
	}
	if !heuristics {
		t.Error("heuristics should be false")
	}
	if ttl != time.Duration(300)*time.Second {
		t.Error("ttl should be 300 sec")
	}
	if err != nil {
		t.Error(err)
	}
}
