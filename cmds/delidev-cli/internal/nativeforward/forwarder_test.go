// SPDX-License-Identifier: Apache-2.0
package nativeforward

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const responsesPath = apiproxy.Prefix + "/responses"

func fixtureToken() string {
	return apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNativeForwardPreparationKeepsRelayLifetime(t *testing.T) {
	for _, scenario := range []struct {
		name, body                        string
		bodyDelay, proofDelay, relayDelay time.Duration
		wantAbort                         bool
	}{
		{name: "buffer and proof", body: "{}", bodyDelay: 70 * time.Millisecond, proofDelay: 50 * time.Millisecond, relayDelay: 80 * time.Millisecond},
		{name: "proof only", body: "{}", proofDelay: 70 * time.Millisecond, relayDelay: 80 * time.Millisecond},
		{name: "relay exceeds its deadline", body: "{}", proofDelay: 30 * time.Millisecond, relayDelay: 300 * time.Millisecond, wantAbort: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var sends atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				select {
				case <-time.After(scenario.proofDelay):
				case <-request.Context().Done():
					return nil, request.Context().Err()
				}
				sends.Add(1)
				reader, writer := io.Pipe()
				go func() {
					_, _ = io.WriteString(writer, "data: heartbeat\n\n")
					select {
					case <-time.After(scenario.relayDelay):
						_, _ = io.WriteString(writer, "data: complete\n\n")
						_ = writer.Close()
					case <-request.Context().Done():
						_ = writer.CloseWithError(request.Context().Err())
					}
				}()
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
			})
			f := open(t, Config{Origin: closedOrigin(t), Client: &http.Client{Transport: transport}, relayTimeout: 100 * time.Millisecond, preparationAllowance: 160 * time.Millisecond})
			var body io.Reader = strings.NewReader(scenario.body)
			if scenario.bodyDelay > 0 {
				reader, writer := io.Pipe()
				body = reader
				go func() {
					time.Sleep(scenario.bodyDelay)
					_, _ = io.WriteString(writer, scenario.body)
					_ = writer.Close()
				}()
			}
			request, err := http.NewRequest(http.MethodPost, f.NativeOrigin()+responsesPath, body)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+fixtureToken())
			response, err := (&http.Client{}).Do(request)
			if scenario.wantAbort {
				if response != nil {
					got, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if response.StatusCode == http.StatusOK && readErr == nil {
						t.Fatalf("expired relay appeared complete: %q", got)
					}
				} else if err == nil {
					t.Fatal("expired relay had neither response nor abort")
				}
				if sends.Load() != 1 {
					t.Fatal("expired relay was sent again")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusOK || sends.Load() != 1 {
				t.Fatalf("status=%d sends=%d body=%q read=%v", response.StatusCode, sends.Load(), got, readErr)
			}
			if string(got) != "data: heartbeat\n\ndata: complete\n\n" {
				t.Fatalf("incomplete relay response %q", got)
			}
		})
	}
}

// relocation models one same-server move: the immutable pairing origin A is
// retired (closed or occupied) and the authenticated runtime B is current.
type relocation struct {
	target  desktopruntime.Target
	relay   *httptest.Server
	retired string
	// rawBusiness counts non-proof requests that reached B, and wrapped counts
	// those whose bearer arrived wrapped by the desktop transport.
	rawBusiness atomic.Int32
	wrapped     atomic.Int32
	business    atomic.Int32
	next        atomic.Pointer[http.Handler]
}

func newRelocation(t *testing.T, retired string) *relocation {
	t.Helper()
	r := &relocation{retired: retired}
	r.relay = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != desktopruntime.ProofPath {
			r.rawBusiness.Add(1)
			if strings.HasPrefix(req.Header.Get("Authorization"), "Bearer runtime-v2.") {
				r.wrapped.Add(1)
			}
		}
		desktopruntime.Handler(r.target, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.business.Add(1)
			(*r.next.Load()).ServeHTTP(w, req)
		}), nil).ServeHTTP(w, req)
	}))
	t.Cleanup(r.relay.Close)
	r.target = desktopruntime.Target{Version: 2, Endpoint: r.relay.URL, Generation: domain.NewID(), ServerID: domain.NewID(), Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))}
	ok := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	r.next.Store(&ok)
	return r
}

