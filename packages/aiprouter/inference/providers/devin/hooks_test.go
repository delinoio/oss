// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type executorFunc func(context.Context, inference.AdapterRequest) (*http.Response, error)

func (f executorFunc) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	return f(ctx, r)
}

type stateFixture struct {
	data    map[string][]byte
	ttl     time.Duration
	failure error
}

func (s *stateFixture) Load(_ context.Context, id string) ([]byte, error) {
	if s.failure != nil {
		return nil, s.failure
	}
	return s.data[id], nil
}
func (s *stateFixture) Save(_ context.Context, id string, b []byte, ttl time.Duration) error {
	if s.failure != nil {
		return s.failure
	}
	s.ttl = ttl
	s.data[id] = bytes.Clone(b)
	return nil
}
func unary(b []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))}
}

// Synthetic protobuf mirrors pinned catalog/JWT unary exchanges. The executor
// fixture is the only send boundary; no test contacts a provider account.
func TestGenericPreflightAndStateLifecycle(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"namespace","name":"sandbox","tools":[{"type":"custom","name":"exec"}]}]}`)
	in.Principal = inference.Principal{OrganizationID: "org", UserID: "user", MembershipGeneration: 1}
	in.Credential.AccountID = "account"
	store := &stateFixture{data: map[string][]byte{}}
	ctx, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	jwt := "eyJabcdefghijk.payload.signature"
	calls := 0
	x := executorFunc(func(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 31*time.Second {
			t.Fatal("unbounded preflight")
		}
		if r.Method != "POST" || r.Headers.Get("Content-Type") != "application/proto" {
			t.Fatal(r)
		}
		meta := fields(t, fields(t, r.Body)[1][0].b)
		if string(meta[3][0].b) != "fixture-key" {
			t.Fatal("credential lost")
		}
		switch calls {
		case 1:
			if !strings.HasSuffix(r.URL, "/GetUserJwt") {
				t.Fatal(r.URL)
			}
			return unary(textField(nil, 1, jwt)), nil
		case 2:
			if !strings.HasSuffix(r.URL, "/GetCascadeModelConfigs") || string(meta[21][0].b) != jwt {
				t.Fatal("JWT catalog binding")
			}
			row := blob(textField(nil, 22, "swe-2-high"), 23, number(nil, 13, 65536))
			return unary(blob(nil, 1, row)), nil
		default:
			t.Fatal("hidden repeat")
			return nil, nil
		}
	})
	for i := 0; i < 2; i++ {
		body, err := a.Prepare(ctx, in, x)
		if err != nil || !bytes.Equal(body, in.Body) {
			t.Fatal(err)
		}
	}
	req, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	_, proto, err := readFrame(bytes.NewReader(req.Body))
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, proto)
	if fields(t, f[8][0].b)[2][0].n != 65536 || calls != 2 {
		t.Fatal("catalog cap not applied")
	}
	cascade := string(f[16][0].b)
	trajectory := string(f[15][0].b)
	wire := append(frame(blob(nil, 6, toolCall("call", "exec", `{"input":"hello"}`))), eos(`{}`)...)
	resp, err := a.DecodeWithInput(ctx, in, unary(wire))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"tool_namespace":"sandbox"`)) || !bytes.Contains(out, []byte(`"tool_kind":"custom"`)) || !bytes.Contains(out, []byte("sandbox__exec")) {
		t.Fatal(string(out))
	}
	if err := a.CaptureState(ctx, in, "resp_public", store); err != nil {
		t.Fatal(err)
	}
	if store.ttl != StateTTL || bytes.Contains(store.data["resp_public"], []byte(jwt)) || bytes.Contains(store.data["resp_public"], []byte("fixture-key")) {
		t.Fatal("state content or TTL")
	}
	in.PreviousResponseID = "resp_public"
	next, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	req, err = a.Build(next, in)
	if err != nil {
		t.Fatal(err)
	}
	_, proto, _ = readFrame(bytes.NewReader(req.Body))
	f = fields(t, proto)
	if string(f[16][0].b) != cascade || string(f[15][0].b) == trajectory {
		t.Fatal("cascade continuity or unsafe trajectory reuse")
	}
	if err = a.CaptureState(next, in, "unfinished", store); !errors.Is(err, inference.ErrContinuation) {
		t.Fatal("unfinished state captured", err)
	}
	store.failure = errors.New("deletion fence")
	if err = a.CaptureState(ctx, in, "deleted", store); !errors.Is(err, store.failure) {
		t.Fatal("save fence ignored", err)
	}
}

