// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"testing"
)

type modelOrdinalAuth struct {
	*testAuth
	ordinals []int
	refuse   bool
}

func (a *modelOrdinalAuth) Dispatch(ctx context.Context, p Principal, start func(context.Context) error) error {
	a.ordinals = append(a.ordinals, ModelDispatchAttempt(ctx))
	if a.refuse {
		a.refuse = false
		return ErrUnauthorized
	}
	return a.testAuth.Dispatch(ctx, p, start)
}
func TestModelDispatchOrdinalExcludesSupportAndRefusedWrites(t *testing.T) {
	h, s, auth, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fixture(Chat)) }, Chat)
	a := &modelOrdinalAuth{testAuth: auth}
	h.c.Authorizer = a
	rec := &UsageRecord{}
	x := &requestExecutor{h: h, target: Target{BaseURL: s.URL}, record: rec, maxSends: 8}
	for i, wire := range []Wire{"", Chat, Chat, "", Chat} {
		if i == 1 {
			a.refuse = true
		}
		x.credential.AccountID = string(rune('a' + i))
		r, e := x.Do(context.Background(), AdapterRequest{URL: s.URL, ResponseWire: wire})
		if i == 1 {
			if e == nil {
				t.Fatal("refused model was sent")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
	}
	if !reflect.DeepEqual(a.ordinals, []int{0, 1, 1, 0, 2}) || rec.Sends != 4 {
		t.Fatalf("ordinals=%v sends=%d", a.ordinals, rec.Sends)
	}
}
func TestDirectModelDispatchOrdinal(t *testing.T) {
	h, _, auth, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fixture(Chat)) }, Chat)
	a := &modelOrdinalAuth{testAuth: auth}
	h.c.Authorizer = a
	out := call(h, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if out.Code != 200 || !reflect.DeepEqual(a.ordinals, []int{1}) {
		t.Fatalf("%d %v", out.Code, a.ordinals)
	}
}
