// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/hosted"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/google"
	"github.com/delinoio/oss/packages/tairouter/providers"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const validationEnvelope = `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Account validation is required.","details":[{"reason":"VALIDATION_REQUIRED"}]}}`

func TestHostedValidationEnvelopeIsComplete(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"structured", validationEnvelope, true},
		{"exact-boundary", validationEnvelope + strings.Repeat(" ", 4096-len(validationEnvelope)), true},
		{"over-limit", validationEnvelope + strings.Repeat(" ", 4097-len(validationEnvelope)), false},
		{"phrase-only", `{"error":{"message":"VALIDATION_REQUIRED: account validation is required"}}`, false},
		{"ordinary", `{"error":{"status":"PERMISSION_DENIED"}}`, false},
		{"truncated", validationEnvelope[:len(validationEnvelope)-1], false},
		{"valid-prefix-truncated", validationEnvelope + `{"error":`, false},
		{"malformed", `{not-json}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := completeHostedValidation(io.NopCloser(strings.NewReader(tc.body))); got != tc.want {
				t.Fatalf("qualified=%v", got)
			}
		})
	}
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-byte timeout", true: "incomplete idle timeout"}[first], func(t *testing.T) {
			reader, writer := io.Pipe()
			defer writer.Close()
			if first {
				go func() { _, _ = writer.Write([]byte(validationEnvelope[:40])) }()
			}
			start := time.Now()
			if completeHostedValidation(reader) || time.Since(start) < 1800*time.Millisecond {
				t.Fatal("slow envelope gained recovery")
			}
		})
	}
}

func hostedValidationFixture(t *testing.T, mode string) {
	a, sc, key := dispatchFixture(t)
	model := "gemini-3.8-flash"
	initialData := map[string]providerData{}
	for _, id := range []string{"a", "b", "c"} {
		d := providerData{Metadata: &v1.ProviderConnection{Id: id, OrganizationId: sc.OrganizationID, ProviderId: "google-antigravity", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Credential: &providers.Credential{Scope: sc, Provider: "google-antigravity", AccessToken: "token-" + id, ProjectID: "project-" + id}, Models: &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: model}}}, ModelsDiscoveredAt: time.Now()}
		putRoutingProvider(t, a, sc, d)
		initialData[id] = d
	}
	pool := &v1.Pool{Id: "search", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: []*v1.PoolMember{{ConnectionId: "a", Priority: 0, Weight: 1}, {ConnectionId: "b", Priority: 1, Weight: 1}}}
	if mode == "repeated refusal" {
		pool.Members = append(pool.Members, &v1.PoolMember{ConnectionId: "c", Priority: 2, Weight: 1})
	}
	poolRaw, _ := json.Marshal(pool)
	if err := a.s.PutPrivate(context.Background(), sc, string(poolKind), pool.Id, poolRaw); err != nil {
		t.Fatal(err)
	}
	backend := v1.HostedSearchBackend_HOSTED_SEARCH_BACKEND_GEMINI
	targetSelection := &v1.HostedHelperTarget{Selection: &v1.HostedHelperTarget_PoolId{PoolId: pool.Id}, ModelId: model}
	if mode == "direct target" {
		targetSelection = &v1.HostedHelperTarget{Selection: &v1.HostedHelperTarget_ConnectionId{ConnectionId: "a"}, ModelId: model}
	}
	settings := &v1.HostedSettings{Search: &v1.HostedSearchSettings{Backend: &backend, Target: targetSelection}}
	settingsRaw, _ := protojson.Marshal(settings)
	if err := a.s.PutPrivate(context.Background(), sc, "hosted-helpers", "settings", settingsRaw); err != nil {
		t.Fatal(err)
	}
	p := admitRouting(t, a, key)
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var envelope map[string]any
		if err := json.NewDecoder(req.Body).Decode(&envelope); err != nil {
			t.Error(err)
		}
		project, _ := envelope["project"].(string)
		account := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer token-")
		if project != "project-"+account {
			t.Errorf("token/project mismatch: %s %s", account, project)
		}
		native, _ := envelope["request"].(map[string]any)
		tools, _ := native["tools"].([]any)
		if len(tools) != 1 {
			t.Error("grounding lost")
		}
		mu.Lock()
		sent = append(sent, account)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if account == "a" && mode == "pool replaced" {
			changed := &v1.Pool{Id: pool.Id, Members: []*v1.PoolMember{{ConnectionId: "a", Weight: 1}}}
			raw, _ := json.Marshal(changed)
			if err := a.s.PutPrivate(context.Background(), sc, string(poolKind), pool.Id, raw); err != nil {
				t.Error(err)
			}
		}
		if account == "a" && (mode == "paused sibling" || mode == "stale roster" || mode == "replaced generation" || mode == "needs reauth") {
			d := initialData["b"]
			switch mode {
			case "paused sibling":
				d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_DISABLED
			case "stale roster":
				d.ModelsDiscoveredAt = time.Now().Add(-7 * time.Hour)
			case "replaced generation":
				d.Credential.AccessToken = "token-temporary"
				putRoutingProvider(t, a, sc, d)
				d.Credential.AccessToken = "token-b"
			case "needs reauth":
				lease, err := a.current(p)
				if err != nil {
					t.Error(err)
					break
				}
				_, version, err := a.providerVersion(context.Background(), sc, "b")
				if err != nil {
					t.Error(err)
					break
				}
				if err := a.s.MarkPrivateReauthentication(context.Background(), lease, string(providerKind), "b", version); err != nil {
					t.Error(err)
				}
			}
			if mode != "needs reauth" {
				putRoutingProvider(t, a, sc, d)
			}
		}
		if account == "a" && mode == "phrase only" {
			w.WriteHeader(403)
			io.WriteString(w, `{"error":{"message":"VALIDATION_REQUIRED: account validation is required"}}`)
			return
		}
		if account == "a" && mode == "ordinary 403" {
			w.WriteHeader(403)
			io.WriteString(w, `{"error":{"status":"PERMISSION_DENIED"}}`)
			return
		}
		if account == "a" || account == "b" && mode == "repeated refusal" {
			w.WriteHeader(403)
			io.WriteString(w, validationEnvelope)
			return
		}
		io.WriteString(w, `{"response":{"candidates":[{"content":{"parts":[{"text":"`+strings.ToUpper(account)+` answer. Source: https://source.test/page"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}}`)
	}))
	defer srv.Close()
	resolver := &hostedResolver{auth: a, adapters: map[inference.Wire]inference.Adapter{google.WireAntigravity: google.NewAntigravity(google.Options{})}}
	req := hosted.OperationRequest{Principal: p, RequestID: "search-fixture", Kind: hosted.Search, Arguments: json.RawMessage(`{"query":"fixture query"}`)}
	executor, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	op := executor.(*hostedOperation)
	if op.credential.AccountID != "a" {
		t.Fatalf("initial=%s", op.credential.AccountID)
	}
	op.target.BaseURL, op.transport.base = srv.URL, srv.URL
	op.transport.policy = inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}
	ctx := context.Background()
	if mode == "budget exhausted" {
		attempts := 0
		ctx = inference.WithPhysicalSendBudget(ctx, func(c context.Context, start func(context.Context) error) error {
			attempts++
			if attempts > 1 {
				return inference.ErrUnavailable
			}
			return start(c)
		})
	}
	result, err := op.Execute(ctx, req)
	if mode != "success" {
		var refusal *inference.Error
		if !errors.As(err, &refusal) || refusal.Status != 403 || result.Output != "" {
			t.Fatalf("result=%+v error=%#v refusal=%+v", result, err, refusal)
		}
	} else if err != nil || result.Output != "B answer. Source: https://source.test/page" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	mu.Lock()
	got := append([]string(nil), sent...)
	mu.Unlock()
	want := []string{"a", "b"}
	if mode == "pool replaced" || mode == "phrase only" || mode == "ordinary 403" || mode == "direct target" || mode == "paused sibling" || mode == "stale roster" || mode == "replaced generation" || mode == "needs reauth" || mode == "budget exhausted" {
		want = []string{"a"}
	}
	if len(got) != len(want) || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sends=%v", got)
	}
	if op.credential.AccountID != "a" {
		t.Fatal("parent account changed")
	}
	if mode == "success" || mode == "repeated refusal" {
		for _, id := range []string{"a", "b"} {
			if _, _, err := a.providerVersion(context.Background(), sc, id); err != nil {
				t.Fatalf("validation refusal changed account %s: %v", id, err)
			}
		}
	}
	usage, err := a.s.ListUsage(context.Background(), sc, sc.UserID, 10)
	if err != nil || len(usage) != len(want) {
		t.Fatalf("usage=%d error=%v", len(usage), err)
	}
}

func TestHostedValidationRefusalUsesOneSibling(t *testing.T) {
	for _, mode := range []string{"success", "repeated refusal", "pool replaced", "phrase only", "ordinary 403", "direct target", "paused sibling", "stale roster", "replaced generation", "needs reauth", "budget exhausted"} {
		t.Run(mode, func(t *testing.T) { hostedValidationFixture(t, mode) })
	}
}