func TestPreflightFailureAccounting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      []byte
		status    int
		failure   error
		wantCalls int
	}{
		{"budget", nil, 0, errors.New("budget exhausted"), 1},
		{"auth", nil, 0, inference.ErrDestination, 1},
		{"unavailable", nil, 503, nil, 1},
		{"invalid JWT", []byte("invalid"), 200, nil, 1},
		{"invalid catalog", textField(nil, 1, "eyJabcdefghijk.payload.signature"), 200, nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
			ctx := a.PrepareContext(context.Background())
			calls := 0
			x := executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
				calls++
				if tc.failure != nil {
					return nil, tc.failure
				}
				r := unary(tc.body)
				r.StatusCode = tc.status
				if calls == 2 {
					r = unary([]byte{10, 128})
				}
				return r, nil
			})
			for i := 0; i < 2; i++ {
				_, err := a.Prepare(ctx, in, x)
				if !errors.Is(err, tc.failure) {
					t.Fatal(err)
				}
			}
			if calls != tc.wantCalls {
				t.Fatal(calls)
			}
		})
	}
	a := New()
	ctx, cancel := context.WithCancel(a.PrepareContext(context.Background()))
	cancel()
	_, err := a.Prepare(ctx, input(`{}`), executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		t.Fatal("send after cancellation")
		return nil, nil
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStateScopeAndCorruption(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	store := &stateFixture{data: map[string][]byte{}}
	ctx, err := a.RestoreState(context.Background(), in, store)
	if err != nil {
		t.Fatal(err)
	}
	markComplete(ctx)
	if err = a.CaptureState(ctx, in, "response", store); err != nil {
		t.Fatal(err)
	}
	in.PreviousResponseID = "response"
	for _, mutate := range []func(*inference.AdapterInput){func(v *inference.AdapterInput) { v.Principal.UserID = "other" }, func(v *inference.AdapterInput) { v.Credential.AccountID = "other" }, func(v *inference.AdapterInput) { v.Credential.Secret = "other" }, func(v *inference.AdapterInput) { v.Principal.MembershipGeneration++ }} {
		other := in
		mutate(&other)
		if _, err = a.RestoreState(context.Background(), other, store); !errors.Is(err, inference.ErrContinuation) {
			t.Fatal("cross scope restore", err)
		}
		if _, err = a.Build(ctx, other); !errors.Is(err, inference.ErrContinuation) {
			t.Fatal("cross scope build", err)
		}
	}
	good := bytes.Clone(store.data["response"])
	var saved savedState
	if err = json.Unmarshal(good, &saved); err != nil {
		t.Fatal(err)
	}
	saved.ExpiresAt = time.Now().Add(-time.Second)
	expired, _ := json.Marshal(saved)
	for _, bad := range [][]byte{nil, []byte(`{}`), expired, bytes.Repeat([]byte("x"), StateMaxEntryBytes+1)} {
		store.data["response"] = bad
		if _, err = a.RestoreState(context.Background(), in, store); !errors.Is(err, inference.ErrContinuation) {
			t.Fatal("bad state accepted", err)
		}
	}
}

func TestFailedCustomStreamCannotCapture(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","name":"exec"}]}`)
	for _, wire := range [][]byte{frame(textField(nil, 3, "truncated")), append(frame(blob(nil, 6, toolCall("c", "exec", `{"input":42}`))), eos(`{}`)...), eos(`{"error":{"code":"permission_denied"}}`)} {
		ctx, err := a.RestoreState(context.Background(), in, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Build(ctx, in); err != nil {
			t.Fatal(err)
		}
		r, err := a.DecodeWithInput(ctx, in, unary(wire))
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err == nil || bytes.Contains(out, []byte("[DONE]")) {
			t.Fatal("failure became success")
		}
		if err = a.CaptureState(ctx, in, "resp", &stateFixture{data: map[string][]byte{}}); !errors.Is(err, inference.ErrContinuation) {
			t.Fatal(err)
		}
	}
}

func TestRawIdleAndRecovery(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	body := newStreamIdle(context.Background(), r, 5*time.Millisecond)
	defer body.Close()
	_, err := io.ReadAll(body)
	var typed *inference.Error
	if !errors.As(err, &typed) || typed.Code != "devin_stream_idle_timeout" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status int
		oauth  bool
		action inference.RecoveryAction
	}{{401, true, inference.RecoveryRotate}, {401, false, inference.RecoveryStop}, {403, true, inference.RecoveryStop}, {429, true, inference.RecoveryDefault}, {503, true, inference.RecoveryDefault}} {
		in := input(`{}`)
		if tc.oauth {
			in.Target.Auth = inference.OAuth
		}
		d, err := (RecoveryPolicy{}).RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: tc.status})
		if err != nil || d.Action != tc.action {
			t.Fatal(d, err)
		}
	}
}

