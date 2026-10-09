// SPDX-License-Identifier: Apache-2.0
package tokenprices

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const SuccessInterval = 24 * time.Hour
const FailureInterval = time.Hour
const RequestTimeout = 15 * time.Second

type State string

const (
	Unavailable State = "unavailable"
	Current     State = "current"
	Stale       State = "stale"
)

type diskCache struct {
	Version     int             `json:"version"`
	Checked     time.Time       `json:"checked"`
	LastAttempt time.Time       `json:"last_attempt"`
	Failure     bool            `json:"failure"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}
type Snapshot struct {
	State       State
	Checked     time.Time
	LastAttempt time.Time
	Failure     string
	Catalog     Catalog
}
type flight struct {
	done chan struct{}
	err  error
}
type Manager struct {
	life    context.Context
	stop    context.CancelFunc
	mu      sync.Mutex
	state   Snapshot
	raw     []byte
	path    string
	client  *http.Client
	logger  *slog.Logger
	now     func() time.Time
	active  *flight
	wake    chan struct{}
	publish func(context.Context, Snapshot) error
}

func New(root string, transport http.RoundTripper, logger *slog.Logger, publish func(context.Context, Snapshot) error) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	life, stop := context.WithCancel(context.Background())
	m := &Manager{life: life, stop: stop, path: filepath.Join(root, "models-dev-token-prices.json"), logger: logger, now: time.Now, wake: make(chan struct{}, 1), publish: publish, state: Snapshot{State: Unavailable}}
	m.client = &http.Client{Transport: transport, Timeout: RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	raw, e := security.ReadPrivate(m.path, MaxBytes+4096)
	if e == nil {
		var c diskCache
		if domain.DecodeBounded(raw, &c, MaxBytes+4096) == nil && c.Version == 1 && !c.Checked.After(m.now()) && !c.LastAttempt.Before(c.Checked) && !c.LastAttempt.After(m.now()) {
			if c.Checked.IsZero() && len(c.Raw) == 0 && c.Failure && !c.LastAttempt.IsZero() {
				m.state.LastAttempt = c.LastAttempt
				m.state.Failure = "refresh_failed"
				return m
			}
			catalog, e := Decode(c.Raw, c.Checked)
			if e == nil {
				state := Current
				failure := ""
				if c.Failure {
					state = Stale
					failure = "refresh_failed"
				}
				m.raw = append([]byte(nil), c.Raw...)
				m.state = Snapshot{State: state, Checked: c.Checked, LastAttempt: c.LastAttempt, Failure: failure, Catalog: catalog}
				return m
			}
		}
		m.logger.Warn("token_price_cache_rejected", "code", "invalid_cache")
	} else if !errors.Is(e, os.ErrNotExist) {
		m.logger.Warn("token_price_cache_unavailable", "code", "cache_read_failed")
	}
	return m
}

// Snapshot returns independent metadata so callers cannot alter the manager's
// validated cache or a concurrent refresh's publication input.
func (m *Manager) Snapshot() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return clone(m.state) }
func clone(s Snapshot) Snapshot {
	c := Catalog{Digest: s.Catalog.Digest, References: map[string]Reference{}}
	for k, r := range s.Catalog.References {
		r.Costs = append(json.RawMessage(nil), r.Costs...)
		if r.Basis != nil {
			b := *r.Basis
			b.Exclusions = append([]string(nil), b.Exclusions...)
			copyRate := func(v *string) *string {
				if v == nil {
					return nil
				}
				x := *v
				return &x
			}
			b.InputPerMillion = copyRate(b.InputPerMillion)
			b.CachedInputPerMillion = copyRate(b.CachedInputPerMillion)
			b.OutputPerMillion = copyRate(b.OutputPerMillion)
			r.Basis = &b
		}
		c.References[k] = r
	}
	s.Catalog = c
	return s
}
func (m *Manager) Refresh(ctx context.Context) error {
	if e := m.life.Err(); e != nil {
		return e
	}
	m.mu.Lock()
	if e := m.life.Err(); e != nil {
		m.mu.Unlock()
		return e
	}
	if f := m.active; f != nil {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.done:
			return f.err
		}
	}
	if m.state.Failure != "" && m.now().Before(m.state.LastAttempt.Add(FailureInterval)) {
		m.mu.Unlock()
		return errors.New("refresh retry is not due")
	}
	f := &flight{done: make(chan struct{})}
	m.active = f
	m.mu.Unlock()
	start := m.now()
	err := m.refresh(ctx)
	m.mu.Lock()
	if err != nil {
		m.state.LastAttempt = m.now().UTC()
		m.state.Failure = "refresh_failed"
		if !m.state.Checked.IsZero() {
			m.state.State = Stale
		}
	}
	if err != nil {
		cached, _ := json.Marshal(diskCache{Version: 1, Checked: m.state.Checked, LastAttempt: m.state.LastAttempt, Failure: true, Raw: m.raw})
		if e := security.WriteAtomic(m.path, cached); e != nil {
			m.logger.Warn("token_price_cache_write_failed", "code", "cache_write_failed")
		}
	}
	f.err = err
	m.active = nil
	close(f.done)
	m.mu.Unlock()
	if err != nil {
		m.logger.Warn("token_price_refresh_failed", "phase", "refresh", "duration_ms", m.now().Sub(start).Milliseconds(), "code", "refresh_failed")
	} else {
		snapshot := m.Snapshot()
		m.logger.Info("token_price_refresh_completed", "phase", "publish", "duration_ms", m.now().Sub(start).Milliseconds(), "models", len(snapshot.Catalog.References))
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return err
}
func (m *Manager) refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	unbind := context.AfterFunc(m.life, cancel)
	defer unbind()
	if m.client.Transport == nil {
		return errors.New("explicit outbound transport required")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	response, e := m.client.Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > MaxBytes {
		return ErrInvalid
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, MaxBytes+1))
	if e != nil {
		return e
	}
	checked := m.now().UTC()
	catalog, e := Decode(raw, checked)
	if e != nil {
		return e
	}
	next := Snapshot{State: Current, Checked: checked, LastAttempt: checked, Catalog: catalog}
	cached, e := json.Marshal(diskCache{Version: 1, Checked: checked, LastAttempt: checked, Raw: raw})
	if e != nil || len(cached) > MaxBytes+4096 {
		return ErrInvalid
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = security.WriteAtomic(m.path, cached); e != nil {
		return e
	}
	if m.publish != nil {
		if e = m.publish(ctx, clone(next)); e != nil {
			return e
		}
	}
	m.mu.Lock()
	m.raw = append([]byte(nil), raw...)
	m.state = next
	m.mu.Unlock()
	return nil
}

// Run is joined by the server owner. Explicit refresh wakes scheduling without
// launching another fetch; failure backoff applies to explicit refresh too.
func (m *Manager) Run(ctx context.Context) {
	for {
		snapshot := m.Snapshot()
		due := snapshot.Checked.Add(SuccessInterval)
		if snapshot.Failure != "" {
			due = snapshot.LastAttempt.Add(FailureInterval)
		}
		delay := due.Sub(m.now())
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			m.Close()
			return
		case <-m.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}
		_ = m.Refresh(ctx)
	}
}

// Close cancels and joins both scheduled and explicitly requested refresh work.
func (m *Manager) Close() {
	m.stop()
	m.mu.Lock()
	f := m.active
	m.mu.Unlock()
	if f != nil {
		<-f.done
	}
}
