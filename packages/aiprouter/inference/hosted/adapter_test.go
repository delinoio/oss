// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type provider struct{}

func (provider) Build(_ context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	return inference.AdapterRequest{URL: "https://model.test/generate", Body: in.Body, ResponseWire: inference.Chat}, nil
}
func (provider) Decode(_ context.Context, r *http.Response) (*http.Response, error) { return r, nil }

type executor struct {
	replies []string
	sends   int
	usage   []inference.Usage
	bodies  []json.RawMessage
}

func (e *executor) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	err := inference.PhysicalSend(ctx, func(context.Context) error { e.sends++; return nil })
	if err != nil {
		return nil, err
	}
	e.bodies = append(e.bodies, append([]byte(nil), r.Body...))
	if len(e.replies) == 0 {
		return nil, errors.New("fixture exhausted")
	}
	text := e.replies[0]
	e.replies = e.replies[1:]
	return response(text), nil
}
func (e *executor) RecordUsage(u inference.Usage) { e.usage = append(e.usage, u) }
func response(s string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(s))}
}

type resolverFunc func(context.Context, OperationRequest) (OperationExecutor, error)

func (f resolverFunc) Resolve(c context.Context, r OperationRequest) (OperationExecutor, error) {
	return f(c, r)
}

type operationFunc func(context.Context, OperationRequest) (OperationResult, error)

