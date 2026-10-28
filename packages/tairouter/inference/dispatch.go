// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Dispatcher serializes the first HTTP request write with revocation/deletion.
// start writes only the first bounded chunk, never waits for response headers, and
// is called exactly once synchronously. A failure after start is non-replayable.
// Production Authorizers must implement this interface with authoritative locking.
type Dispatcher interface {
	Dispatch(context.Context, Principal, func(context.Context) error) error
}

// gateClient runs DNS, TCP and TLS before taking the application dispatch gate.
// HTTP/1.1 is deliberate: a shared HTTP/2 connection would require per-stream gates,
// not a connection-level first-write gate. Enable H2 only with that implementation.
func gateClient(client *http.Client, ctx context.Context, p Principal, a Authorizer, onSend func()) *atomic.Bool {
	sent := &atomic.Bool{}
	base := client.Transport.(*http.Transport).Clone()
	dial := base.DialContext
	wrap := func(c net.Conn) net.Conn {
		return &gatedConn{Conn: withSupportIdle(ctx, c), ctx: ctx, sent: sent, start: onSend, gate: func(start func(context.Context) error) error {
			if d, ok := a.(Dispatcher); ok {
				return d.Dispatch(ctx, p, start)
			}
			if e := a.BeforeSend(ctx, p); e != nil {
				return e
			}
			return start(ctx)
		}}
	}
	base.ForceAttemptHTTP2 = false
	base.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	base.DialContext = func(c context.Context, n, addr string) (net.Conn, error) {
		connectionCtx, stop := supportConnectContext(c)
		defer stop()
		conn, e := dial(connectionCtx, n, addr)
		if e != nil {
			return nil, e
		}
		return wrap(conn), nil
	}
	base.DialTLSContext = func(c context.Context, n, addr string) (net.Conn, error) {
		connectionCtx, stop := supportConnectContext(c)
		defer stop()
		conn, e := dial(connectionCtx, n, addr)
		if e != nil {
			return nil, e
		}
		host, _, e := net.SplitHostPort(addr)
		if e != nil {
			conn.Close()
			return nil, e
		}
		cfg := base.TLSClientConfig.Clone()
		cfg.ServerName = host
		cfg.NextProtos = []string{"http/1.1"}
		secure := tls.Client(conn, cfg)
		handshake, cancel := context.WithTimeout(connectionCtx, 30*time.Second)
		defer cancel()
		if e = secure.HandshakeContext(handshake); e != nil {
			conn.Close()
			return nil, e
		}
		return wrap(secure), nil
	}
	client.Transport = base
	return sent
}

type gatedConn struct {
	net.Conn
	ctx   context.Context
	once  sync.Once
	sent  *atomic.Bool
	gate  func(func(context.Context) error) error
	start func()
	err   error
}

func (c *gatedConn) Write(b []byte) (int, error) {
	first := false
	n := 0
	c.once.Do(func() {
		first = true
		c.err = c.gate(func(ctx context.Context) error {
			if e := validateNegotiationExpiry(c.ctx); e != nil {
				return e
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			if e := c.ctx.Err(); e != nil {
				return e
			}
			limit := len(b)
			if limit > 16<<10 {
				limit = 16 << 10
			}
			deadline := time.Now().Add(5 * time.Second)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			_ = c.Conn.SetWriteDeadline(deadline)
			defer c.Conn.SetWriteDeadline(time.Time{})
			markModelDispatch(ctx)
			c.sent.Store(true)
			c.start()
			var e error
			n, e = c.Conn.Write(b[:limit])
			return e
		})
	})
	if c.err != nil {
		return n, c.err
	}
	if !first {
		return c.Conn.Write(b)
	}
	if n < len(b) {
		rest, e := c.Conn.Write(b[n:])
		return n + rest, e
	}
	return n, nil
}
