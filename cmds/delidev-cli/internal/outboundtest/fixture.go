// SPDX-License-Identifier: Apache-2.0
// Package outboundtest provides isolated local proxy fixtures, never product
// routing or a runtime dependency. Only test code imports it.
package outboundtest

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
)

const Username = "fixture-proxy-user"
const Password = "fixture-proxy-password"
const Credential = `{"username":"fixture-proxy-user","password":"fixture-proxy-password"}`

type Fixture struct {
	Profile          domain.NetworkProfile
	Calls            atomic.Int32
	Targets          chan string
	Roots            *x509.CertPool
	ProxyCertificate *x509.Certificate
}

func (f *Fixture) Resolve(context.Context) (domain.NetworkProfile, []byte, error) {
	return f.Profile, []byte(Credential), nil
}
func (f *Fixture) Resolver() outbound.Resolver { return f.Resolve }
func profile(mode domain.ProxyMode, address string) domain.NetworkProfile {
	host, port, _ := net.SplitHostPort(address)
	n, _ := strconv.ParseUint(port, 10, 16)
	return domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: mode, Host: host, Port: uint16(n)}, CredentialGeneration: domain.NewID()}
}
func Tunnel(client net.Conn, reader io.Reader, target string) {
	upstream, err := net.DialTimeout("tcp", target, 2*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{})
	go func() { io.Copy(upstream, reader); upstream.Close(); close(done) }()
	io.Copy(client, upstream)
	client.Close()
	<-done
}
func Connect(t *testing.T, target string, secure bool) *Fixture {
	t.Helper()
	f := &Fixture{Targets: make(chan string, 32)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Calls.Add(1)
		f.Targets <- r.Host
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(Username+":"+Password)) {
			w.WriteHeader(407)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		Tunnel(conn, rw, target)
	}))
	mode := domain.ProxyHTTP
	if secure {
		server.StartTLS()
		mode = domain.ProxyHTTPS
		f.Roots = x509.NewCertPool()
		f.Roots.AddCert(server.Certificate())
		f.ProxyCertificate = server.Certificate()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	f.Profile = profile(mode, server.Listener.Addr().String())
	return f
}
func SOCKS5(t *testing.T, target string) *Fixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{Profile: profile(domain.ProxySOCKS5, listener.Addr().String()), Targets: make(chan string, 32)}
	var wg sync.WaitGroup
	var mu sync.Mutex
	live := map[net.Conn]bool{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			live[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				defer func() { mu.Lock(); delete(live, conn); mu.Unlock() }()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				header := make([]byte, 2)
				if _, err := io.ReadFull(r, header); err != nil || header[0] != 5 {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(r, methods); err != nil {
					return
				}
				conn.Write([]byte{5, 2})
				if _, err := io.ReadFull(r, header); err != nil || header[0] != 1 {
					return
				}
				user := make([]byte, int(header[1]))
				if _, err := io.ReadFull(r, user); err != nil {
					return
				}
				n, err := r.ReadByte()
				if err != nil {
					return
				}
				pass := make([]byte, int(n))
				if _, err := io.ReadFull(r, pass); err != nil || string(user) != Username || string(pass) != Password {
					conn.Write([]byte{1, 1})
					return
				}
				conn.Write([]byte{1, 0})
				request := make([]byte, 4)
				if _, err := io.ReadFull(r, request); err != nil || request[0] != 5 || request[1] != 1 {
					return
				}
				var host string
				switch request[3] {
				case 1:
					b := make([]byte, 4)
					if _, err := io.ReadFull(r, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 4:
					b := make([]byte, 16)
					if _, err := io.ReadFull(r, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 3:
					n, err := r.ReadByte()
					if err != nil {
						return
					}
					b := make([]byte, int(n))
					if _, err := io.ReadFull(r, b); err != nil {
						return
					}
					host = string(b)
				default:
					return
				}
				port := make([]byte, 2)
				if _, err := io.ReadFull(r, port); err != nil {
					return
				}
				f.Calls.Add(1)
				f.Targets <- net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
				conn.SetDeadline(time.Time{})
				Tunnel(conn, r, target)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for c := range live {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return f
}

func TLSOrigin(t *testing.T, name string, handler http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if ip := net.ParseIP(name); ip != nil {
		cert.DNSNames = nil
		cert.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	s := httptest.NewUnstartedServer(handler)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s, roots
}
func Address(url string) string {
	return strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "https://")
}

// DenyDirect reserves a destination that immediately refuses every direct
// connection. Proxy fixtures alone map that authority to the actual origin.
func DenyDirect(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			calls.Add(1)
			conn.Close()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	return listener.Addr().String(), &calls
}

func Wait(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal(label)
	}
}
