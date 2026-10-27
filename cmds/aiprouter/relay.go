// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

func inferencePath(path string) bool {
	switch path {
	case "/v1/responses", "/v1/responses/compact", "/v1/chat/completions", "/v1/messages", "/v1/messages/count_tokens", "/v1/models", "/v1/images/generations", "/v1/images/edits", "/v1/alpha/search":
		return true
	}
	return strings.HasPrefix(path, "/v1/models/") || strings.HasPrefix(path, "/v1/aiprouter/artifacts/") || strings.HasPrefix(path, "/v1/opencodex/artifacts/")
}
func RelayHandler(p Profile, store Credentials, transport http.RoundTripper) (http.Handler, error) {
	target, err := serverURL(p.Server)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "remote inference connection failed", http.StatusBadGateway)
	}}
	proxy.Rewrite = func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = target.Host
		r.Out.Header.Del("Cookie")
		r.Out.Header.Del("Authorization")
		r.Out.Header.Del("X-Api-Key")
		r.Out.Header.Del("X-Opencodex-Api-Key")
		r.Out.Header.Del("Forwarded")
		r.Out.Header.Del("X-Forwarded-For")
		r.Out.Header.Del("X-Forwarded-Host")
		r.Out.Header.Del("X-Forwarded-Proto")
		r.Out.Header.Set("Authorization", "Bearer "+r.In.Context().Value(relayKey{}).(string))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, e := net.SplitHostPort(r.Host)
		if e != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "loopback host required", http.StatusForbidden)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not admitted by this relay", http.StatusForbidden)
			return
		}
		if !inferencePath(r.URL.Path) || strings.Contains(r.URL.Path, "..") || r.URL.RawPath != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		key, e := store.Get(credentialID(p))
		if e != nil || key == "" {
			http.Error(w, "profile credential unavailable", 503)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), relayKey{}, key)))
	}), nil
}

type relayKey struct{}

func RunRelay(ctx context.Context, p Profile, store Credentials) error {
	if e := p.Validate(); e != nil {
		return e
	}
	h, e := RelayHandler(p, store, nil)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", p.Port))
	if e != nil {
		return fmt.Errorf("cannot bind fixed loopback port %d; stop the competing listener or preview and confirm a port change", p.Port)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-done:
		}
	}()
	e = srv.Serve(listener)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
