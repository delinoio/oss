// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NativeTLSConfig struct {
	CLIModels           []ClientModel
	Address             string
	Policy              NativeCompatibilityPolicy
	CA                  *NativeCA
	Gateway, Key, Token string
	Models              []ClientModel
	TrialUntil          time.Time
	OnReady             func()
	Transport           http.RoundTripper
}

// RunNativeTLS serves only the selected native protocol on a fixed loopback port.
// CONNECT authentication confines access to the explicitly configured application.
func RunNativeTLS(ctx context.Context, cfg NativeTLSConfig) error {
	host, port, err := net.SplitHostPort(cfg.Address)
	ip := net.ParseIP(host)
	n, _ := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || n < 1 || n > 65535 || len(cfg.Token) != 64 || cfg.CA == nil || cfg.CA.TLSCertificate.PrivateKey == nil || len(cfg.Policy.Hosts()) == 0 {
		return errors.New("invalid native loopback runtime configuration")
	}
	gateway, err := serverURL(cfg.Gateway)
	if err != nil || cfg.Key == "" {
		return errors.New("native gateway credential or origin unavailable")
	}
	for _, h := range cfg.Policy.Hosts() {
		if cfg.CA.TLSCertificate.Leaf == nil || cfg.CA.TLSCertificate.Leaf.VerifyHostname(h) != nil {
			return errors.New("native certificate does not cover selected destination")
		}
	}
	leaf := cfg.CA.TLSCertificate.Leaf
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || now.Before(cfg.CA.Certificate.NotBefore) || !now.Before(cfg.CA.Certificate.NotAfter) {
		return errors.New("native certificate validity expired or pending")
	}
	deadline := leaf.NotAfter
	if cfg.CA.Certificate.NotAfter.Before(deadline) {
		deadline = cfg.CA.Certificate.NotAfter
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if !cfg.TrialUntil.IsZero() {
		var stop context.CancelFunc
		ctx, stop = context.WithDeadline(ctx, cfg.TrialUntil)
		defer stop()
	}
	transport := cfg.Transport
	if transport == nil {
		transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConns: 32, MaxConnsPerHost: 32, DisableCompression: true}
	}
	if t, ok := transport.(*http.Transport); ok {
		defer t.CloseIdleConnections()
	}
	ln, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return errors.New("native fixed loopback port is occupied; preview and confirm a different port")
	}
	defer ln.Close()
	slog.Info("native compatibility listener started", "mode", cfg.Policy.mode, "address", cfg.Address)
	defer slog.Info("native compatibility listener stopped", "mode", cfg.Policy.mode, "address", cfg.Address)
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 64)
	pacToken := make([]byte, 32)
	if _, err = rand.Read(pacToken); err != nil {
		return errors.New("native PAC entropy unavailable")
	}
	pacPath := "/" + hex.EncodeToString(pacToken) + "/proxy.pac"
	pacURL := "http://" + cfg.Address + pacPath
	codex := cfg.Policy.mode == NativeCompatibilityCodexObserve || cfg.Policy.mode == NativeCompatibilityCodexAccountUITrial
	proxyAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("tairouter:"+NativeProxyPassword(cfg.Token)))
	matches := func(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
	control, err := newNativeControlServer(cfg.Token)
	if err != nil {
		return errors.New("native listener generation unavailable")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			if codex && r.URL.Path == pacPath && r.Method == http.MethodGet && r.Header.Get("Origin") == "" {
				w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
				w.Header().Set("Cache-Control", "no-store")
				script := "function FindProxyForURL(url, host) { if (host.toLowerCase() === \"chatgpt.com\" && url.substring(0, 6) === \"https:\") return \"PROXY " + cfg.Address + "; DIRECT\"; return \"DIRECT\"; }"
				if !cfg.TrialUntil.IsZero() {
					script = "function FindProxyForURL(url, host) { if (Date.now() >= " + strconv.FormatInt(cfg.TrialUntil.UnixMilli(), 10) + ") return \"DIRECT\"; if (host.toLowerCase() === \"chatgpt.com\" && url.substring(0,6) === \"https:\") return \"PROXY " + cfg.Address + "; DIRECT\"; return \"DIRECT\"; }"
				}
				io.WriteString(w, script)
				return
			}
			if control.Challenge(w, r) {
				return
			}
			if !control.Authorize(r) || r.Header.Get("Origin") != "" {
				http.Error(w, "native control authentication required", 403)
				return
			}
			if codex && r.URL.Path == "/_tairouter/native/launch" && r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				body, _ := json.Marshal(map[string]string{"pacURL": pacURL})
				nativeControlRespond(w, r, cfg.Token, http.StatusOK, body)
				return
			}
			if r.URL.Path == "/_tairouter/native/health" && r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				if ctx.Err() != nil || !time.Now().Before(leaf.NotAfter) {
					nativeControlRespond(w, r, cfg.Token, http.StatusServiceUnavailable, []byte(`{"status":"expired"}`))
					return
				}
				fingerprint := sha256.Sum256(leaf.Raw)
				body, _ := json.Marshal(map[string]string{"status": "ready", "caFingerprint": cfg.CA.Fingerprint, "leafFingerprint": hex.EncodeToString(fingerprint[:]), "leafNotAfter": leaf.NotAfter.UTC().Format(time.RFC3339Nano)})
				nativeControlRespond(w, r, cfg.Token, http.StatusOK, body)
				return
			}
			if r.URL.Path == "/_tairouter/native/stop" && r.Method == http.MethodPost {
				nativeControlRespond(w, r, cfg.Token, http.StatusOK, []byte(`{"status":"stopping"}`))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				cancel()
				return
			}
			nativeControlRespond(w, r, cfg.Token, http.StatusNotFound, []byte(`{"status":"not-found"}`))
			return
		}
		if !codex && !matches(r.Header.Get("Proxy-Authorization"), proxyAuth) {
			w.Header().Set("Proxy-Authenticate", `Basic realm="tairouter-native"`)
			http.Error(w, "native proxy authentication required", 407)
			return
		}
		targetHost, targetPort, e := net.SplitHostPort(r.Host)
		allowed := false
		for _, h := range cfg.Policy.Hosts() {
			if h == targetHost {
				allowed = true
			}
		}
		if e != nil || targetPort != "443" || !allowed || r.URL.Host != r.Host {
			http.Error(w, "native destination refused", 403)
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			http.Error(w, "native connection capacity reached", 503)
			return
		}
		raw, buffer, e := w.(http.Hijacker).Hijack()
		if e != nil {
			<-slots
			return
		}
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			raw.Close()
			<-slots
			return
		}
		conns[raw] = true
		wg.Add(1)
		mu.Unlock()
		go func() {
			defer wg.Done()
			defer func() { raw.Close(); mu.Lock(); delete(conns, raw); mu.Unlock(); <-slots }()
			raw.SetDeadline(time.Now().Add(15 * time.Minute))
			connCtx, connCancel := context.WithTimeout(ctx, 15*time.Minute)
			defer connCancel()
			if _, e = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
				return
			}
			if buffer.Flush() != nil {
				return
			}
			wrapped := &nativeBufferedConn{Conn: raw, reader: buffer.Reader}
			secure := tls.Server(wrapped, &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				if hello.ServerName != targetHost || !time.Now().Before(leaf.NotAfter) || ctx.Err() != nil {
					return nil, errors.New("native TLS destination mismatch")
				}
				return &cfg.CA.TLSCertificate, nil
			}})
			hs, done := context.WithTimeout(connCtx, 10*time.Second)
			e = secure.HandshakeContext(hs)
			done()
			if e != nil {
				return
			}
			reader := bufio.NewReaderSize(secure, 64<<10)
			for ctx.Err() == nil {
				req, requestReader, e := nativeReadRequestBuffered(reader)
				if e != nil {
					return
				}
				req = req.WithContext(connCtx)
				req.Close = true
				if len(req.Host) > 1024 || (req.Host != targetHost && req.Host != targetHost+":443") || req.URL.IsAbs() || req.URL.Opaque != "" {
					req.Body.Close()
					nativeWriteError(secure, 403)
					return
				}
				req.URL.Scheme = "https"
				req.URL.Host = targetHost
				if e = nativeForwardResponse(connCtx, &nativeBufferedConn{Conn: secure, reader: requestReader}, req, cfg, gateway, transport); e != nil {
					return
				}
				if req.Close {
					return
				}
			}
		}()
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			srv.Close()
			mu.Lock()
			for c := range conns {
				c.Close()
			}
			mu.Unlock()
		case <-done:
		}
	}()
	if cfg.OnReady != nil {
		cfg.OnReady()
	}
	err = srv.Serve(ln)
	cancel()
	srv.Close()
	mu.Lock()
	for c := range conns {
		c.Close()
	}
	mu.Unlock()
	wg.Wait()
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type nativeBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *nativeBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func nativeWriteError(w io.Writer, status int) {
	r := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("native compatibility request refused\n")), Close: true}
	r.Write(w)
}
func nativeForwardResponse(ctx context.Context, output io.Writer, req *http.Request, cfg NativeTLSConfig, gateway *url.URL, transport http.RoundTripper) error {
	defer req.Body.Close()
	decision, err := cfg.Policy.DecideRequest(req)
	if err != nil {
		nativeWriteError(output, 403)
		return err
	}
	// Buffer bounded requests before dispatch: an oversized body must never reach
	// either authority as a partially executed request.
	const maxBody = 16 << 20
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		nativeWriteError(output, 413)
		return errors.New("native request body exceeds limit")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A tunnel carries one request. Once its body has been consumed, any new
	// input or disconnect cancels upstream work. TLS supports concurrent read/write.
	isUpgrade := cfg.Policy.isCodex() && req.URL.Hostname() == "chatgpt.com" && req.Method == http.MethodGet && strings.EqualFold(req.Header.Get("Upgrade"), "websocket") && nativeHeaderToken(req.Header.Get("Connection"), "upgrade")
	if conn, ok := output.(net.Conn); ok && !isUpgrade {
		go func() { var b [1]byte; conn.Read(b[:]); cancel() }()
	}
	target := *req.URL
	headers := req.Header.Clone()
	if decision.Action == NativeCompatibilityGateway {
		target = *gateway
		target.Path = decision.GatewayPath
		target.RawQuery = ""
		headers = NativeCompatibilityGatewayHeaders(req.Header)
		headers.Set("Authorization", "Bearer "+cfg.Key)
	} else if decision.Action != NativeCompatibilityUpstream {
		nativeWriteError(output, 403)
		return errors.New("native action refused")
	}
	for _, v := range strings.Split(headers.Get("Connection"), ",") {
		headers.Del(strings.TrimSpace(v))
	}
	for _, h := range []string{"Connection", "Proxy-Authorization", "Proxy-Connection", "Keep-Alive", "Upgrade", "TE", "Trailer", "Transfer-Encoding", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		headers.Del(h)
	}
	if isUpgrade {
		headers.Set("Connection", "Upgrade")
		headers.Set("Upgrade", "websocket")
	} else {
		headers.Set("Accept-Encoding", "identity")
	}
	outgoing, err := http.NewRequestWithContext(ctx, req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	outgoing.Header = headers
	res, err := transport.RoundTrip(outgoing)
	if err != nil {
		nativeWriteError(output, 502)
		return errors.New("native upstream unavailable")
	}
	defer res.Body.Close()
	// RoundTrip never follows redirects. Refuse them rather than sending native
	// sessions to a new destination through the application.
	if !cfg.Policy.isCodex() && res.StatusCode >= 300 && res.StatusCode < 400 {
		nativeWriteError(output, 502)
		return errors.New("native upstream redirect refused")
	}
	if res.StatusCode == http.StatusSwitchingProtocols {
		if !isUpgrade || !strings.EqualFold(res.Header.Get("Upgrade"), "websocket") || !nativeHeaderToken(res.Header.Get("Connection"), "upgrade") {
			nativeWriteError(output, 502)
			return errors.New("unexpected native protocol upgrade")
		}
		return nativeRelayUpgrade(ctx, output, res)
	}
	res.Header.Del("Proxy-Authenticate")
	res.Header.Del("Alt-Svc")
	if decision.Action == NativeCompatibilityGateway {
		res.Header.Del("Set-Cookie")
	}
	sourceModels := cfg.Models
	if decision.ResponseKind == NativeCompatibilityResponseCLISelector || decision.ResponseKind == NativeCompatibilityResponseCLIBootstrap {
		sourceModels = cfg.CLIModels
	}
	models := make([]NativeCompatibilityModel, 0, len(sourceModels))
	for _, m := range sourceModels {
		models = append(models, NativeCompatibilityModel{ID: m.ID, Name: m.Name, Description: m.Description, ContextWindow: m.Context})
	}
	if decision.ResponseKind == NativeCompatibilityResponseUsageStream {
		// Transform incrementally through a pipe; cancellation/close releases the
		// producer and preserves backpressure without buffering a live stream.
		if res.Header.Get("Content-Encoding") == "" || res.Header.Get("Content-Encoding") == "identity" {
			nativeRewriteHeaders(res.Header)
		} else {
			res.Header.Del("Content-Length")
		}
		res.Header.Set("Cache-Control", "no-store")
		res.ContentLength = -1
		pr, pw := io.Pipe()
		original := res.Body
		go func() {
			e := cfg.Policy.RewriteUsageStream(decision, res.StatusCode, res.Header.Clone(), original, pw)
			pw.CloseWithError(e)
		}()
		res.Body = pr
		defer pr.Close()
	} else if decision.ResponseKind != NativeCompatibilityResponseUnchanged {
		b, e := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
		if e != nil || len(b) > maxBody {
			nativeWriteError(output, 502)
			return errors.New("native response exceeds rewrite limit")
		}
		b, e = cfg.Policy.RewriteResponse(decision, res.StatusCode, res.Header, b, models)
		if e != nil {
			nativeWriteError(output, 502)
			return e
		}
		res.Body = io.NopCloser(bytes.NewReader(b))
		res.ContentLength = int64(len(b))
		res.Header.Set("Content-Length", strconv.Itoa(len(b)))
	}
	res.Close = req.Close
	res.TransferEncoding = nil
	return res.Write(output)
}

// Bound the inner HTTP header before net/http allocates parsed header values.
// One request per CONNECT avoids retaining pipelined data behind this reader.
func nativeReadRequest(reader *bufio.Reader) (*http.Request, error) {
	r, _, e := nativeReadRequestBuffered(reader)
	return r, e
}
func nativeReadRequestBuffered(reader *bufio.Reader) (*http.Request, *bufio.Reader, error) {
	header := make([]byte, 0, 4096)
	for len(header) < 32<<10 {
		b, e := reader.ReadByte()
		if e != nil {
			return nil, nil, e
		}
		header = append(header, b)
		if len(header) >= 4 && bytes.Equal(header[len(header)-4:], []byte("\r\n\r\n")) {
			parsed := bufio.NewReader(io.MultiReader(bytes.NewReader(header), reader))
			r, e := http.ReadRequest(parsed)
			return r, parsed, e
		}
	}
	return nil, nil, errors.New("native request header exceeds limit")
}

func nativeHeaderToken(value, token string) bool {
	for _, v := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(v), token) {
			return true
		}
	}
	return false
}

// Relay upgraded bytes only after policy and verified TLS have fixed the origin.
// Closing either peer or the runtime closes both directions and releases the slot.
func nativeRelayUpgrade(ctx context.Context, output io.Writer, res *http.Response) error {
	client, ok := output.(net.Conn)
	if !ok {
		return errors.New("native upgraded connection unavailable")
	}
	upstream, ok := res.Body.(io.ReadWriteCloser)
	if !ok {
		return errors.New("native upstream upgrade unavailable")
	}
	defer upstream.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		return err
	}
	headers := res.Header.Clone()
	headers.Del("Proxy-Authenticate")
	headers.Del("Alt-Svc")
	if err := headers.Write(client); err != nil {
		return err
	}
	if _, err := io.WriteString(client, "\r\n"); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() { _, e := io.Copy(upstream, client); done <- e }()
	go func() { _, e := io.Copy(client, upstream); done <- e }()
	var err error
	remaining := 2
	select {
	case err = <-done:
		remaining--
	case <-ctx.Done():
		err = ctx.Err()
	}
	upstream.Close()
	client.Close()
	for i := 0; i < remaining; i++ {
		<-done
	}
	return err
}