func TestConcurrentPrepareAndBoundedBody(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	ctx := a.PrepareContext(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 2)
	x := executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		close(entered)
		<-release
		return unary([]byte("invalid JWT")), nil
	})
	go func() { _, err := a.Prepare(ctx, in, x); finished <- err }()
	<-entered
	go func() { _, err := a.Prepare(ctx, in, x); finished <- err }()
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	r, w := io.Pipe()
	defer w.Close()
	_, ok, err := preflight(context.Background(), executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: r}, nil
	}), "https://server.codeium.com", nil, 5*time.Millisecond, 64)
	if err != nil || ok {
		t.Fatal("timeout did not degrade catalog", err)
	}
	_, ok, err = preflight(context.Background(), executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		return unary(bytes.Repeat([]byte("x"), 65)), nil
	}), "https://server.codeium.com", nil, time.Second, 64)
	if err != nil || ok {
		t.Fatal("oversize catalog accepted", err)
	}
}

func TestNamespacedFunctionMetadata(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"namespace","name":"mcp__cua","tools":[{"type":"function","name":"js"}]}]}`)
	ctx := a.PrepareContext(context.Background())
	if _, err := a.Build(ctx, in); err != nil {
		t.Fatal(err)
	}
	wire := append(frame(blob(nil, 6, toolCall("c", "js", "{}"))), eos(`{}`)...)
	r, err := a.DecodeWithInput(ctx, in, unary(wire))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil || !bytes.Contains(b, []byte(`"tool_namespace":"mcp__cua"`)) || !bytes.Contains(b, []byte(`"tool_kind":"function"`)) {
		t.Fatal(string(b), err)
	}
}

func TestRequiredToolCannotFinishWithoutCall(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read"}}],"tool_choice":"required"}`)
	ctx := a.PrepareContext(context.Background())
	if _, err := a.Build(ctx, in); err != nil {
		t.Fatal(err)
	}
	r, err := a.DecodeWithInput(ctx, in, unary(append(frame(textField(nil, 3, "no tool")), eos(`{}`)...)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	var typed *inference.Error
	if !errors.As(err, &typed) || typed.Code != "devin_required_tool_missing" || bytes.Contains(b, []byte("[DONE]")) {
		t.Fatal(string(b), err)
	}
}
