package gohttpcache

import (
	"net/http"
	"testing"
	"time"
)

type DeterminerExpected struct {
	Cache      bool
	Store      bool
	Stale      bool
	Heuristics bool
	Ttl        time.Duration
	Err        error
}

type DeterminerTestCase struct {
	RespStatus  int
	Method      string
	ReqHdr      http.Header
	ResHdr      http.Header
	Public      DeterminerExpected
	Private     DeterminerExpected
	Explanation string
}

func (self *DeterminerTestCase) runindividualtest(t *testing.T, determiner Determiner, expectation DeterminerExpected) (testpass bool) {
	cache, store, stale, heuristics, ttl, err := determiner.Determine(self.Method, self.RespStatus, self.ReqHdr, self.ResHdr)
	testpass = true
	if cache != expectation.Cache {
		t.Error("cachable should be", expectation.Cache, "got", cache)
		testpass = false
	}
	if store != expectation.Store {
		t.Error("store should be", expectation.Store, "got", store)
		testpass = false
	}
	if stale != expectation.Stale {
		t.Error("stale should be", expectation.Stale, "got", stale)
		testpass = false
	}
	if heuristics != expectation.Heuristics {
		t.Error("heuristics should be", expectation.Heuristics, "got", heuristics)
		testpass = false
	}
	if ttl != expectation.Ttl {
		t.Error("ttl should be", expectation.Ttl, "got", ttl)
		testpass = false
	}
	if err != nil {
		t.Error(err)
		testpass = false
	}
	return
}

func (self *DeterminerTestCase) runtest(t *testing.T) {
	if !self.runindividualtest(t, NewPublicDeterminer(), self.Public) {
		t.Error("^ Public test failed")
		t.Error(self)
	}
	if !self.runindividualtest(t, NewPrivateDeterminer(), self.Private) {
		t.Error("^ Private test failed")
		t.Error(self)
	}
}

func Test_PublicDeterminer(t *testing.T) {
	req := make(http.Header)
	res := make(http.Header)
	res.Set("Cache-Control", "public, s-maxage=300, max-age=3600")
	dt := &DeterminerTestCase{
		200,
		"GET",
		req,
		res,
		DeterminerExpected{
			true,
			true,
			true,
			false,
			time.Duration(300) * time.Second,
			nil,
		},
		DeterminerExpected{
			true,
			true,
			true,
			false,
			time.Duration(3600) * time.Second,
			nil,
		},
		"",
	}
	dt.runtest(t)
}
