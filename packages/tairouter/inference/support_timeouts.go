// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"net"
	"sync"
	"time"
)

// SupportTimeouts adds phase deadlines without replacing the parent's total deadline.
// Connect covers DNS/TCP/targetTLS; Idle covers first headers and subsequent reads.
// Nonpositive values leave that phase's existing defaults in place.
type SupportTimeouts struct{ Connect, Idle time.Duration }
type supportTimeoutKey struct{}

func WithSupportTimeouts(ctx context.Context, timeouts SupportTimeouts) context.Context {
	return context.WithValue(ctx, supportTimeoutKey{}, timeouts)
}
func supportConnectContext(ctx context.Context) (context.Context, context.CancelFunc) {
	options, _ := ctx.Value(supportTimeoutKey{}).(SupportTimeouts)
	if options.Connect > 0 {
		return context.WithTimeout(ctx, options.Connect)
	}
	return ctx, func() {}
}

type supportIdleConn struct {
	net.Conn
	idle      time.Duration
	firstByte *supportFirstByte
}

func (c *supportIdleConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if n > 0 {
		c.Conn.SetReadDeadline(time.Now().Add(c.idle))
		if c.firstByte != nil {
			c.firstByte.progress(c.idle)
		}
	}
	return n, e
}
func withSupportIdle(ctx context.Context, c net.Conn) net.Conn {
	options, _ := ctx.Value(supportTimeoutKey{}).(SupportTimeouts)
	if options.Idle <= 0 {
		return c
	}
	c.SetReadDeadline(time.Now().Add(options.Idle))
	timer, _ := ctx.Value(supportFirstByteKey{}).(*supportFirstByte)
	return &supportIdleConn{Conn: c, idle: options.Idle, firstByte: timer}
}

type supportFirstByteKey struct{}

func supportFirstByteContext(ctx context.Context) (context.Context, func(), context.CancelFunc) {
	options, _ := ctx.Value(supportTimeoutKey{}).(SupportTimeouts)
	child, cancel := context.WithCancel(ctx)
	if options.Idle <= 0 {
		return child, func() {}, cancel
	}
	// Pinned legacy idle starts before connect, independently of connect timeout.
	state := &supportFirstByte{active: true}
	state.timer = time.AfterFunc(options.Idle, cancel)
	child = context.WithValue(child, supportFirstByteKey{}, state)
	return child, state.stop, cancel
}

type supportFirstByte struct {
	mu     sync.Mutex
	timer  *time.Timer
	active bool
}

func (s *supportFirstByte) progress(idle time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.timer.Reset(idle)
	}
}
func (s *supportFirstByte) stop() { s.mu.Lock(); defer s.mu.Unlock(); s.active = false; s.timer.Stop() }
