// SPDX-License-Identifier: Apache-2.0
// Package nativeproxy owns one authenticated, destination-restricted native
// API listener. It forwards original native bytes and never creates a request.
package nativeproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const MaxConnections = 16
const copyBufferSize = 32 << 10
const idleTimeout = 30 * time.Second

type Config struct {
	Origin     string
	Profile    domain.NetworkProfile
	Credential *domain.ProxyCredential
	// TLSConfig applies only to the upstream HTTPS proxy. Destination TLS is
	// owned and verified by Codex through the unchanged original server URL.
	TLSConfig *tls.Config
	Observed  func(context.Context)
	Failed    func(context.Context)
}

type Proxy struct {
	ctx         context.Context
	cancel      context.CancelFunc
	listener    net.Listener
	config      Config
	origin      *url.URL
	address     string
	localToken  string
	localAuth   string
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	localCount  int
	stopped     bool
	stopOnce    sync.Once
	observed    sync.Once
	failed      sync.Once
	wait        sync.WaitGroup
	accepted    chan struct{}
	done        chan struct{}
}

func invalid() error {
	return domain.Fail(domain.Unsupported, "The owned Codex API route is unavailable.", "Reconcile the original Worker profile; native routing has no direct or alternate fallback.")
}

func Open(ctx context.Context, config Config) (*Proxy, error) {
	if ctx.Err() != nil || rpc.ValidateEndpoint(config.Origin) != nil || config.Profile.Validate() != nil || config.Profile.Mode == domain.ProxyDirect || config.Profile.CredentialGeneration != "" && (config.Credential == nil || config.Credential.Validate() != nil) || config.Profile.CredentialGeneration == "" && config.Credential != nil {
		return nil, invalid()
	}
	u, err := url.Parse(config.Origin)
	if err != nil || !domain.ProxyHost(u.Hostname()) || u.RawPath != "" || u.ForceQuery {
		return nil, invalid()
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 || strconv.FormatUint(number, 10) != port {
		return nil, invalid()
	}
	address := net.JoinHostPort(u.Hostname(), port)
	if u.Scheme == "http" && !config.Profile.Bypasses(address) {
		return nil, domain.Fail(domain.PermissionDenied, "Plaintext loopback relay traffic requires an exact Worker bypass.", "Select Direct or explicitly bypass this exact paired loopback destination; the owned proxy never forwards its token through an upstream proxy.")
	}
	token, err := security.RandomToken()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, invalid()
	}
	life, cancel := context.WithCancel(ctx)
	if config.Credential != nil {
		original := *config.Credential
		config.Credential = &original
	}
	config.Profile.Bypass = append([]domain.ProxyBypass(nil), config.Profile.Bypass...)
	if config.TLSConfig != nil {
		config.TLSConfig = config.TLSConfig.Clone()
	}
	p := &Proxy{ctx: life, cancel: cancel, listener: listener, config: config, origin: u, address: address, localToken: token, localAuth: "Basic " + base64.StdEncoding.EncodeToString([]byte("delidev:"+token)), connections: map[net.Conn]struct{}{}, accepted: make(chan struct{}), done: make(chan struct{})}
	go p.accept()
	go func() {
		<-life.Done()
		p.stop()
	}()
	go func() {
		<-p.accepted
		p.wait.Wait()
		close(p.done)
	}()
	return p, nil
}

// NativeURL is transient credential delivery. Callers must neither serialize
// nor log it, and must register its credential with native output protection.
func (p *Proxy) NativeURL() string {
	u := url.URL{Scheme: "http", Host: p.listener.Addr().String(), User: url.UserPassword("delidev", p.localToken)}
	return u.String()
}

func (p *Proxy) ProtectedValues() []string { return []string{p.localToken, p.localAuth, p.NativeURL()} }

func (p *Proxy) accept() {
	defer close(p.accepted)
	for {
		connection, err := p.listener.Accept()
		if err != nil {
			p.cancel()
			return
		}
		p.mu.Lock()
		if p.stopped || p.localCount >= MaxConnections {
			p.mu.Unlock()
			connection.Close()
			continue
		}
		p.localCount++
		p.connections[connection] = struct{}{}
		p.wait.Add(1)
		p.mu.Unlock()
		go func() {
			defer p.wait.Done()
			defer func() {
				connection.Close()
				p.mu.Lock()
				delete(p.connections, connection)
				p.localCount--
				p.mu.Unlock()
			}()
			p.serve(connection)
		}()
	}
}

func (p *Proxy) stop() {
	p.stopOnce.Do(func() {
		p.cancel()
		p.mu.Lock()
		p.stopped = true
		p.listener.Close()
		for connection := range p.connections {
			connection.Close()
		}
		p.mu.Unlock()
	})
}

