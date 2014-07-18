package gohttpcache

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Determiner struct {
	ispublic          bool  //Is this a public cache?
	cachablebydefault []int //Statuses that can be treated as cachable by default.
}

func NewDeterminer(ispublic bool) Determiner {
	return Determiner{ispublic: ispublic, cachablebydefault: []int{200, 404}}
}

func NewPrivateDeterminer() Determiner {
	return Determiner{ispublic: false}
}

func NewPublicDeterminer() Determiner {
	return Determiner{ispublic: true}
}

//Determine determines cachability of a request/response pair.
func (self *Determiner) Determine(reqmethod string, respstatus int, reqhdrs, reshdrs http.Header) (cache, store, stale bool, ttl time.Duration, err error) {
	//Section 3..A cache MUST NOT store a response to any request, unless
	//The request method is understood by the cache and defined as being cacheable.
	// TODO: Currently hardcoding this to GET only, needs to be configurable
	if reqmethod != "GET" {
		return
	}
	//The response status code is understood by the cache. We assume this
	// to be the list of status codes understood by net/http/status.go
	if http.StatusText(respstatus) == "" {
		return
	}
	cachecontrol := reshdrs[http.CanonicalHeaderKey("Cache-Control")] //The Cache-Control header. Keep for future
	for _, c := range cachecontrol {
		// the "no-store" cache directive (see Section 5.2) does not appear
		// in request or response header fields
		if strings.Contains(c, "no-store") {
			return
		}
		if self.ispublic {
			// the "private" response directive (see Section 5.2.2.6) does not
			// appear in the response, if the cache is shared
			if strings.Contains(c, "private") {
				return
			}
		}
	}
	if self.ispublic {
		//the Authorization header field (see Section 4.2 of [RFC7235]) does
		//not appear in the request, if the cache is shared, unless the
		//response explicitly allows it (see Section 3.2)
		if _, ok := reqhdrs[http.CanonicalHeaderKey("Authorization")]; ok {
			//Auth hdr is present .. and is shared cache... - Section 3.2 check
			//tl;dr cache only if explicitly allowed by must-revalidate,
			//public, and s-maxage.
			//TODO: Actually implement this. For now we treat auth hrd to be uncachable
			return
		}
	}
	// Atleast 1 of last 6 sub-bullet points must be met for caching to continue
	var allowcache bool
	//contains an Expires header field
	_, allowcache = reshdrs[http.CanonicalHeaderKey("Expires")]
	for _, c := range cachecontrol {
		//contains a max-age response directive
		if strings.Contains(c, "max-age") {
			allowcache = true
		}
		//contains a s-maxage response directive and the cache is shared
		if self.ispublic {
			if strings.Contains(c, "s-maxage") {
				allowcache = true
			}
		}
		//contains a public response directive
		if strings.Contains(c, "public") {
			allowcache = true
		}
	}
	//contains a Cache Control Extension (see Section 5.2.3) that
	//allows it to be cached
	// ^ we ignore this
	//has a status code that is defined as cacheable by default
	for _, s := range self.cachablebydefault {
		if respstatus == s {
			allowcache = true
		}
	}
	if !allowcache {
		return
	}

	cache = true //From here on, a request is cachable.

	//4.2.1.  Calculating Freshness Lifetime
	// Date header is needed to calculate freshness from origin.
	// rfc7231 section 7.1.1.2 clarifies recipient with a clock uses
	// Now if Date is missing

	var date time.Time
	datehdr, dateok := reshdrs[http.CanonicalHeaderKey("Date")]
	if dateok {
		date, err = http.ParseTime(datehdr[0])
		if err != nil {
			date = time.Now()
		}
	} else {
		date = time.Now()
	}
	ttltmp, err := getmaxageval(self.ispublic, cachecontrol)
	if err == nil {
		ttl = time.Duration(ttltmp) * time.Second
	} else {
		//Look for expiry header
		expiry, expiryok := reshdrs[http.CanonicalHeaderKey("Expires")]
		if expiryok {
			expires, err := http.ParseTime(expiry[0])
			ttl = expires.Sub(date)
			if err != nil {
				//TODO: Use Heuristics
			}
		} else {
			//TODO: Use Heuristics
		}
	}
	//Progress: http://tools.ietf.org/html/rfc7234#section-4.2.1
	return
}

func getmaxageval(ispublic bool, cachecontrol []string) (maxage int, err error) {
	//TODO: this is a major clusterfuck, refactor later after writing tests
	if ispublic {
		for _, c := range cachecontrol {
			if strings.Contains(c, "s-maxage") {
				for _, smax := range strings.Split(c, ",") {
					if strings.Contains(smax, "s-maxage") {
						ttlsplitted := strings.Split(smax, "=")
						if len(ttlsplitted) != 2 {
							err = errors.New("Error parsing s-maxage")
							return
						} else {
							ttlstr := strings.TrimSpace(ttlsplitted[1])
							maxage, err = strconv.Atoi(ttlstr)
							return
						}
					}
				}
				return
			}
		}
	}
	for _, c := range cachecontrol {
		if strings.Contains(c, "max-age") {
			for _, smax := range strings.Split(c, ",") {
				if strings.Contains(smax, "max-age") {
					ttlsplitted := strings.Split(smax, "=")
					if len(ttlsplitted) != 2 {
						err = errors.New("Error parsing max-age")
						return
					} else {
						ttlstr := strings.TrimSpace(ttlsplitted[1])
						maxage, err = strconv.Atoi(ttlstr)
						return
					}
				}
			}
			return
		}
	}
	err = errors.New("s-maxage or max-age not found")
	return
}
