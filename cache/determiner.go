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
	return NewDeterminer(false)
}

func NewPublicDeterminer() Determiner {
	return NewDeterminer(true)
}

//Determine determines cachability of a request/response pair.
func (self *Determiner) Determine(reqmethod string, respstatus int, reqhdrs, reshdrs http.Header) (cache, store, stale, heuristics bool, ttl time.Duration, err error) {
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
	store = true
	stale = true
	//4.2.1.  Calculating Freshness Lifetime
	// Date header is needed to calculate freshness from origin.
	// rfc7231 section 7.1.1.2 clarifies recipient with a clock uses
	// Now if Date is missing

	var date time.Time
	var err1 error
	datehdr, dateok := reshdrs[http.CanonicalHeaderKey("Date")]
	if dateok {
		date, err1 = http.ParseTime(datehdr[0])
		if err1 != nil {
			date = time.Now()
		}
	} else {
		date = time.Now()
	}
	ttltmp, err1 := getmaxageval(self.ispublic, cachecontrol)
	if err1 == nil {
		//TODO: Take Age header value into account! section 5.1
		ttl = time.Duration(ttltmp) * time.Second
	} else {
		//Look for expiry header
		expiry, expiryok := reshdrs[http.CanonicalHeaderKey("Expires")]
		if expiryok {
			expires, err1 := http.ParseTime(expiry[0])
			ttl = expires.Sub(date)
			if err1 != nil {
				// Use Heuristics 4.2.2
				heuristics = true
			}
		} else {
			//Use Heuristics 4.2.2
			heuristics = true
		}
	}
	//Skipping until 5.2.2 because other stuff relates to handling request/responses
	// Section 5.2.2 Response Cache-Control Directives
	// TODO : Dear future me, find a proper parser to extract directives.

	// 5.2.2.1 must-revalidate  - no stale
	if directiveincc(cachecontrol, "must-revalidate") {
		stale = false
	}

	// 5.2.2.2 no-cache .
	// Simple version without arguments, cache, but set ttl to 0 and dont allow stale.
	// Actual version, look for field names and handle them accordingly and dont mangle ttl.
	// TODO: Actually implement actual version
	if directiveincc(cachecontrol, "no-cache") {
		ttl = time.Duration(0)
		stale = false
	}
	// 5.2.2.3 no-store.
	// This is somewhat confusing and open to interpretation.
	// "the cache MUST NOT intentionally store the information in non-volatile storage"
	// For our case we will set store to false but let cache be true.
	// tl;dr allow this to be stored in memory, but not permanent storage?
	if directiveincc(cachecontrol, "no-store") {
		store = false
	}
	//5.2.2.4.  no-transform
	//We do nothing with it for now. Transformers need to determine this on their own.
	//5.2.2.5.  public
	//If public is present, request is cachable even if its normally not!
	if directiveincc(cachecontrol, "public") {
		cache = true
		store = true
	}
	// 5.2.2.6.  private
	// Simple version, if present, public cache may not cache or store response.
	// Actual version, if field names are present then simply hide them.
	// TODO: Implement the actual version.
	if self.ispublic && directiveincc(cachecontrol, "private") {
		cache = false
		store = false
	}
	//5.2.2.7.  proxy-revalidate. Just like must-revalidate but applies only to shared cache.
	if self.ispublic && directiveincc(cachecontrol, "proxy-revalidate") {
		stale = false
	}
	//5.2.2.8.  max-age and 5.2.2.9.  s-maxage implemented while calculating ttl
	//5.4.  Pragma
	// Pragma is looked at only if cache-control is missing.
	if len(cachecontrol) == 0 {
		pragma, pragmaok := reshdrs[http.CanonicalHeaderKey("Pragma")]
		if pragmaok {
			if directiveincc(pragma, "no-cache") {
				//no-cache appears in pragma, so dont cache.
				cache = false
				store = false
			}
		}
	}
	//progress http://tools.ietf.org/html/rfc7234#section-5.5
	return
}

//directiveincc checks if any of the headers contain a particular directive or not.
func directiveincc(cachecontrol []string, directive string) (present bool) {
	for _, c := range cachecontrol {
		if strings.Contains(c, directive) {
			present = true
			return
		}
	}
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
