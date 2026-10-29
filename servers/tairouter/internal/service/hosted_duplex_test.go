// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/hosted"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/cursor"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/devin"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/workbuddy"
	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func duplexField(n int, data []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(n<<3|2))
	b = binary.AppendUvarint(b, uint64(len(data)))
	return append(b, data...)
}

func TestHostedCursorDuplexProgress(t *testing.T) {
	const token = "fixture-only-token"
	const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg=="
	for _, name := range []string{"direct", "hosted", "routed"} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var runID string
			var sends, records int
			appendSeen := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token {
					http.Error(w, "unexpected auth", 401)
					return
				}
				mu.Lock()
				sends++
				mu.Unlock()
				switch r.URL.Path {
				case cursor.RunSSEPath:
					if _, err := cursor.ReadFrame(r.Body); err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					runID = r.Header.Get("X-Request-Id")
					mu.Unlock()
					w.Header().Set("Content-Type", "application/connect+proto")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					select {
					case <-appendSeen:
					case <-r.Context().Done():
						return
					}
					for _, frame := range []cursor.Frame{
						{Payload: duplexField(1, duplexField(1, duplexField(1, []byte("image description"))))},
						{Payload: duplexField(1, duplexField(14, nil))},
						{Flags: 2, Payload: []byte(`{}`)},
					} {
						b, err := cursor.EncodeFrame(frame)
						if err != nil {
							t.Error(err)
							return
						}
						if _, err = w.Write(b); err != nil {
							return
						}
						w.(http.Flusher).Flush()
					}
				case cursor.BidiAppendPath:
					mu.Lock()
					sameID := runID != "" && runID == r.Header.Get("X-Request-Id")
					mu.Unlock()
					if !sameID || r.Header.Get("Content-Type") != "application/proto" {
						http.Error(w, "append identity", 400)
						return
					}
					if b, err := io.ReadAll(r.Body); err != nil || len(b) == 0 {
						http.Error(w, "empty append", 400)
						return
					}
					close(appendSeen)
					w.Header().Set("Content-Type", "application/proto")
					w.WriteHeader(200)
				default:
					http.Error(w, "unexpected path", 404)
				}
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			target := inference.Target{Provider: "cursor", BaseURL: srv.URL, Model: "claude-4.6-sonnet", Wire: cursor.Wire, Auth: inference.OAuth}
			cred := inference.Credential{AccountID: "account-a", Secret: token}
			if name == "routed" {
				auth, sc, key := dispatchFixture(t)
				provider := providerData{Metadata: &v1.ProviderConnection{Id: "account-a", OrganizationId: sc.OrganizationID, ProviderId: "cursor", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Credential: &providers.Credential{Scope: sc, Provider: "cursor", AccessToken: token}, Models: &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: target.Model, InputModalities: []string{"text", "image"}}}}, ModelsDiscoveredAt: time.Now()}
				putRoutingProvider(t, auth, sc, provider)
				putRoutingRoute(t, auth, sc, &v1.Route{Id: "vision-route", OrganizationId: sc.OrganizationID, ModelId: "vision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "account-a"}, ModelId: target.Model}}})
				backend := v1.HostedVisionBackend_HOSTED_VISION_BACKEND_ROUTED
				settings := &v1.HostedSettings{Vision: &v1.HostedVisionSettings{Backend: &backend, Selection: &v1.HostedVisionSettings_RouteModel{RouteModel: "vision-route"}}}
				raw, err := protojson.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				if err = auth.s.PutPrivate(ctx, sc, "hosted-helpers", "settings", raw); err != nil {
					t.Fatal(err)
				}
				principal := admitRouting(t, auth, key)
				request := hosted.OperationRequest{Principal: principal, RequestID: "fixture-turn", CallID: "vision", Kind: hosted.Vision, Arguments: json.RawMessage(`{"image_url":{"url":"` + png + `"}}`)}
				resolver := &hostedResolver{auth: auth}
				resolver.adapters = map[inference.Wire]inference.Adapter{cursor.Wire: cursor.NewWithVision(hostedVisionPrepare(auth, nil, nil))}
				executor, err := resolver.Resolve(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				op := executor.(*hostedOperation)
				if op.target.Model != target.Model || op.credential.AccountID != "account-a" {
					t.Fatalf("wrong routed target: %+v %+v", op.target, op.credential)
				}
				// Cursor's pinned production origin is replaced only inside this fixture.
				op.target.BaseURL = srv.URL
				op.transport.base = srv.URL
				op.transport.policy = inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}
				result, err := op.Execute(ctx, request)
				if err != nil || !strings.Contains(result.Output, "image description") {
					t.Fatalf("routed result=%+v err=%v", result, err)
				}
				mu.Lock()
				gotSends := sends
				mu.Unlock()
				if gotSends != 2 {
					t.Fatalf("routed sends=%d", gotSends)
				}
				current := provider.Models
				provider.Models = nil
				putRoutingProvider(t, auth, sc, provider)
				if _, err = resolver.Resolve(ctx, request); err == nil {
					t.Fatal("missing Cursor catalog selected")
				}
				provider.Models = current
				provider.ModelsDiscoveredAt = time.Now().Add(-7 * time.Hour)
				putRoutingProvider(t, auth, sc, provider)
				if _, err = resolver.Resolve(ctx, request); err == nil {
					t.Fatal("stale Cursor catalog selected")
				}
				provider.ModelsDiscoveredAt = time.Now()
				provider.Models.Models[0].ID = "composer-2.5"
				putRoutingProvider(t, auth, sc, provider)
				putRoutingRoute(t, auth, sc, &v1.Route{Id: "vision-route", OrganizationId: sc.OrganizationID, ModelId: "vision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "account-a"}, ModelId: "composer-2.5"}}})
				executor, err = resolver.Resolve(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				noVision := executor.(*hostedOperation)
				noVision.target.BaseURL, noVision.transport.base = srv.URL, srv.URL
				noVision.transport.policy = inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}
				if _, err = noVision.Execute(ctx, request); !errors.Is(err, inference.ErrUnsupported) {
					t.Fatalf("non-vision Cursor recursion: %v", err)
				}
				mu.Lock()
				gotSends = sends
				mu.Unlock()
				if gotSends != 2 {
					t.Fatalf("recursive send count=%d", gotSends)
				}
				return
			}
			var x inference.AdapterExecutor = directCursorExecutor{client: srv.Client()}
			if name == "hosted" {
				x = &hostedTransport{base: srv.URL, provider: "cursor", model: target.Model, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, start func(context.Context) error) error { return start(c) }, record: func(context.Context, string, store.Usage) error { mu.Lock(); records++; mu.Unlock(); return nil }}
			}
			in := inference.AdapterInput{Principal: inference.Principal{OrganizationID: "org-a", UserID: "user-a", MembershipGeneration: 1}, Target: target, Credential: cred, Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(`{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"` + png + `"}}]}]}`)}
			a := cursor.New()
			body, err := a.Prepare(ctx, in, x)
			if err != nil {
				t.Fatal(err)
			}
			in.Body = body
			r, _, err := a.Execute(ctx, in, x, nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(r.Body)
			err = errors.Join(err, r.Body.Close())
			if err != nil || !bytes.Contains(out, []byte("image description")) || !bytes.Contains(out, []byte("[DONE]")) {
				t.Fatalf("response %s: %v", out, err)
			}
			mu.Lock()
			gotSends, gotRecords := sends, records
			mu.Unlock()
			if gotSends != 2 || name == "hosted" && gotRecords != 2 {
				t.Fatalf("sends=%d receipts=%d", gotSends, gotRecords)
			}
		})
	}
}