func (f operationFunc) Execute(c context.Context, r OperationRequest) (OperationResult, error) {
	return f(c, r)
}
func input() inference.AdapterInput {
	return inference.AdapterInput{Principal: inference.Principal{OrganizationID: "org-a", UserID: "same", KeyID: "key", MembershipGeneration: 1}, Wire: inference.Chat, Operation: inference.Generate, Body: json.RawMessage(`{"model":"fixture","messages":[{"role":"user","content":"question"}],"tools":[{"type":"web_search"}]}`)}
}
func call(name, args string) string {
	b, _ := json.Marshal(object{"id": "r1", "choices": []any{object{"message": object{"role": "assistant", "content": "", "tool_calls": []any{object{"id": "call1", "type": "function", "function": object{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}, "usage": object{"prompt_tokens": 7, "completion_tokens": 2}})
	return string(b)
}

const answer = `{"id":"r2","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3}}`

func budget(limit int) (context.Context, *int) {
	sends := new(int)
	ctx := inference.WithPhysicalSendBudget(context.Background(), func(ctx context.Context, start func(context.Context) error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if *sends >= limit {
			return fault(429, "fixture_budget")
		}
		*sends++
		return start(ctx)
	})
	return ctx, sends
}
func service(t *testing.T, seen *[]OperationRequest) Resolver {
	return resolverFunc(func(_ context.Context, r OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, r OperationRequest) (OperationResult, error) {
			err := inference.PhysicalSend(ctx, func(context.Context) error { *seen = append(*seen, r); return nil })
			return OperationResult{Output: "result", Sources: []Source{{URL: "https://source.test"}}}, err
		}), nil
	})
}
func TestMultiQuerySharesPhysicalBudget(t *testing.T) {
	for _, limit := range []int{3, 4} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			ctx, sends := budget(limit)
			seen := []OperationRequest{}
			a, _ := New(provider{}, Config{Resolver: service(t, &seen)})
			e := &executor{replies: []string{call("web_search", `{"queries":[" A ","a","B"]}`), answer}}
			r, w, err := a.Execute(ctx, input(), e, nil)
			if limit == 3 {
				if err == nil || *sends != 3 {
					t.Fatalf("err=%v sends=%d", err, *sends)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if w != inference.Chat || *sends != 4 || len(seen) != 2 || len(e.usage) != 1 {
				t.Fatalf("wire=%v sends=%d ops=%d usage=%d", w, *sends, len(seen), len(e.usage))
			}
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), "web_search_call") || strings.Contains(string(b), "tool_calls") {
				t.Fatalf("bad output %s", b)
			}
			if *e.usage[0].InputTokens != 7 {
				t.Fatal("lost intermediate usage")
			}
		})
	}
}
func TestCancellationStopsFollowup(t *testing.T) {
	ctx, sends := budget(10)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, _ OperationRequest) (OperationResult, error) {
			err := inference.PhysicalSend(ctx, func(context.Context) error { return nil })
			cancel()
			return OperationResult{Output: "late"}, err
		}), nil
	})})
	e := &executor{replies: []string{call("web_search", `{"queries":["a","b"]}`), answer}}
	_, _, err := a.Execute(ctx, input(), e, nil)
	if !errors.Is(err, context.Canceled) || *sends != 2 || e.sends != 1 {
		t.Fatalf("err=%v sends=%d model=%d", err, *sends, e.sends)
	}
}
func TestFailureIsNotRetried(t *testing.T) {
	ctx, sends := budget(10)
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, _ OperationRequest) (OperationResult, error) {
			_ = inference.PhysicalSend(ctx, func(context.Context) error { return nil })
			return OperationResult{}, inference.ErrAmbiguous
		}), nil
	})})
	e := &executor{replies: []string{call("web_search", `{"query":"a"}`), answer}}
	_, _, err := a.Execute(ctx, input(), e, nil)
	if !errors.Is(err, inference.ErrAmbiguous) || *sends != 2 || len(e.usage) != 1 {
		t.Fatalf("%v sends=%d", err, *sends)
	}
}
func TestTenantIsolationConcurrent(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(_ context.Context, r OperationRequest) (OperationExecutor, error) {
		owner := r.Principal
		return operationFunc(func(ctx context.Context, r OperationRequest) (OperationResult, error) {
			if r.Principal != owner {
				return OperationResult{}, inference.ErrUnauthorized
			}
			err := inference.PhysicalSend(ctx, func(context.Context) error { mu.Lock(); defer mu.Unlock(); seen[owner.OrganizationID]++; return nil })
			return OperationResult{Output: owner.OrganizationID}, err
		}), nil
	})})
	var wg sync.WaitGroup
	for _, org := range []string{"org-a", "org-b"} {
		wg.Add(1)
		go func(org string) {
			defer wg.Done()
			ctx, _ := budget(4)
			in := input()
			in.Principal.OrganizationID = org
			e := &executor{replies: []string{call("web_search", `{"query":"same"}`), answer}}
			r, _, err := a.Execute(ctx, in, e, nil)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body.Close()
			if !strings.Contains(string(e.bodies[1]), org) {
				t.Error("owner result missing")
			}
		}(org)
	}
	wg.Wait()
	if seen["org-a"] != 1 || seen["org-b"] != 1 {
		t.Fatal(seen)
	}
}
func TestClientFunctionNeverActivatesHosted(t *testing.T) {
	in := input()
	in.Body = json.RawMessage(`{"messages":[],"tools":[{"type":"function","function":{"name":"web_search","parameters":{}}}]}`)
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		t.Fatal("client tool executed")
		return nil, nil
	})})
	e := &executor{replies: []string{call("web_search", `{"query":"a"}`)}}
	ctx, sends := budget(3)
	r, _, err := a.Execute(ctx, in, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if *sends != 1 {
		t.Fatal(*sends)
	}
}
func TestSearchLimitForcesAnswer(t *testing.T) {
	ctx, sends := budget(10)
	seen := []OperationRequest{}
	a, _ := New(provider{}, Config{Resolver: service(t, &seen), MaxSearches: 1})
	e := &executor{replies: []string{call("web_search", `{"queries":["a","b"]}`), answer}}
	r, _, err := a.Execute(ctx, input(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if *sends != 3 || len(seen) != 1 {
		t.Fatal(*sends, len(seen))
	}
	m, _ := decode(e.bodies[1])
	if m["tool_choice"] != "none" || len(arr(m["tools"])) != 0 {
		t.Fatal("tools not disabled")
	}
}
func TestImageArtifactsAndVision(t *testing.T) {
	ctx, sends := budget(10)
	kinds := []Kind{}
	a, _ := New(provider{}, Config{DescribeImages: true, Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, r OperationRequest) (OperationResult, error) {
			err := inference.PhysicalSend(ctx, func(context.Context) error { kinds = append(kinds, r.Kind); return nil })
			out := OperationResult{Output: "description"}
			if r.Kind == Image {
				out.Artifacts = []Artifact{{ID: "owned", URL: "https://r2.test/signed", ContentType: "image/png"}}
			}
			return out, err
		}), nil
	})})
	in := input()
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://input.test/a"}}]}],"tools":[{"type":"image_generation"}]}`)
	e := &executor{replies: []string{call("image_gen", `{"prompt":"draw"}`), answer}}
	r, _, err := a.Execute(ctx, in, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if *sends != 4 || len(kinds) != 2 || kinds[0] != Vision || kinds[1] != Image || !strings.Contains(string(b), "https://r2.test/signed") {
		t.Fatalf("sends=%d kinds=%v body=%s", *sends, kinds, b)
	}
	if strings.Contains(string(e.bodies[0]), "https://input.test/a") {
		t.Fatal("image not described")
	}
}
func TestCodecTruncationAndBound(t *testing.T) {
	c := StandardCodec{}
	for _, body := range []string{"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n", strings.Repeat("x", 100)} {
		r := response(body)
		r.Header.Set("Content-Type", "text/event-stream")
		if _, err := c.Collect(context.Background(), r, inference.Chat, 64); err == nil {
			t.Fatal("accepted invalid/oversize stream")
		}
	}
}

