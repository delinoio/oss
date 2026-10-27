// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (p DestinationPolicy) bypassProxy(u *url.URL, ip netip.Addr) bool {
	host := strings.ToLower(u.Hostname())
	for _, rule := range p.NoProxy {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "*" {
			return true
		}
		if prefix, e := netip.ParsePrefix(rule); e == nil && prefix.Contains(ip) {
			return true
		}
		if addr, e := netip.ParseAddr(rule); e == nil && addr.Unmap() == ip.Unmap() {
			return true
		}
		if h, port, e := net.SplitHostPort(rule); e == nil {
			if port != u.Port() && !(port == "443" && u.Scheme == "https" && u.Port() == "") && !(port == "80" && u.Scheme == "http" && u.Port() == "") {
				continue
			}
			rule = h
		}
		rule = strings.TrimPrefix(strings.TrimPrefix(rule, "*"), ".")
		if rule != "" && (host == rule || strings.HasSuffix(host, "."+rule)) {
			return true
		}
	}
	return false
}
func proxyURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || strings.Contains(u.Host, "%") || strings.HasSuffix(u.Hostname(), ".") {
		return nil, ErrDestination
	}
	switch u.Scheme {
	case "http", "https", "socks5":
	default:
		return nil, ErrDestination
	}
	return u, nil
}
func (p DestinationPolicy) dialProxy(ctx context.Context, address string) (net.Conn, error) {
	u, e := proxyURL(p.ProxyURL)
	if e != nil {
		return nil, e
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		case "socks5":
			port = "1080"
		}
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, e := resolver.LookupNetIP(ctx, "ip", u.Hostname())
	if e != nil {
		return nil, e
	}
	var conn net.Conn
	for _, ip := range ips {
		if !ip.IsValid() {
			return nil, ErrDestination
		}
		d := net.Dialer{Timeout: 30 * time.Second}
		conn, e = d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if e == nil {
			break
		}
	}
	if conn == nil {
		if e != nil {
			return nil, e
		}
		return nil, ErrDestination
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	rawConnection := conn
	stop := context.AfterFunc(ctx, func() { rawConnection.Close() })
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	if u.Scheme == "https" {
		secure := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		if e = secure.HandshakeContext(ctx); e != nil {
			return nil, e
		}
		conn = secure
	}
	if u.Scheme == "socks5" {
		e = socksTunnel(conn, u, address)
	} else {
		e = connectTunnel(conn, u, address)
	}
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	conn.SetDeadline(time.Time{})
	success = true
	return conn, nil
}
func connectTunnel(conn net.Conn, u *url.URL, address string) error {
	header := "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\n"
	if u.User != nil {
		password, _ := u.User.Password()
		header += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+password)) + "\r\n"
	}
	if _, e := io.WriteString(conn, header+"\r\n"); e != nil {
		return e
	}
	// CONNECT carries no inference payload. Refuse oversized proxy headers before TLS.
	reader := bufio.NewReader(io.LimitReader(conn, 32<<10))
	resp, e := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if e != nil {
		return ErrDestination
	}
	if resp.StatusCode != 200 {
		return ErrDestination
	}
	if reader.Buffered() != 0 {
		return ErrDestination
	}
	return nil
}
func socksTunnel(conn net.Conn, u *url.URL, address string) error {
	methods := []byte{5, 1, 0}
	if u.User != nil {
		methods = []byte{5, 1, 2}
	}
	if _, e := conn.Write(methods); e != nil {
		return e
	}
	answer := make([]byte, 2)
	if _, e := io.ReadFull(conn, answer); e != nil {
		return e
	}
	if answer[0] != 5 || answer[1] != methods[2] {
		return ErrDestination
	}
	if answer[1] == 2 {
		password, _ := u.User.Password()
		user := u.User.Username()
		if len(user) > 255 || len(password) > 255 {
			return ErrDestination
		}
		auth := append([]byte{1, byte(len(user))}, []byte(user)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, e := conn.Write(auth); e != nil {
			return e
		}
		if _, e := io.ReadFull(conn, answer); e != nil {
			return e
		}
		if answer[0] != 1 || answer[1] != 0 {
			return ErrDestination
		}
	}
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return e
	}
	ip, e := netip.ParseAddr(host)
	if e != nil {
		return ErrDestination
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return ErrDestination
	}
	request := []byte{5, 1, 0, 1}
	if ip.Is6() {
		request[3] = 4
	}
	request = append(request, ip.AsSlice()...)
	request = append(request, byte(n>>8), byte(n))
	if _, e = conn.Write(request); e != nil {
		return e
	}
	reply := make([]byte, 4)
	if _, e = io.ReadFull(conn, reply); e != nil {
		return e
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 {
		return ErrDestination
	}
	size := 0
	switch reply[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		b := make([]byte, 1)
		if _, e = io.ReadFull(conn, b); e != nil {
			return e
		}
		size = int(b[0])
	default:
		return fmt.Errorf("invalid proxy reply")
	}
	_, e = io.ReadFull(conn, make([]byte, size+2))
	return e
}