func (p *Proxy) Close() error {
	p.stop()
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
		return domain.Fail(domain.RecoveryRequired, "The owned native API proxy cleanup is unconfirmed.", "Retain original runtime ownership until every tunnel and observer is joined.")
	}
}

func (p *Proxy) noteUse() {
	p.observed.Do(func() {
		if p.config.Observed != nil {
			// This is a handler-owned callback. Close joins its enclosing handler;
			// observers must retain the bounded original runtime context.
			p.config.Observed(p.ctx)
		}
	})
}

func (p *Proxy) target(request *http.Request) bool {
	if request.URL.User != nil || request.URL.Fragment != "" || request.URL.RawPath != "" || request.URL.RawQuery != "" || request.URL.ForceQuery || request.Header.Get("Origin") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Upgrade") != "" {
		return false
	}
	if request.Method == http.MethodConnect {
		return p.origin.Scheme == "https" && request.RequestURI == p.address && request.Host == p.address && request.URL.Host == p.address && request.URL.Scheme == "" && request.URL.Path == "" && request.URL.Opaque == ""
	}
	return p.origin.Scheme == "http" && request.Method == http.MethodPost && request.URL.Scheme == "http" && request.URL.Host == p.origin.Host && request.Host == p.origin.Host && request.URL.Opaque == "" && (request.URL.Path == apiproxy.Prefix+"/responses" || request.URL.Path == apiproxy.Prefix+"/responses/compact")
}