type directCursorExecutor struct{ client *http.Client }

func (x directCursorExecutor) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, strings.NewReader(string(r.Body)))
	if err != nil {
		return nil, err
	}
	req.Header = r.Headers.Clone()
	return x.client.Do(req)
}

func TestHostedLiveBodyLifecycle(t *testing.T) {
	for _, kind := range []string{"complete", "receipt-failure", "oversize", "early-close", "cancel", "gate"} {
		t.Run(kind, func(t *testing.T) {
			var records []store.Usage
			var sends int
			serverDone := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				if kind == "oversize" {
					_, _ = w.Write(bytes.Repeat([]byte("a"), (32<<20)+1))
					return
				}
				if kind == "early-close" || kind == "cancel" {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(serverDone)
					return
				}
				_, _ = io.WriteString(w, `{"usage":{"input_tokens":2,"output_tokens":3}}`)
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tr := &hostedTransport{base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, start func(context.Context) error) error {
				if kind == "gate" {
					return inference.ErrUnauthorized
				}
				return start(c)
			}, record: func(_ context.Context, _ string, u store.Usage) error {
				records = append(records, u)
				if kind == "receipt-failure" {
					return errors.New("storage failed")
				}
				return nil
			}}
			resp, err := tr.Do(ctx, inference.AdapterRequest{Method: http.MethodPost, URL: srv.URL})
			if kind == "gate" {
				if !errors.Is(err, inference.ErrUnauthorized) || sends != 0 || len(records) != 0 {
					t.Fatalf("gate: %v sends=%d receipts=%d", err, sends, len(records))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "early-close" || kind == "cancel" {
				if kind == "cancel" {
					cancel()
				}
				err = resp.Body.Close()
				select {
				case <-serverDone:
				case <-time.After(time.Second):
					t.Fatal("upstream body remained open")
				}
			} else {
				_, err = io.Copy(io.Discard, resp.Body)
				err = errors.Join(err, resp.Body.Close())
			}
			if kind == "receipt-failure" && !errors.Is(err, inference.ErrUnavailable) {
				t.Fatalf("receipt failure hidden: %v", err)
			}
			if kind == "oversize" {
				var sizeErr *inference.Error
				if !errors.As(err, &sizeErr) || sizeErr.Code != "hosted_response_too_large" {
					t.Fatalf("size failure: %v", err)
				}
			}
			if len(records) != 1 || sends != 1 {
				t.Fatalf("sends=%d receipts=%d", sends, len(records))
			}
			if kind == "complete" && (records[0].InputTokens == nil || *records[0].InputTokens != 2) {
				t.Fatalf("usage lost: %+v", records[0])
			}
			if kind == "early-close" || kind == "cancel" || kind == "oversize" {
				if records[0].InputTokens != nil || records[0].OutputTokens != nil {
					t.Fatal("unknown usage became known")
				}
			}
		})
	}
}

func TestHostedCursorDuplexFailureStopsBothDirections(t *testing.T) {
	for _, kind := range []string{"first-frame-silence", "append-failure"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var receipts int
			var appendSeen bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case cursor.RunSSEPath:
					if _, err := cursor.ReadFrame(r.Body); err != nil {
						t.Error(err)
						return
					}
					w.Header().Set("Content-Type", "application/connect+proto")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case cursor.BidiAppendPath:
					mu.Lock()
					appendSeen = true
					mu.Unlock()
					if kind == "append-failure" {
						http.Error(w, "fixture refusal", 500)
						return
					}
					w.Header().Set("Content-Type", "application/proto")
					w.WriteHeader(200)
				default:
					http.Error(w, "unexpected path", 404)
				}
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			x := &hostedTransport{base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, start func(context.Context) error) error { return start(c) }, record: func(context.Context, string, store.Usage) error {
				mu.Lock()
				receipts++
				fail := kind == "append-failure" && receipts == 2
				mu.Unlock()
				if fail {
					return errors.New("receipt unavailable")
				}
				return nil
			}}
			in := inference.AdapterInput{Principal: inference.Principal{OrganizationID: "org", UserID: "user"}, Target: inference.Target{Provider: "cursor", BaseURL: srv.URL, Model: "claude-4.6-sonnet", Wire: cursor.Wire, Auth: inference.OAuth}, Credential: inference.Credential{AccountID: "account", Secret: "fixture-only-token"}, Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(`{"messages":[{"role":"user","content":"hello"}]}`)}
			r, _, err := cursor.New().Execute(ctx, in, x, nil)
			if err == nil {
				_, err = io.Copy(io.Discard, r.Body)
				err = errors.Join(err, r.Body.Close())
			}
			mu.Lock()
			gotAppend, gotReceipts := appendSeen, receipts
			mu.Unlock()
			if err == nil || !gotAppend || gotReceipts != 2 {
				t.Fatalf("failure=%v append=%t receipts=%d", err, gotAppend, gotReceipts)
			}
			if kind == "append-failure" && !errors.Is(err, inference.ErrUnavailable) {
				t.Fatalf("run receipt failure hidden: %v", err)
			}
		})
	}
}

