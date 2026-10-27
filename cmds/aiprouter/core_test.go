// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	values      map[string]string
	failProfile bool
	deletes     []string
}

func (s *memoryStore) Set(k, v string) error {
	if s.failProfile && strings.HasPrefix(k, "profile/") {
		return errors.New("SECRET storage error")
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[k] = v
	return nil
}
func (s *memoryStore) Get(k string) (string, error) {
	v, ok := s.values[k]
	if !ok {
		return "", ErrUnavailable
	}
	return v, nil
}
func (s *memoryStore) Delete(k string) error {
	delete(s.values, k)
	s.deletes = append(s.deletes, k)
	return nil
}

type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Wait(ctx context.Context, d time.Duration) error {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	return ctx.Err()
}

type mockAPI struct {
	clock              *fakeClock
	polls              int
	revoked, confirmed bool
	revokeErr          error
	store              *memoryStore
}

func (a *mockAPI) Begin(context.Context, string) (Device, error) {
	return Device{"id", "DEVICE_SECRET", "CODE", "https://router.test/device", a.clock.Now().Add(10 * time.Minute)}, nil
}
func (a *mockAPI) Poll(context.Context, Device) (Grant, error) {
	a.polls++
	if a.polls == 1 {
		return Grant{Status: DevicePending}, nil
	}
	return Grant{DeviceApproved, "key-id", "REMOTE_SECRET", "org"}, nil
}
func (a *mockAPI) Confirm(context.Context, Device) error {
	if a.store.values[credentialID(Profile{Name: "test", Server: "https://router.test", KeyID: "key-id"})] != "REMOTE_SECRET" {
		return errors.New("not stored")
	}
	a.confirmed = true
	return nil
}
func (a *mockAPI) Revoke(context.Context, string, string) error { a.revoked = true; return a.revokeErr }
func (a *mockAPI) Models(context.Context, string) ([]ClientModel, error) {
	return []ClientModel{{ID: "vendor/model", Name: "Model", Context: 128000, Output: 8192, Tools: true}}, nil
}
func testProfile() Profile {
	return Profile{Name: "test", Server: "https://router.test", Port: DefaultPort}
}
func TestDeviceStoresBeforeAcknowledgement(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	store := &memoryStore{}
	api := &mockAPI{clock: clock, store: store}
	var out bytes.Buffer
	p, e := (Connector{api, store, clock, &out, nil}).Connect(context.Background(), testProfile(), "laptop")
	if e != nil || p.KeyID != "key-id" || !api.confirmed || api.revoked {
		t.Fatalf("handoff failed: %v", e)
	}
	for _, d := range clock.waits {
		if d != PollInterval {
			t.Fatal("wrong polling interval")
		}
	}
	if strings.Contains(out.String(), "SECRET") {
		t.Fatal("secret leaked")
	}
}
func TestDeviceStoreFailureCompensates(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	store := &memoryStore{failProfile: true}
	api := &mockAPI{clock: clock, store: store, revokeErr: errors.New("offline")}
	_, e := (Connector{api, store, clock, io.Discard, nil}).Connect(context.Background(), testProfile(), "laptop")
	if e == nil || !api.revoked || api.confirmed || !strings.Contains(e.Error(), "Revoke CLI key key-id") || strings.Contains(e.Error(), "SECRET") {
		t.Fatalf("unsafe failure: %v", e)
	}
}

type answer bool

func (a answer) Confirm(string) bool { return bool(a) }
func testDisk(t *testing.T) Disk {
	t.Helper()
	return Disk{Root: filepath.Join(realTemp(t), "state"), Protection: NativeProtector{}}
}
func TestSettingsPreviewConsentAndRestore(t *testing.T) {
	d := testDisk(t)
	s, e := d.Load()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(realTemp(t), "settings.json")
	original := []byte("{\n // keep me\n \"secret\": \"UNRELATED_SECRET\"\n}\n")
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	after, e := Patch(original, JSON5, []Fragment{{[]string{"providers", "aiprouter"}, map[string]any{"baseUrl": "http://127.0.0.1:17843"}}}, false)
	if e != nil {
		t.Fatal(e)
	}
	c := Change{Path: path, Before: original, After: after, Existed: true, Fields: []string{"providers.aiprouter"}}
	var out bytes.Buffer
	Preview(&out, []Change{c})
	if strings.Contains(out.String(), "UNRELATED_SECRET") {
		t.Fatal("preview leaked source")
	}
	if d.Apply(&s, "pi", "test", []Change{c}, answer(false)) == nil {
		t.Fatal("applied without consent")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(current, original) {
		t.Fatal("decline modified file")
	}
	if e = d.Apply(&s, "pi", "test", []Change{c}, answer(true)); e != nil {
		t.Fatal(e)
	}
	edited := bytes.Replace(after, []byte("keep me"), []byte("user edit"), 1)
	_ = os.WriteFile(path, edited, 0600)
	if e = d.Restore(&s, "pi"); e != nil {
		t.Fatal(e)
	}
	current, _ = os.ReadFile(path)
	if !bytes.Equal(current, bytes.Replace(original, []byte("keep me"), []byte("user edit"), 1)) {
		t.Fatalf("unrelated edit lost: %s", current)
	}
	if len(s.Records) != 0 {
		t.Fatal("backup not deleted")
	}
}
func TestRestoreConflictPreservesBackup(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	path := filepath.Join(realTemp(t), "c.json")
	before := []byte("{}\n")
	after := []byte("{\"owned\":1}\n")
	_ = os.WriteFile(path, before, 0600)
	if e := d.Apply(&s, "pi", "test", []Change{{Path: path, Before: before, After: after, Existed: true}}, answer(true)); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(path, []byte("{\"owned\":2}\n"), 0600)
	if !errors.Is(d.Restore(&s, "pi"), ErrConflict) || len(s.Records) != 1 {
		t.Fatal("conflict was overwritten")
	}
}
func TestSourcePreservingFormats(t *testing.T) {
	for _, tc := range []struct {
		format Format
		before string
	}{{JSON5, "{ // user comment\n unquoted: 'keep',\n}\n"}, {YAML, "# user comment\nunrelated: keep\nproviders:\n  other:\n    api: keep\n"}, {TOML, "# user comment\nunrelated = 'keep'\n"}} {
		t.Run(string(tc.format), func(t *testing.T) {
			b, e := Patch([]byte(tc.before), tc.format, []Fragment{{[]string{"providers", "aiprouter"}, map[string]any{"base_url": "https://router.test"}}}, false)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Contains(b, []byte("user comment")) || !bytes.Contains(b, []byte("keep")) {
				t.Fatal("source lost")
			}
			if _, e = Patch(b, tc.format, []Fragment{{[]string{"providers", "aiprouter"}, map[string]any{"base_url": "new"}}}, false); e == nil {
				t.Fatal("unowned contribution overwritten")
			}
		})
	}
}
func TestSourceRejectsDuplicateJSONKeys(t *testing.T) {
	_, e := Patch([]byte(`{"providers":{},"providers":{}}`), JSON, []Fragment{{[]string{"providers", "aiprouter"}, true}}, false)
	if e == nil {
		t.Fatal("duplicate key accepted")
	}
}
func TestSymlinkRefused(t *testing.T) {
	root := realTemp(t)
	target := filepath.Join(root, "real")
	_ = os.WriteFile(target, []byte("{}"), 0600)
	link := filepath.Join(root, "link")
	if e := os.Symlink(target, link); e != nil {
		t.Skip(e)
	}
	if _, _, e := readConfig(link); e == nil {
		t.Fatal("symlink read")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRelayBoundaryAndCredentialReplacement(t *testing.T) {
	store := &memoryStore{values: map[string]string{credentialID(testProfile()): "REMOTE_SECRET"}}
	calls := 0
	transport := roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "router.test" || r.Header.Get("Authorization") != "Bearer REMOTE_SECRET" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Api-Key") != "" {
			t.Fatal("unsafe relay headers")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: test\n\n"))}, nil
	})
	h, e := RelayHandler(testProfile(), store, transport)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/v1/responses", "/aiprouter.v1.ClientService/RevokeCLIKey", "/v1/../management"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:17843"+path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer LOCAL_SECRET")
		r.Header.Set("X-Api-Key", "LOCAL_SECRET")
		r.Header.Set("Cookie", "browser_secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if path == "/v1/responses" && w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if calls != 1 {
		t.Fatal("management route forwarded")
	}
	r := httptest.NewRequest("POST", "http://attacker.test/v1/responses", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding allowed")
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestFixedPortConflictDoesNotChooseAnotherPort(t *testing.T) {
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	p := testProfile()
	p.Port = listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e = RunRelay(ctx, p, &memoryStore{})
	if e == nil || !strings.Contains(e.Error(), "fixed loopback port") {
		t.Fatal("port conflict did not fail clearly")
	}
}
func TestCredentialIdentitySeparatesServerAndIssuedKey(t *testing.T) {
	p := testProfile()
	p.KeyID = "key-one"
	other := p
	other.KeyID = "key-two"
	if credentialID(p) == credentialID(other) {
		t.Fatal("keys collide")
	}
	other = p
	other.Server = "https://other.test"
	if credentialID(p) == credentialID(other) {
		t.Fatal("servers collide")
	}
}