func (r *relocation) handle(h http.HandlerFunc) {
	handler := http.Handler(h)
	r.next.Store(&handler)
}

func (r *relocation) client(resolve func() (desktopruntime.Target, error)) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	if resolve == nil {
		resolve = func() (desktopruntime.Target, error) { return r.target, nil }
	}
	return &http.Client{Transport: &desktopruntime.Transport{Base: base, Resolve: resolve}}
}

func closedOrigin(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return "http://" + address
}

func open(t *testing.T, config Config) *Forwarder {
	t.Helper()
	if config.Token == "" {
		config.Token = fixtureToken()
	}
	f, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func post(t *testing.T, f *Forwarder, path, body string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.NativeOrigin()+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+fixtureToken())
	req.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(req)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestForwardsFirstResponseToCurrentRuntimeAndStreams(t *testing.T) {
	origin := closedOrigin(t)
	r := newRelocation(t, origin)
	release := make(chan struct{})
	var seen atomic.Value
	r.handle(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		seen.Store(req.Header.Get("Authorization") + "|" + req.URL.Path + "|" + string(raw))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
	})
	f := open(t, Config{Origin: origin, Client: r.client(nil)})
	response := post(t, f, responsesPath, `{"model":"m"}`, nil)
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("X-Delidev-Correlation-Id") == "" {
		t.Fatal("unexpected response", response.StatusCode)
	}
	buffer := make([]byte, len("data: first\n\n"))
	// The first event must arrive while the upstream stream is still open.
	if _, err := io.ReadFull(response.Body, buffer); err != nil || string(buffer) != "data: first\n\n" {
		t.Fatal("first event was not flushed", err)
	}
	close(release)
	rest, _ := io.ReadAll(response.Body)
	if string(rest) != "data: second\n\n" {
		t.Fatalf("stream tail %q", rest)
	}
	if got := seen.Load(); got != "Bearer "+fixtureToken()+"|"+responsesPath+`|{"model":"m"}` {
		t.Fatalf("relay observed %v", got)
	}
	if r.wrapped.Load() != 1 || r.rawBusiness.Load() != 1 {
		t.Fatal("the bearer reached the relay unwrapped")
	}
}

func TestRetiredOriginOccupantNeverReceivesRequestOrCredential(t *testing.T) {
	var occupant atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		occupant.Add(1)
		if req.Header.Get("Authorization") != "" {
			t.Error("credential reached the retired origin")
		}
		_, _ = io.WriteString(w, "forged")
	}))
	defer foreign.Close()
	r := newRelocation(t, foreign.URL)
	f := open(t, Config{Origin: foreign.URL, Client: r.client(nil)})
	response := post(t, f, responsesPath, "{}", nil)
	response.Body.Close()
	if response.StatusCode != 200 || r.business.Load() != 1 {
		t.Fatal("the current runtime did not receive the request")
	}
	if occupant.Load() != 0 {
		t.Fatal("the retired origin received traffic")
	}
}

func TestInvalidOrMissingProofFailsClosedWithoutBearer(t *testing.T) {
	var bearer atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" || req.URL.Path != desktopruntime.ProofPath {
			bearer.Add(1)
		}
		_, _ = io.WriteString(w, "forged proof")
	}))
	defer foreign.Close()
	stale := desktopruntime.Target{Version: 2, Endpoint: foreign.URL, Generation: domain.NewID(), ServerID: domain.NewID(), Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))}
	r := newRelocation(t, foreign.URL)
	changed := r.target
	changed.Generation = domain.NewID()
	for name, resolve := range map[string]func() (desktopruntime.Target, error){
		"foreign listener":   func() (desktopruntime.Target, error) { return stale, nil },
		"changed generation": func() (desktopruntime.Target, error) { return changed, nil },
		"missing runtime": func() (desktopruntime.Target, error) {
			return desktopruntime.Target{}, domain.Fail(domain.ServerUnavailable, "gone", "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			f := open(t, Config{Origin: foreign.URL, Client: r.client(resolve), Logger: logger})
			response := post(t, f, responsesPath, "{}", nil)
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), `"code":"server_unavailable"`) {
				t.Fatalf("unclassified failure %d %s", response.StatusCode, body)
			}
			if strings.Contains(string(body), fixtureToken()) || strings.Contains(logs.String(), fixtureToken()) || strings.Contains(logs.String(), foreign.URL) || !strings.Contains(logs.String(), `"stage":"upstream"`) || !strings.Contains(logs.String(), `"correlation_id"`) {
				t.Fatalf("unsafe or unclassified diagnostics %s", logs.String())
			}
		})
	}
	if bearer.Load() != 0 || r.rawBusiness.Load() != 0 || r.business.Load() != 0 {
		t.Fatal("a business request or bearer was transmitted without proof")
	}
}