// The adapter ends at [DONE] while HTTP remains open, so receipt persistence
// happens during Collect's owned-body close rather than an upstream EOF read.
func TestHostedWorkBuddyTerminalReceiptFailure(t *testing.T) {
	for _, failReceipt := range []bool{false, true} {
		t.Run(fmt.Sprintf("receipt-failure=%t", failReceipt), func(t *testing.T) {
			closed := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"description\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var records int
			var receipt store.Usage
			tr := &hostedTransport{base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, start func(context.Context) error) error { return start(c) }, price: func(u *store.Usage, usage inference.Usage) {
				if usage.InputTokens != nil && usage.OutputTokens != nil {
					cost := "0.000005"
					u.Cost, u.PriceKnown = &cost, true
				}
			}, record: func(_ context.Context, _ string, u store.Usage) error {
				receipt = u
				records++
				if failReceipt {
					return errors.New("fixture receipt failure")
				}
				return nil
			}}
			response, err := tr.Do(ctx, inference.AdapterRequest{Method: http.MethodPost, URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			response, err = workbuddy.New().Decode(ctx, response)
			if err != nil {
				t.Fatal(err)
			}
			body, err := inference.AggregateResponse(ctx, response, inference.Chat)
			if failReceipt {
				if !errors.Is(err, inference.ErrUnavailable) || strings.Contains(string(body), "choices") {
					t.Fatalf("receipt failure accepted as content: %s, %v", body, err)
				}
			} else if err != nil || !strings.Contains(string(body), "description") {
				t.Fatalf("successful terminal rejected: %s, %v", body, err)
			}
			if receipt.Outcome != store.OutcomeSucceeded || receipt.InputTokens == nil || *receipt.InputTokens != 2 || receipt.OutputTokens == nil || *receipt.OutputTokens != 3 || receipt.Cost == nil || *receipt.Cost != "0.000005" || !receipt.PriceKnown {
				t.Fatalf("terminal usage or pricing lost: %+v", receipt)
			}
			if records != 1 {
				t.Fatalf("receipts=%d", records)
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("upstream remained open")
			}
		})
	}
}

func TestHostedCloseRecognizesOnlyCompleteSSETerminals(t *testing.T) {
	usage := "data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\n"
	for _, tc := range []struct {
		name, data, contentType string
		cancel, complete        bool
	}{
		{name: "done", data: usage + "data: [DONE]\n\n", complete: true},
		{name: "crlf", data: strings.ReplaceAll(usage+"data: [DONE]\n\n", "\n", "\r\n"), complete: true},
		{name: "responses", data: "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}}\n\n", complete: true},
		{name: "messages", data: usage + "data: {\"type\":\"message_stop\"}\n\n", complete: true},
		{name: "usage-only", data: usage},
		{name: "partial-marker", data: usage + "data: [DONE]\n"},
		{name: "malformed", data: "data: {invalid}\n\n" + usage + "data: [DONE]\n\n"},
		{name: "error", data: usage + "data: {\"error\":{\"message\":\"fixture\"}}\n\ndata: [DONE]\n\n"},
		{name: "cancelled", data: usage + "data: [DONE]\n\n", cancel: true},
		{name: "finite-json", data: "{\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}", contentType: "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var receipt store.Usage
			var records int
			tr := &hostedTransport{record: func(_ context.Context, _ string, u store.Usage) error { records++; receipt = u; return nil }}
			ct := tc.contentType
			if ct == "" {
				ct = "text/event-stream"
			}
			source := io.NopCloser(strings.NewReader(tc.data))
			b := &hostedBody{reader: source, body: source, ctx: ctx, t: tr, limit: 32 << 20, contentType: ct}
			if _, err := io.ReadFull(b, make([]byte, len(tc.data))); err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				cancel()
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if records != 1 {
				t.Fatalf("receipts=%d", records)
			}
			if tc.complete {
				if receipt.Outcome != store.OutcomeSucceeded || receipt.InputTokens == nil || *receipt.InputTokens != 2 || receipt.OutputTokens == nil || *receipt.OutputTokens != 3 {
					t.Fatalf("terminal usage lost: %+v", receipt)
				}
			} else if receipt.InputTokens != nil || receipt.OutputTokens != nil || receipt.Outcome == store.OutcomeSucceeded {
				t.Fatalf("partial or cancelled body became confirmed: %+v", receipt)
			}
		})
	}
}

