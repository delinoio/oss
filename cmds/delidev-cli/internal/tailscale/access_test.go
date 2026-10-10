// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"net/http"
	"testing"
)

type accessFixture struct {
	target  string
	foreign bool
	calls   [][]string
	child   *fixtureChild
}

func (f *accessFixture) Read(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if args[0] == "status" {
		return []byte(`{"BackendState":"Running","Self":{"ID":"self","HostName":"self","DNSName":"self.fixture.ts.net.","UserID":1,"Online":true},"CertDomains":["self.fixture.ts.net"],"Peer":{}}`), nil
	}
	if f.foreign {
		return []byte(`{"TCP":{"8443":{"HTTPS":true}},"Web":{"foreign.fixture.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3456"}}}}}`), nil
	}
	if f.target == "" {
		return []byte(`{}`), nil
	}
	return json.Marshal(map[string]any{"Foreground": map[string]any{"original-session": map[string]any{"TCP": map[string]any{"8443": map[string]any{"HTTPS": true}}, "Web": map[string]any{"self.fixture.ts.net:8443": map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": f.target}}}}}}})
}
func (f *accessFixture) Start(_ context.Context, id domain.ID, target string) (Child, error) {
	f.target = target
	f.child = &fixtureChild{done: make(chan struct{})}
	return f.child, nil
}

type fixtureChild struct {
	done   chan struct{}
	closed bool
}

func (f *fixtureChild) Done() <-chan struct{} { return f.done }
func (f *fixtureChild) Close() error {
	if !f.closed {
		f.closed = true
		close(f.done)
	}
	return nil
}
func TestAccessOwnsForegroundAndSupersededReceipts(t *testing.T) {
	f := &accessFixture{}
	id := domain.NewID()
	enable := domain.NewID()
	origin := "https://self.fixture.ts.net:8443"
	manager := &AccessManager{Root: t.TempDir(), ServerID: id, Parent: context.Background(), Runner: f, Launcher: f, Handler: http.NotFoundHandler()}
	initial, err := manager.Status()
	if err != nil || initial.State != AccessOff || initial.Enabled {
		t.Fatal("access enabled without consent")
	}
	value, err := manager.Set(context.Background(), enable, true, id, origin)
	if err != nil || value.State != AccessReady || f.child == nil {
		t.Fatal(value, err)
	}
	child := f.child
	if _, err = manager.Set(context.Background(), domain.NewID(), false, id, origin); err != nil || !child.closed || manager.Origin() != "" {
		t.Fatal("disable failed to join original child", err)
	}
	if _, err = manager.Set(context.Background(), enable, true, id, origin); err != nil || f.child != child {
		t.Fatal("old receipt reconstructed ingress", err)
	}
	for _, args := range f.calls {
		if args[0] != "status" && !(len(args) == 3 && args[0] == "serve" && args[1] == "status") {
			t.Fatal("global mutation performed")
		}
	}
}
func TestAccessForeignConfigurationAndIdentityFailClosed(t *testing.T) {
	for _, origin := range []string{"https://self.fixture.ts.net:8443", "https://replacement.fixture.ts.net:8443"} {
		f := &accessFixture{foreign: true}
		id := domain.NewID()
		m := &AccessManager{Root: t.TempDir(), ServerID: id, Runner: f, Launcher: f, Handler: http.NotFoundHandler()}
		if _, err := m.Set(context.Background(), domain.NewID(), true, id, origin); err == nil || f.child != nil {
			t.Fatal("foreign configuration or identity was adopted")
		}
	}
}
func TestServeOwnershipRejectsAmbiguousAndFunnelState(t *testing.T) {
	for _, raw := range []string{`{"TCP":{},"TCP":{}}`, `{"Services":{"foreign":{}}}`, `{"AllowFunnel":{"a":true}}`} {
		if emptyServeConfig([]byte(raw)) {
			t.Fatal("ambiguous state admitted")
		}
	}
	if ownedServeConfig([]byte(`{"Foreground":{"session":{}},"AllowFunnel":{"foreign":true}}`), "https://self.fixture.ts.net:8443", "http://127.0.0.1:4") {
		t.Fatal("funnel state admitted")
	}
	if uniqueJSON([]byte(`{"Peer":{"a":{},"a":{}}}`)) {
		t.Fatal("duplicate native identity admitted")
	}
}
