// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestExplicitProxyTunnelAndNoDirectFallback(t *testing.T) {
	var targetCalls, proxyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); io.WriteString(w, "ok") }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Method != "CONNECT" {
			t.Error("expected CONNECT")
			w.WriteHeader(400)
			return
		}
		host, _, e := net.SplitHostPort(r.Host)
		if e != nil || net.ParseIP(host) == nil {
			t.Error("proxy received unpinned destination")
		}
		upstream, e := net.Dial("tcp", r.Host)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		client, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			upstream.Close()
			return
		}
		io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() {
			defer client.Close()
			defer upstream.Close()
			done := make(chan struct{})
			go func() { io.Copy(upstream, client); close(done) }()
			io.Copy(client, upstream)
			client.Close()
			upstream.Close()
			<-done
		}()
	}))
	defer proxy.Close()
	p := DestinationPolicy{ApprovedOrigins: []string{target.URL}, ProxyURL: proxy.URL}
	gate := func(ctx context.Context, start func(context.Context) error) error { return start(ctx) }
	req, _ := http.NewRequest("GET", target.URL, nil)
	resp, e := p.Do(context.Background(), req, gate, nil)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" || targetCalls.Load() != 1 || proxyCalls.Load() != 1 {
		t.Fatal("proxy tunnel failed")
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer broken.Close()
	p.ProxyURL = broken.URL
	if _, e = p.Do(context.Background(), req, gate, nil); e == nil {
		t.Fatal("broken proxy accepted")
	}
	if targetCalls.Load() != 1 {
		t.Fatal("failed proxy retried direct")
	}
	p.NoProxy = []string{"127.0.0.1"}
	resp, e = p.Do(context.Background(), req, gate, nil)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if targetCalls.Load() != 2 {
		t.Fatal("explicit NO_PROXY ignored")
	}
}
func TestNoProxyDomainBoundaries(t *testing.T) {
	p := DestinationPolicy{NoProxy: []string{".example.com"}}
	for _, fixture := range []struct {
		host string
		want bool
	}{{"api.example.com", true}, {"example.com", true}, {"notexample.com", false}, {"example.com.evil.test", false}} {
		u, _ := url.Parse("https://" + fixture.host)
		if p.bypassProxy(u, netip.MustParseAddr("8.8.8.8")) != fixture.want {
			t.Fatal(fixture.host)
		}
	}
}

func TestSOCKS5AuthenticatesAndPinsAddress(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		greeting := make([]byte, 3)
		if _, e := io.ReadFull(server, greeting); e != nil {
			done <- e
			return
		}
		if !bytes.Equal(greeting, []byte{5, 1, 2}) {
			done <- fmt.Errorf("unexpected SOCKS auth greeting")
			return
		}
		server.Write([]byte{5, 2})
		head := make([]byte, 2)
		io.ReadFull(server, head)
		user := make([]byte, int(head[1]))
		io.ReadFull(server, user)
		n := make([]byte, 1)
		io.ReadFull(server, n)
		password := make([]byte, int(n[0]))
		io.ReadFull(server, password)
		if string(user) != "operator" || string(password) != "proxy-secret" {
			done <- fmt.Errorf("bad proxy credentials")
			return
		}
		server.Write([]byte{1, 0})
		request := make([]byte, 10)
		if _, e := io.ReadFull(server, request); e != nil {
			done <- e
			return
		}
		if !bytes.Equal(request, []byte{5, 1, 0, 1, 8, 8, 8, 8, 1, 187}) {
			done <- fmt.Errorf("SOCKS destination was not pinned")
			return
		}
		_, e := server.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		done <- e
	}()
	u, _ := url.Parse("socks5://operator:proxy-secret@proxy.invalid")
	client.SetDeadline(time.Now().Add(time.Second))
	if e := socksTunnel(client, u, "8.8.8.8:443"); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