// A Connect trailer completes decoding while the underlying HTTP stream is open.
// A receipt failure at source close must reach the aggregate caller through Devin.
func TestHostedDevinTerminalReceiptFailure(t *testing.T) {
	for _, failReceipt := range []bool{false, true} {
		t.Run(fmt.Sprintf("receipt-failure=%t", failReceipt), func(t *testing.T) {
			closed := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/connect+proto")
				for _, frame := range []cursor.Frame{{Payload: duplexField(3, []byte("devin description"))}, {Flags: 2, Payload: []byte("{}")}} {
					encoded, err := cursor.EncodeFrame(frame)
					if err != nil {
						t.Error(err)
						return
					}
					if _, err = w.Write(encoded); err != nil {
						return
					}
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var records int
			tr := &hostedTransport{base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, start func(context.Context) error) error { return start(c) }, record: func(context.Context, string, store.Usage) error {
				records++
				if failReceipt {
					return errors.New("fixture receipt failure")
				}
				return nil
			}}
			response, err := tr.Do(ctx, inference.AdapterRequest{Method: http.MethodPost, URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			response, err = devin.New().Decode(ctx, response)
			if err != nil {
				t.Fatal(err)
			}
			body, err := inference.AggregateResponse(ctx, response, inference.Chat)
			if failReceipt {
				if !errors.Is(err, inference.ErrUnavailable) || strings.Contains(string(body), "choices") {
					t.Fatalf("receipt failure accepted as content: %s, %v", body, err)
				}
			} else if err != nil || !strings.Contains(string(body), "devin description") {
				t.Fatalf("successful terminal rejected: %s, %v", body, err)
			}
			if records != 1 {
				t.Fatalf("receipts=%d", records)
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("upstream remained open")
			}
		})
	}
}

func TestHostedAdapterRequestMethod(t *testing.T) {
	for _, method := range []string{"", http.MethodPost, http.MethodGet} {
		t.Run(map[string]string{"": "default", http.MethodPost: "explicit-post", http.MethodGet: "explicit-get"}[method], func(t *testing.T) {
			expected := method
			if expected == "" {
				expected = http.MethodPost
			}
			var mu sync.Mutex
			var sends, records int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				sends++
				mu.Unlock()
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != expected || string(body) != "fixture-body" {
					t.Errorf("request method=%s body=%s error=%v", r.Method, body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, "{}")
			}))
			defer srv.Close()
			tr := &hostedTransport{base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}}, gate: func(c context.Context, start func(context.Context) error) error { return start(c) }, record: func(context.Context, string, store.Usage) error { mu.Lock(); records++; mu.Unlock(); return nil }}
			response, err := tr.Do(context.Background(), inference.AdapterRequest{Method: method, URL: srv.URL, Body: []byte("fixture-body")})
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, response.Body)
			if err = errors.Join(err, response.Body.Close()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actualSends, actualRecords := sends, records
			mu.Unlock()
			if actualSends != 1 || actualRecords != 1 {
				t.Fatalf("sends=%d receipts=%d", actualSends, actualRecords)
			}
		})
	}
}