func TestRequestsAreBoundToExecutionAndRelayRoutes(t *testing.T) {
	r := newRelocation(t, closedOrigin(t))
	f := open(t, Config{Origin: r.retired, Client: r.client(nil), BodyLimit: 16})
	cases := map[string]struct {
		path   string
		body   string
		mutate func(*http.Request)
		status int
	}{
		"foreign token": {responsesPath, "{}", func(q *http.Request) {
			q.Header.Set("Authorization", "Bearer "+strings.Replace(fixtureToken(), "A", "B", 1)+"x")
		}, 401},
		"no token":      {responsesPath, "{}", func(q *http.Request) { q.Header.Del("Authorization") }, 401},
		"api key":       {responsesPath, "{}", func(q *http.Request) { q.Header.Set("X-Api-Key", fixtureToken()) }, 401},
		"other path":    {apiproxy.Prefix + "/chat/completions", "{}", nil, 404},
		"query":         {responsesPath + "?x=1", "{}", nil, 404},
		"traversal":     {apiproxy.Prefix + "/responses/../messages", "{}", nil, 404},
		"origin header": {responsesPath, "{}", func(q *http.Request) { q.Header.Set("Origin", "http://example.invalid") }, 403},
		"cookie":        {responsesPath, "{}", func(q *http.Request) { q.Header.Set("Cookie", "a=b") }, 403},
		"foreign host":  {responsesPath, "{}", func(q *http.Request) { q.Host = "example.invalid" }, 403},
		"oversize body": {responsesPath, strings.Repeat("x", 17), nil, 413},
		"method":        {responsesPath, "", func(q *http.Request) { q.Method = http.MethodGet }, 404},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			response := post(t, f, c.path, c.body, c.mutate)
			response.Body.Close()
			if response.StatusCode != c.status {
				t.Fatalf("status %d, want %d", response.StatusCode, c.status)
			}
		})
	}
	if r.business.Load() != 0 || r.rawBusiness.Load() != 0 {
		t.Fatal("a rejected request reached the relay")
	}
	response := post(t, f, apiproxy.Prefix+"/responses/compact", "{}", nil)
	response.Body.Close()
	if response.StatusCode != 200 || r.business.Load() != 1 {
		t.Fatal("compact route was not forwarded")
	}
}

func TestRedirectIsNeverFollowedAndNeverRetried(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	defer target.Close()
	r := newRelocation(t, closedOrigin(t))
	r.handle(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, target.URL, http.StatusTemporaryRedirect)
	})
	f := open(t, Config{Origin: r.retired, Client: r.client(nil)})
	response := post(t, f, responsesPath, "{}", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || followed.Load() != 0 || r.business.Load() != 1 {
		t.Fatalf("redirect handling %d followed=%d business=%d", response.StatusCode, followed.Load(), r.business.Load())
	}
	// A transport failure after the request was sent is not retried either.
	r.handle(func(w http.ResponseWriter, req *http.Request) {
		connection, _, _ := w.(http.Hijacker).Hijack()
		_ = connection.Close()
	})
	response = post(t, f, responsesPath, "{}", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || r.business.Load() != 2 {
		t.Fatalf("failed send was replayed: status %d business %d", response.StatusCode, r.business.Load())
	}
}

func TestCloseCancelsAndJoinsActiveStream(t *testing.T) {
	r := newRelocation(t, closedOrigin(t))
	started, canceled := make(chan struct{}), make(chan struct{})
	r.handle(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: open\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-req.Context().Done()
		close(canceled)
	})
	var idle atomic.Int32
	f := open(t, Config{Origin: r.retired, Client: r.client(nil), CloseIdle: func() { idle.Add(1) }})
	response := post(t, f, responsesPath, "{}", nil)
	defer response.Body.Close()
	<-started
	address := strings.TrimPrefix(f.NativeOrigin(), "http://")
	closed := make(chan error, 1)
	go func() { closed <- f.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("close did not join the stream")
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request was not canceled")
	}
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("the native stream looked like clean completion")
	}
	if connection, err := net.DialTimeout("tcp4", address, time.Second); err == nil {
		connection.Close()
		t.Fatal("listener stayed open")
	}
	if idle.Load() != 1 {
		t.Fatal("transport connections were not released after the join")
	}
	if f.Close() != nil || idle.Load() != 1 {
		t.Fatal("close is not idempotent")
	}
}