func deny(connection net.Conn, code int) {
	connection.SetWriteDeadline(time.Now().Add(idleTimeout))
	response := &http.Response{StatusCode: code, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Connection": {"close"}}, ContentLength: 0, Close: true}
	_ = response.Write(connection)
}

// net/http derives Host from an absolute/CONNECT target and discards its raw
// header. Check that independently first so conflicting wire authorities cannot
// disappear during parsing. Retain prefetched body/tunnel bytes unchanged.
func readLocalRequest(reader *bufio.Reader) (*http.Request, *bufio.Reader, error) {
	var header bytes.Buffer
	var hosts []string
	for lineNumber := 0; ; lineNumber++ {
		line, err := reader.ReadString('\n')
		if err != nil || header.Len()+len(line) > 32<<10 || !strings.HasSuffix(line, "\r\n") {
			return nil, reader, invalid()
		}
		header.WriteString(line)
		if lineNumber > 0 {
			if line == "\r\n" {
				break
			}
			if line[0] == ' ' || line[0] == '\t' {
				return nil, reader, invalid()
			}
			name, value, found := strings.Cut(strings.TrimSuffix(line, "\r\n"), ":")
			if !found {
				return nil, reader, invalid()
			}
			if strings.EqualFold(name, "Host") {
				hosts = append(hosts, strings.TrimSpace(value))
			}
		}
	}
	parsed := bufio.NewReader(io.MultiReader(bytes.NewReader(header.Bytes()), reader))
	request, err := http.ReadRequest(parsed)
	if err != nil || len(hosts) != 1 || hosts[0] != request.Host {
		return nil, parsed, invalid()
	}
	return request, parsed, nil
}

func (p *Proxy) serve(local net.Conn) {
	local.SetReadDeadline(time.Now().Add(10 * time.Second))
	boundedHeaders := &headerConnection{Conn: local, remaining: 32 << 10}
	reader := bufio.NewReader(boundedHeaders)
	request, reader, err := readLocalRequest(reader)
	if err != nil {
		deny(local, http.StatusBadRequest)
		return
	}
	defer request.Body.Close()
	values := request.Header.Values("Proxy-Authorization")
	if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte(p.localAuth)) != 1 {
		deny(local, http.StatusProxyAuthRequired)
		return
	}
	if !p.target(request) || request.ContentLength > 32<<20 || request.Method == http.MethodConnect && (request.ContentLength > 0 || len(request.TransferEncoding) != 0) {
		deny(local, http.StatusForbidden)
		return
	}
	boundedHeaders.remaining = -1
	request.Header.Del("Proxy-Authorization")
	life, cancel := context.WithTimeout(p.ctx, 15*time.Minute)
	defer cancel()
	upstream, err := outbound.DialPinned(life, p.config.Profile, p.config.Credential, p.address, p.config.TLSConfig)
	if err != nil {
		p.failed.Do(func() {
			if p.config.Failed != nil {
				p.config.Failed(p.ctx)
			}
		})
		deny(local, http.StatusBadGateway)
		return
	}
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		upstream.Close()
		return
	}
	p.connections[upstream] = struct{}{}
	p.mu.Unlock()
	defer func() {
		upstream.Close()
		p.mu.Lock()
		delete(p.connections, upstream)
		p.mu.Unlock()
	}()
	cancellationDone := make(chan struct{})
	stop := context.AfterFunc(life, func() { local.Close(); upstream.Close(); close(cancellationDone) })
	defer func() {
		if !stop() {
			<-cancellationDone
		}
	}()
	if request.Method == http.MethodConnect {
		local.SetWriteDeadline(time.Now().Add(idleTimeout))
		if _, err := io.WriteString(local, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		p.tunnel(local, upstream, reader)
		return
	}
	local.SetReadDeadline(time.Now().Add(idleTimeout))
	request.Body = http.MaxBytesReader(nil, request.Body, 32<<20)
	request.Close = true
	request.RequestURI = ""
	upstream.SetWriteDeadline(time.Now().Add(idleTimeout))
	if request.Write(upstream) != nil {
		return
	}
	upstream.SetReadDeadline(time.Now().Add(10 * time.Minute))
	header := &headerConnection{Conn: upstream, remaining: 32 << 10}
	response, err := http.ReadResponse(bufio.NewReader(header), request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	header.remaining = -1
	response.Header.Del("Proxy-Authenticate")
	response.Header.Del("Proxy-Authorization")
	response.Close = true
	response.Trailer = nil
	response.Header.Del("Trailer")
	response.Body = &boundedResponse{source: response.Body, connection: upstream, remaining: 256 << 20}
	p.noteUse()
	_ = response.Write(&idleConnection{Conn: local})
}

func (p *Proxy) tunnel(local, upstream net.Conn, reader *bufio.Reader) {
	local.SetDeadline(time.Time{})
	upstream.SetDeadline(time.Time{})
	finished := make(chan int64, 2)
	var fromNative, fromServer atomic.Bool
	nativeWriter := &observedWriter{destination: &idleConnection{Conn: upstream}, wrote: &fromNative, other: &fromServer, observe: p.noteUse}
	serverWriter := &observedWriter{destination: &idleConnection{Conn: local}, wrote: &fromServer, other: &fromNative, observe: p.noteUse}
	go func() {
		count, _ := io.CopyBuffer(nativeWriter, &idleReader{source: reader, connection: local}, make([]byte, copyBufferSize))
		finished <- count
	}()
	go func() {
		count, _ := io.CopyBuffer(serverWriter, &idleConnection{Conn: upstream}, make([]byte, copyBufferSize))
		finished <- count
	}()
	first := <-finished
	local.Close()
	upstream.Close()
	second := <-finished
	if first > 0 && second > 0 {
		p.noteUse()
	}
}

type headerConnection struct {
	net.Conn
	remaining int
}

type observedWriter struct {
	destination io.Writer
	wrote       *atomic.Bool
	other       *atomic.Bool
	observe     func()
}

func (w *observedWriter) Write(buffer []byte) (int, error) {
	count, err := w.destination.Write(buffer)
	if count > 0 {
		w.wrote.Store(true)
		if w.other.Load() {
			w.observe()
		}
	}
	return count, err
}

func (c *headerConnection) Read(buffer []byte) (int, error) {
	if c.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if c.remaining > 0 && len(buffer) > c.remaining {
		buffer = buffer[:c.remaining]
	}
	count, err := c.Conn.Read(buffer)
	if c.remaining > 0 {
		c.remaining -= count
	}
	return count, err
}

type idleConnection struct{ net.Conn }

func (c *idleConnection) Read(buffer []byte) (int, error) {
	c.SetReadDeadline(time.Now().Add(idleTimeout))
	return c.Conn.Read(buffer)
}
func (c *idleConnection) Write(buffer []byte) (int, error) {
	c.SetWriteDeadline(time.Now().Add(idleTimeout))
	return c.Conn.Write(buffer)
}

type idleReader struct {
	source     io.Reader
	connection net.Conn
}

func (r *idleReader) Read(buffer []byte) (int, error) {
	r.connection.SetReadDeadline(time.Now().Add(idleTimeout))
	return r.source.Read(buffer)
}

type boundedResponse struct {
	source     io.ReadCloser
	connection net.Conn
	remaining  int64
}

func (r *boundedResponse) Read(buffer []byte) (int, error) {
	r.connection.SetReadDeadline(time.Now().Add(idleTimeout))
	if r.remaining <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	count, err := r.source.Read(buffer)
	r.remaining -= int64(count)
	return count, err
}
func (r *boundedResponse) Close() error { return r.source.Close() }

// RedactedLabel deliberately excludes every authority and credential.
func (p *Proxy) String() string {
	return "owned native API proxy"
}
