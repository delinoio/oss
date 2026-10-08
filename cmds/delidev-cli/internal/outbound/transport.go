// SPDX-License-Identifier: Apache-2.0
// Package outbound applies explicit server-owned routing without ambient
// settings, destination fallback, redirects or connection reuse.
package outbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/net/proxy"
)

type Resolver func(context.Context) (domain.NetworkProfile, []byte, error)
type Transport struct {
	Base    *http.Transport
	Resolve Resolver
}

func unavailable() error {
	return domain.Fail(domain.Unavailable, "The selected outbound proxy could not complete the connection.", "Check the selected proxy; routing never falls back to direct or another profile.")
}

// DialPinned is for the separately owned native API tunnel. Its caller has
// already authenticated and restricted the exact destination. Bypass remains
// a positive configuration decision; a failed proxy never selects Direct.
func DialPinned(ctx context.Context, profile domain.NetworkProfile, credential *domain.ProxyCredential, address string, tlsConfig *tls.Config) (net.Conn, error) {
	if profile.Validate() != nil || profile.CredentialGeneration != "" && (credential == nil || credential.Validate() != nil) || profile.CredentialGeneration == "" && credential != nil {
		return nil, unavailable()
	}
	if profile.Mode == domain.ProxyDirect || profile.Bypasses(address) {
		return DirectDial(ctx, "tcp", address)
	}
	var value domain.ProxyCredential
	if credential != nil {
		value = *credential
	}
	connection, err := dialProxy(ctx, profile, value, "tcp", address, tlsConfig)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.SafeError(ctx.Err())
		}
		return nil, unavailable()
	}
	return connection, nil
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	p, raw, err := t.Resolve(req.Context())
	defer clear(raw)
	if err != nil {
		return nil, err
	}
	if err = p.Validate(); err != nil {
		return nil, unavailable()
	}
	if req.URL.Scheme == "http" && p.Mode != domain.ProxyDirect {
		host, port := req.URL.Hostname(), req.URL.Port()
		if port == "" {
			port = "80"
		}
		ip := net.ParseIP(host)
		if (strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) && !p.Bypasses(net.JoinHostPort(host, port)) {
			// A loopback HTTP provider is safe only on this server. CONNECT or
			// SOCKS5 would move its plaintext account key into the proxy's trust
			// boundary. Reject before dialing; never silently switch routes.
			return nil, domain.Fail(domain.PermissionDenied, "Plaintext loopback endpoints require Direct or an exact bypass.", "Select Direct, configure an exact destination bypass, or use a verified HTTPS endpoint.")
		}
	}
	var credential domain.ProxyCredential
	if p.CredentialGeneration != "" {
		if domain.Decode(raw, &credential) != nil || credential.Validate() != nil {
			return nil, unavailable()
		}
	} else if len(raw) != 0 {
		return nil, unavailable()
	}
	transport := t.Base.Clone()
	transport.Proxy = nil
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if p.Mode == domain.ProxyDirect || p.Bypasses(address) {
			dial := t.Base.DialContext
			if dial == nil {
				dial = (&net.Dialer{}).DialContext
			}
			return dialDirectDestination(ctx, network, address, dial)
		}
		conn, err := dialProxy(ctx, p, credential, network, address, t.Base.TLSClientConfig)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, unavailable()
		}
		return conn, nil
	}
	// No reusable sockets survive a pinned request. Streaming response bodies
	// retain only their own connection; closing them releases it normally.
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(req)
	if err == nil && p.CredentialGeneration != "" {
		guard := newCredentialBody(response.Body, credential)
		for name, values := range response.Header {
			if guard.containsHeaderName(name) {
				guard.Close()
				return nil, unavailable()
			}
			for _, value := range values {
				if value == credential.Username || value == credential.Password || guard.contains([]byte(value)) {
					guard.Close()
					return nil, unavailable()
				}
			}
		}
		// net/http fills the original response's trailers when its body reaches
		// EOF, including undeclared fields. Keep that response private: these
		// outbound clients have no trailer contract, and exposing an empty map
		// on the original response would let EOF repopulate it with credentials.
		protected := *response
		protected.Header = response.Header.Clone()
		protected.Header.Del("Trailer")
		protected.Trailer = nil
		protected.Body = guard
		response = &protected
	}
	return response, err
}

func dialProxy(ctx context.Context, p domain.NetworkProfile, c domain.ProxyCredential, network, address string, tlsConfig *tls.Config) (net.Conn, error) {
	dialer := directDialer{}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	proxyAddress := net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))
	if p.Mode == domain.ProxySOCKS5 {
		var auth *proxy.Auth
		if p.CredentialGeneration != "" {
			auth = &proxy.Auth{User: c.Username, Password: c.Password}
		}
		d, err := proxy.SOCKS5("tcp", proxyAddress, auth, dialer)
		if err != nil {
			return nil, err
		}
		return d.(proxy.ContextDialer).DialContext(bounded, network, address)
	}
	conn, err := dialer.DialContext(bounded, network, proxyAddress)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	owned := conn
	stop := context.AfterFunc(bounded, func() { owned.Close() })
	defer stop()
	if p.Mode == domain.ProxyHTTPS {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if tlsConfig != nil {
			config = tlsConfig.Clone()
		}
		config.ServerName = p.Host
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(bounded); err != nil {
			return nil, err
		}
		conn = secure
	}
	// Tunnel HTTP as well as HTTPS. Proxy credentials are present only in this
	// CONNECT exchange, never in a URL or a provider/GitHub request header.
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
	if p.CredentialGeneration != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password)))
	}
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	req.Header.Del("Proxy-Authorization")
	limited := &headerConn{Conn: conn, remaining: 32 << 10}
	reader := bufio.NewReader(limited)
	response, err := http.ReadResponse(reader, req)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, unavailable()
	}
	if bounded.Err() != nil {
		return nil, bounded.Err()
	}
	if !stop() {
		return nil, unavailable()
	}
	limited.remaining = -1
	success = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type directDialer struct{}

func (directDialer) Dial(network, address string) (net.Conn, error) {
	return DirectDial(context.Background(), network, address)
}
func (directDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return DirectDial(ctx, network, address)
}

type headerConn struct {
	net.Conn
	remaining int
}

func (c *headerConn) Read(p []byte) (int, error) {
	if c.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if c.remaining > 0 && len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	if c.remaining > 0 {
		c.remaining -= n
	}
	return n, err
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// DirectDial pins localhost to actual loopback, including proxy endpoints.
func DirectDial(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	return dialDirectDestination(ctx, network, address, d.DialContext)
}

// Keep the original TLS hostname on the request. Only the socket destination
// changes, before any caller dialer can resolve localhost through ambient DNS.
func dialDirectDestination(ctx context.Context, network, address string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, unavailable()
	}
	if strings.EqualFold(host, "localhost") {
		for _, ip := range []string{"127.0.0.1", "::1"} {
			conn, e := dial(ctx, network, net.JoinHostPort(ip, port))
			if e == nil {
				return conn, nil
			}
			err = e
			if ctx.Err() != nil {
				break
			}
		}
		return nil, err
	}
	return dial(ctx, network, address)
}