func TestExecutionCancellationClosesForwarder(t *testing.T) {
	r := newRelocation(t, closedOrigin(t))
	ctx, cancel := context.WithCancel(context.Background())
	f, err := Open(ctx, Config{Origin: r.retired, Token: fixtureToken(), Client: r.client(nil)})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp4", strings.TrimPrefix(f.NativeOrigin(), "http://"), time.Second)
		if err != nil {
			break
		}
		connection.Close()
		if time.Now().After(deadline) {
			t.Fatal("canceled execution left the listener open")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRequestsAreBoundedAndJoined(t *testing.T) {
	r := newRelocation(t, closedOrigin(t))
	hold := make(chan struct{})
	var active atomic.Int32
	r.handle(func(w http.ResponseWriter, req *http.Request) {
		active.Add(1)
		<-hold
		w.WriteHeader(200)
	})
	f := open(t, Config{Origin: r.retired, Client: r.client(nil)})
	var wait sync.WaitGroup
	for i := 0; i < MaxConcurrent; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := post(t, f, responsesPath, "{}", nil)
			response.Body.Close()
		}()
	}
	deadline := time.Now().Add(10 * time.Second)
	for active.Load() < MaxConcurrent && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	response := post(t, f, responsesPath, "{}", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d past the concurrency bound", response.StatusCode)
	}
	close(hold)
	wait.Wait()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsInvalidAuthority(t *testing.T) {
	r := newRelocation(t, closedOrigin(t))
	for name, config := range map[string]Config{
		"no client":     {Origin: r.retired, Token: fixtureToken()},
		"bad token":     {Origin: r.retired, Token: "token", Client: r.client(nil)},
		"remote http":   {Origin: "http://example.invalid", Token: fixtureToken(), Client: r.client(nil)},
		"negative body": {Origin: r.retired, Token: fixtureToken(), Client: r.client(nil), BodyLimit: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if f, err := Open(context.Background(), config); err == nil {
				_ = f.Close()
				t.Fatal("invalid configuration opened")
			}
		})
	}
	if !ValidNativeOrigin("http://127.0.0.1:1234") {
		t.Fatal("valid origin rejected")
	}
	for _, bad := range []string{"https://127.0.0.1:1234", "http://localhost:1234", "http://127.0.0.1", "http://127.0.0.1:1234/x", "http://u@127.0.0.1:1234", "http://127.0.0.1:01234", "http://127.0.0.1:1234?q=1", "http://[::1]:1234"} {
		if ValidNativeOrigin(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSelectedOpenRouterToolsAdmitOnlyFixedNativePaths(t *testing.T) {
	for _, selected := range []bool{false, true} {
		r := newRelocation(t, closedOrigin(t))
		f := open(t, Config{Origin: r.retired, Client: r.client(nil), OpenRouterTools: selected})
		for _, path := range []string{"/images/generations", "/images/edits", "/alpha/search"} {
			response := post(t, f, apiproxy.Prefix+path, "{}", nil)
			response.Body.Close()
			want := 404
			if selected {
				want = 200
			}
			if response.StatusCode != want {
				t.Fatal("tool route crossed selected profile", selected, path, response.StatusCode)
			}
		}
		for _, path := range []string{"/images", "/images/generations?extra=1", "/alpha/search/extra"} {
			response := post(t, f, apiproxy.Prefix+path, "{}", nil)
			response.Body.Close()
			if response.StatusCode != 404 {
				t.Fatal("unselected tool route admitted")
			}
		}
		want := int32(0)
		if selected {
			want = 3
		}
		if r.business.Load() != want {
			t.Fatal("unsupported route reached upstream")
		}
	}
}
