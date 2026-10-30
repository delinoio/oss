// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
)

var ordinaryMetadataKeys = []string{
	"signature", "thoughtSignature", "thought_signature", "reasoning_signature", "kiro_reasoning", "encrypted_content",
}

type countingReceiptState struct {
	receiptMemory
	loads atomic.Int32
	saves atomic.Int32
}

func (s *countingReceiptState) LoadProviderState(ctx context.Context, p Principal, ns, key string) (ProviderState, error) {
	s.loads.Add(1)
	return s.receiptMemory.LoadProviderState(ctx, p, ns, key)
}

func (s *countingReceiptState) SaveProviderState(ctx context.Context, p Principal, ns, key string, value ProviderState) error {
	s.saves.Add(1)
	return s.receiptMemory.SaveProviderState(ctx, p, ns, key, value)
}

func TestResponsesOrdinaryMetadataDoesNotRequireOrCreateReceipts(t *testing.T) {
	for _, key := range append(append([]string{}, ordinaryMetadataKeys...), "label", "") {
		t.Run(key, func(t *testing.T) {
			var sends atomic.Int32
			metadata := object{}
			if key != "" {
				metadata[key] = "ordinary-customer-tag"
			}
			h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				body, err := decode(raw)
				if err != nil || !reflect.DeepEqual(obj(body["metadata"]), metadata) {
					t.Errorf("upstream metadata = %v, error = %v", body["metadata"], err)
				}
				response, err := decode([]byte(fixture(Responses)))
				if err != nil {
					t.Error(err)
				}
				response["metadata"] = metadata
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}, Responses)
			states := &countingReceiptState{receiptMemory: receiptMemory{}}
			h.c.ProviderStates = states
			request := object{"model": "m", "input": "Say ok", "metadata": metadata}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []Wire{Responses, Chat} {
				translated, err := TranslateRequestJSON(raw, Responses, target)
				if err != nil {
					t.Fatal(err)
				}
				body, err := decode(translated)
				if err != nil || !reflect.DeepEqual(obj(body["metadata"]), metadata) {
					t.Fatalf("%s translated metadata = %v, error = %v", target, body["metadata"], err)
				}
			}
			result := call(h, "/v1/responses", string(raw))
			if result.Code != http.StatusOK || sends.Load() != 1 {
				t.Fatalf("status = %d, sends = %d, body = %s", result.Code, sends.Load(), result.Body.String())
			}
			body, err := decode(result.Body.Bytes())
			if err != nil || !reflect.DeepEqual(obj(body["metadata"]), metadata) {
				t.Fatalf("response metadata = %v, error = %v", body["metadata"], err)
			}
			if states.loads.Load() != 0 || states.saves.Load() != 0 {
				t.Fatalf("receipt loads = %d, saves = %d", states.loads.Load(), states.saves.Load())
			}
		})
	}
}

func TestResponsesMetadataExclusionPreservesNestedSignedFields(t *testing.T) {
	metadata := object{"signature": "ordinary-customer-tag"}
	for _, field := range []string{"input", "metadata"} {
		t.Run(field, func(t *testing.T) {
			body := object{"metadata": metadata}
			signed := object{"type": "thinking", "thinking": "reason", "signature": "opaque"}
			if field == "input" {
				body["input"] = []any{signed}
			} else {
				body["input"] = []any{object{"metadata": []any{signed}}}
			}
			tokens, err := signedReplayTokens(body)
			if err != nil || len(tokens) != 1 {
				t.Fatalf("nested signed token = %v, error = %v", tokens, err)
			}
		})
	}
	states := &countingReceiptState{receiptMemory: receiptMemory{}}
	h := &Handler{c: Config{Authorizer: &testAuth{}, ProviderStates: states}}
	scope := scopedAdapterState{h: h, p: Principal{OrganizationID: "o", UserID: "u", KeyID: "k", MembershipGeneration: 1}, t: Target{Provider: "fixture", Wire: Responses, Model: "m"}, c: Credential{AccountID: "a", Secret: "token"}}
	ctx := context.WithValue(context.Background(), replayScopeKey{}, scope)
	body := object{"metadata": metadata, "output": []any{object{"type": "thinking", "thinking": "reason", "signature": "opaque"}}}
	if err := h.validateReplay(ctx, body, scope); err == nil {
		t.Fatal("unissued nested signature accepted")
	}
	if err := h.CaptureReplayReceipts(ctx, body); err != nil {
		t.Fatal(err)
	}
	if states.saves.Load() != 1 {
		t.Fatalf("receipt saves = %d, want only the signed field", states.saves.Load())
	}
	if err := h.validateReplay(ctx, body, scope); err != nil {
		t.Fatal(err)
	}
	other := scope
	other.c.AccountID = "different"
	if err := h.validateReplay(ctx, body, other); err == nil {
		t.Fatal("signed field accepted under another account")
	}
}
