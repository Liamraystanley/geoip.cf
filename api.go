// Copyright (c) Liam Stanley <me@liamstanley.io>. All rights reserved. Use
// of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/bluele/gcache"
	"github.com/go-chi/chi"
	bogon "github.com/lrstanley/go-bogon"
)

func registerAPI(r chi.Router) {
	r.Get("/api/{addr}", apiLookup)
	r.Get("/api/{addr}/{filter}", apiLookup)
}

func apiLookup(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "addr")
	var result *AddrResult

	// Allow users to query themselves without having to have them specify
	// their own IP address. Note that this will not work if you are querying
	// the IP address locally.
	if self := strings.ToLower(addr); self == "self" || self == "me" {
		addr, _, _ = net.SplitHostPort(r.RemoteAddr)
	}

	query, err := arc.GetIFPresent(addr)
	if err == nil {
		resultFromARC, _ := query.(AddrResult)
		result = &resultFromARC
		w.Header().Set("X-Cache", "HIT")
		debug.Printf("query %s fetched from arc cache", addr)
	} else {
		w.Header().Set("X-Cache", "MISS")
		if err != gcache.KeyNotFoundError {
			debug.Printf("unable to get %s off arc stack: %s", addr, err)
		}

		ip := net.ParseIP(addr)
		if ip == nil {
			var ips []string
			ips, err = net.LookupHost(addr)
			if err != nil || len(ips) == 0 {
				debug.Printf("error looking up %q as host address: %s", addr, err)
				http.NotFound(w, r)
				return
			}

			ip = net.ParseIP(ips[0])
		}

		if flags.NoBogon {
			if is, _ := bogon.Is(ip.String()); is {
				http.NotFound(w, r)
				return
			}
		}

		result, err = addrLookup(flags.DBPath, ip)
		if err != nil {
			debug.Printf("error looking up address %q (%q): %s", addr, ip, err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		if err = arc.Set(addr, *result); err != nil {
			debug.Printf("unable to add %s to arc cache: %s", addr, err)
		}
	}

	if filter := strings.Split(chi.URLParam(r, "filter"), ","); filter != nil && len(filter) > 0 && filter[0] != "" {
		base := make(map[string]*json.RawMessage)
		var tmp []byte

		tmp, err = json.Marshal(result)
		if err != nil {
			panic(err)
		}

		if err = json.Unmarshal(tmp, &base); err != nil {
			panic(err)
		}

		out := make([]string, len(filter))
		for i := 0; i < len(filter); i++ {
			out[i] = strings.Replace(fmt.Sprintf("%s", *base[filter[i]]), "\"", "", -1)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Join(out, "|")))
		return
	}

	enc := json.NewEncoder(w)

	if ok, _ := strconv.ParseBool(r.FormValue("pretty")); ok {
		enc.SetIndent("", "  ")
	}

	enc.SetEscapeHTML(false) // Otherwise the map url will get unicoded.
	w.Header().Set("Content-Type", "application/json")
	err = enc.Encode(result)
	if err != nil {
		panic(err)
	}
}

func dbDetailsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcache.RLock()
		if mcache.cache == nil {
			mcache.RUnlock()
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("X-Maxmind-Build", fmt.Sprintf("%d-%d", mcache.cache.IPVersion, mcache.cache.BuildEpoch))
		w.Header().Set("X-Maxmind-Type", mcache.cache.DatabaseType)
		mcache.RUnlock()

		next.ServeHTTP(w, r)
	})
}