func TestPublicSearchBatch(t *testing.T) {
	ctx, sends := budget(2)
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		t.Fatal("changed search owner")
		return nil, nil
	})})
	in := input()
	in.Operation = inference.Search
	in.Body = json.RawMessage(`{"queries":["a","b"]}`)
	e := &executor{replies: []string{`{"encrypted_output":null,"output":"A","results":[{"url":"https://a.test"}]}`, `{"encrypted_output":null,"output":"B","results":[{"url":"https://b.test"}]}`}}
	r, _, err := a.Execute(ctx, in, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	m, _ := decode(b)
	if *sends != 2 || len(arr(m["results"])) != 2 || m["output"] != "A\n\nB" {
		t.Fatalf("%d %s", *sends, b)
	}
	for _, b := range e.bodies {
		m, _ := decode(b)
		if m["queries"] != nil || m["query"] == nil {
			t.Fatal("not normalized")
		}
	}
}
func TestVideoAndMixedClientTools(t *testing.T) {
	ctx, sends := budget(10)
	seen := 0
	a, _ := New(provider{}, Config{EnableVideo: true, Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, r OperationRequest) (OperationResult, error) {
			if r.Kind != Video {
				t.Fatal(r.Kind)
			}
			err := inference.PhysicalSend(ctx, func(context.Context) error { seen++; return nil })
			return OperationResult{Artifacts: []Artifact{{ID: "v", URL: "https://r2.test/video", ContentType: "video/mp4"}}}, err
		}), nil
	})})
	in := input()
	in.Body = json.RawMessage(`{"messages":[],"tools":[{"type":"function","function":{"name":"client","parameters":{}}}]}`)
	m, _ := decode([]byte(call("video_gen", `{"prompt":"animate","duration":6}`)))
	message := obj(obj(arr(m["choices"])[0])["message"])
	message["tool_calls"] = append(arr(message["tool_calls"]), object{"id": "client1", "type": "function", "function": object{"name": "client", "arguments": "{}"}})
	b, _ := json.Marshal(m)
	e := &executor{replies: []string{string(b)}}
	r, _, err := a.Execute(ctx, in, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ = io.ReadAll(r.Body)
	out, _ := decode(b)
	message = obj(obj(arr(out["choices"])[0])["message"])
	if len(arr(message["tool_calls"])) != 1 || seen != 1 || *sends != 2 || len(e.usage) != 0 {
		t.Fatalf("seen=%d sends=%d usage=%d body=%s", seen, *sends, len(e.usage), b)
	}
	if !strings.Contains(string(b), "video_generation_call") {
		t.Fatal(string(b))
	}
}
func TestStreamMetadataRoundTrip(t *testing.T) {
	ctx, _ := budget(4)
	seen := []OperationRequest{}
	a, _ := New(provider{}, Config{Resolver: service(t, &seen)})
	in := input()
	m, _ := decode(in.Body)
	m["stream"] = true
	in.Body, _ = json.Marshal(m)
	e := &executor{replies: []string{call("web_search", `{"query":"a"}`), answer}}
	r, w, err := a.Execute(ctx, in, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := (StandardCodec{}).Collect(ctx, r, w, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []inference.Wire{inference.Chat, inference.Responses, inference.Anthropic} {
		converted, err := inference.TranslateResponseJSON(b, inference.Chat, wire)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(converted), "web_search") {
			t.Fatalf("%s missing metadata %s", wire, converted)
		}
	}
}

type historyExecutor struct {
	executor
	history json.RawMessage
}

func (e *historyExecutor) RecordContinuationHistory(raw json.RawMessage, w inference.Wire) error {
	if w != inference.Chat {
		return errors.New("unexpected history wire")
	}
	e.history = append([]byte(nil), raw...)
	return nil
}
func TestHostedContinuationRetainsResults(t *testing.T) {
	ctx, _ := budget(4)
	seen := []OperationRequest{}
	a, _ := New(provider{}, Config{Resolver: service(t, &seen)})
	e := &historyExecutor{executor: executor{replies: []string{call("web_search", `{"query":"a"}`), answer}}}
	r, _, err := a.Execute(ctx, input(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	m, _ := decode(e.history)
	messages := arr(m["messages"])
	if len(messages) != 3 || obj(messages[1])["role"] != "assistant" || obj(messages[2])["role"] != "tool" || !strings.Contains(str(obj(messages[2])["content"]), "result") {
		t.Fatalf("missing private history: %s", e.history)
	}
}
func TestFailedQueryDeduplicatedAcrossRounds(t *testing.T) {
	ctx, sends := budget(10)
	operations := 0
	a, _ := New(provider{}, Config{Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		return operationFunc(func(ctx context.Context, _ OperationRequest) (OperationResult, error) {
			err := inference.PhysicalSend(ctx, func(context.Context) error { operations++; return nil })
			return OperationResult{Failed: true, Output: "search unavailable"}, err
		}), nil
	})})
	second := strings.ReplaceAll(call("web_search", `{"query":"A"}`), "call1", "call2")
	e := &executor{replies: []string{call("web_search", `{"query":"a"}`), second, answer}}
	r, _, err := a.Execute(ctx, input(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if operations != 1 || *sends != 4 {
		t.Fatal(operations, *sends)
	}
}

func TestNativeHostedPolicyPreservesDeclarations(t *testing.T) {
	ctx, sends := budget(1)
	a, _ := New(provider{}, Config{NativeTools: map[Kind]bool{Search: true}, Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		t.Fatal("native tool intercepted")
		return nil, nil
	})})
	e := &executor{replies: []string{answer}}
	r, _, err := a.Execute(ctx, input(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	m, _ := decode(e.bodies[0])
	if obj(arr(m["tools"])[0])["type"] != "web_search" || *sends != 1 {
		t.Fatal("native declaration replaced")
	}
}

type repairingProvider struct {
	provider
	repairs  int
	statuses []int
}

func (p *repairingProvider) RepairRequest(_ context.Context, _ inference.AdapterInput, r inference.AdapterRequest, f inference.HTTPFailure) (inference.AdapterRequest, bool, error) {
	p.repairs++
	p.statuses = append(p.statuses, f.Status)
	if (f.Status != 400 && f.Status != 422) || len(f.Body) == 0 {
		return r, false, errors.New("missing refusal")
	}
	r.Body = append(r.Body, ' ')
	return r, true, nil
}

type refusalExecutor struct {
	executor
	status []int
}

func (e *refusalExecutor) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	out, err := e.executor.Do(ctx, r)
	if err == nil {
		out.StatusCode = e.status[0]
		e.status = e.status[1:]
	}
	return out, err
}
func TestProviderRepairUsesSameExecutor(t *testing.T) {
	ctx, sends := budget(3)
	p := &repairingProvider{}
	a, _ := New(p, Config{EnabledTools: map[Kind]bool{}, Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
		t.Fatal("unconfigured helper executed")
		return nil, nil
	})})
	e := &refusalExecutor{executor: executor{replies: []string{`{"error":"schema"}`, `{"error":"thinking"}`, answer}}, status: []int{400, 400, 200}}
	r, _, err := a.Execute(ctx, input(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if p.repairs != 2 || *sends != 3 {
		t.Fatal(p.repairs, *sends)
	}
}

type recoveryPolicy struct{}

func (recoveryPolicy) RecoverHTTP(_ context.Context, _ inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if f.Header.Get("X-Fixture-Expired") == "true" {
		return inference.RecoveryDecision{Action: inference.RecoveryRefresh}, nil
	}
	return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
}

type recoveryExecutor struct {
	executor
	expireAt int
}

func (e *recoveryExecutor) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	out, err := e.executor.Do(ctx, r)
	if err == nil && e.sends == e.expireAt {
		out.Header.Set("X-Fixture-Expired", "true")
	}
	return out, err
}
func TestRecoveryBeforeAndAfterHelper(t *testing.T) {
	for _, expireAt := range []int{1, 2} {
		t.Run(fmt.Sprint(expireAt), func(t *testing.T) {
			ctx, _ := budget(4)
			seen := []OperationRequest{}
			a, _ := New(provider{}, Config{Resolver: service(t, &seen), RecoveryPolicy: recoveryPolicy{}})
			e := &recoveryExecutor{executor: executor{replies: []string{call("web_search", `{"query":"a"}`), answer}}, expireAt: expireAt}
			r, _, err := a.Execute(ctx, input(), e, nil)
			if expireAt == 1 {
				if err != nil {
					t.Fatal(err)
				}
				defer r.Body.Close()
				if r.Header.Get("X-Fixture-Expired") != "true" || len(seen) != 0 {
					t.Fatal("refresh hidden or helper started")
				}
			} else if err == nil || r != nil || len(seen) != 1 {
				t.Fatal("paid helper exposed to outer retry")
			}
		})
	}
}
func TestIncompleteToolNeverExecutes(t *testing.T) {
	ctx, sends := budget(3)
	seen := []OperationRequest{}
	a, _ := New(provider{}, Config{Resolver: service(t, &seen)})
	reply := strings.Replace(call("web_search", `{"query":"a"}`), `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1)
	e := &executor{replies: []string{reply}}
	_, _, err := a.Execute(ctx, input(), e, nil)
	if err == nil || *sends != 1 || len(seen) != 0 {
		t.Fatal(err, *sends, len(seen))
	}
}

func TestProvider422RepairPreservesStatusAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		budget                             int
		statuses                           []int
		wantSends, wantRepairs, wantStatus int
		wantError                          bool
	}{
		{"one repair", 3, []int{422, 200}, 2, 1, 200, false},
		{"mixed refusals", 3, []int{400, 422, 200}, 3, 2, 200, false},
		{"repair ceiling", 5, []int{422, 422, 422}, 3, 2, 422, false},
		{"shared send ceiling", 1, []int{422, 200}, 1, 1, 0, true},
		{"unrelated refusal", 3, []int{403}, 1, 0, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, sends := budget(tc.budget)
			p := &repairingProvider{}
			a, err := New(p, Config{EnabledTools: map[Kind]bool{}, Resolver: resolverFunc(func(context.Context, OperationRequest) (OperationExecutor, error) {
				t.Fatal("unexpected helper")
				return nil, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			replies := make([]string, len(tc.statuses))
			for i := range replies {
				replies[i] = `{"error":"invalid parameter"}`
			}
			if tc.statuses[len(tc.statuses)-1] == 200 {
				replies[len(replies)-1] = answer
			}
			e := &refusalExecutor{executor: executor{replies: replies}, status: append([]int(nil), tc.statuses...)}
			r, _, err := a.Execute(ctx, input(), e, nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if r != nil {
				defer r.Body.Close()
				if r.StatusCode != tc.wantStatus {
					t.Fatalf("status=%d", r.StatusCode)
				}
			}
			if *sends != tc.wantSends || e.sends != tc.wantSends || p.repairs != tc.wantRepairs {
				t.Fatalf("sends=%d executor sends=%d repairs=%d", *sends, e.sends, p.repairs)
			}
			for i, status := range p.statuses {
				if status != tc.statuses[i] {
					t.Fatalf("repair status=%d want=%d", status, tc.statuses[i])
				}
			}
		})
	}
}
