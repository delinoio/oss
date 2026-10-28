// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// ApprovedOrigins is operator configuration, never provider/user configuration.
// Each entry is an exact scheme://host:port origin (default ports can be omitted).
type DestinationPolicy struct {
	// ProxyURL and NoProxy are explicit operator settings, never inherited from environment.
	// ProxyURL accepts HTTP CONNECT, HTTPS CONNECT, or SOCKS5. A failed proxy never falls back direct.
	ProxyURL        string
	NoProxy         []string
	ApprovedOrigins []string
	Rules           []DestinationRule
	Resolver        Resolver
	// DialContext receives only an already validated, pinned IP:port. It cannot
	// replace destination validation or the subsequent first-plaintext-write gate.
	DialContext func(context.Context, string, string) (net.Conn, error)
	// TLSConfig may add trust roots/client identity; verification cannot be disabled.
	TLSConfig *tls.Config
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func parseDestination(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || strings.Contains(u.Host, "%") || strings.HasSuffix(u.Hostname(), ".") {
		return nil, ErrDestination
	}
	return u, nil
}
func (p DestinationPolicy) approved(u *url.URL) bool {
	for _, s := range p.ApprovedOrigins {
		a, e := parseDestination(s)
		if e == nil && (a.Path == "" || a.Path == "/") && origin(a) == origin(u) {
			return true
		}
	}
	return false
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	// Deny special-use ranges that IsGlobalUnicast deliberately accepts.
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}

// client pins each dial to validated addresses. It never re-resolves an approved hostname
// through another transport, follows redirects, or inherits process proxy settings.
func (p DestinationPolicy) client(raw string) (*http.Client, bool, error) {
	u, e := parseDestination(raw)
	if e != nil {
		return nil, false, e
	}
	if p.ProxyURL != "" {
		if _, err := proxyURL(p.ProxyURL); err != nil {
			return nil, false, err
		}
	}
	rules, err := p.rulesForOrigin(origin(u))
	if err != nil {
		return nil, false, err
	}
	allowed := p.approved(u) || len(rules) > 0
	if u.Scheme != "https" && !allowed {
		return nil, false, ErrDestination
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.TLSConfig != nil {
		tlsConfig = p.TLSConfig.Clone()
		if tlsConfig.InsecureSkipVerify {
			return nil, false, ErrDestination
		}
		if tlsConfig.MinVersion < tls.VersionTLS12 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
		tlsConfig.ServerName = ""
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 30 * time.Second, ResponseHeaderTimeout: 300 * time.Second, DisableKeepAlives: true}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || !strings.EqualFold(host, u.Hostname()) {
			return nil, ErrDestination
		}
		ips, e := resolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, ErrDestination
		}
		for _, ip := range ips {
			if !ip.IsValid() || (!allowed && !publicIP(ip)) || !matchesRules(ip, rules) {
				return nil, ErrDestination
			}
		}
		useProxy := p.ProxyURL != ""
		if useProxy {
			allBypass := true
			for _, ip := range ips {
				if !p.bypassProxy(u, ip) {
					allBypass = false
				}
			}
			useProxy = !allBypass
		}
		d := net.Dialer{Timeout: 30 * time.Second}
		var last error
		for _, ip := range ips {
			var c net.Conn
			var e error
			if useProxy {
				c, e = p.dialProxy(ctx, net.JoinHostPort(ip.String(), port))
			} else {
				if p.DialContext != nil {
					c, e = p.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				} else {
					c, e = d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				}
			}
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, allowed, nil
}
