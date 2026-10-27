// SPDX-License-Identifier: Apache-2.0
package mimofree

import (
	"context"
	"errors"
	"net/http"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
)

func (a *Adapter) Execute(ctx context.Context, in inference.AdapterInput, exec inference.AdapterExecutor, store inference.AdapterStateStore) (out *http.Response, wire inference.Wire, resultErr error) {
	defer func() {
		if resultErr == nil && out != nil && out.StatusCode >= 200 && out.StatusCode < 300 {
			out = policy.WrapChatResponse(out, in.Target.Model, policy.FreeformTools(in.Body))
		}
	}()
	req, err := a.Build(ctx, in)
	if err != nil {
		return nil, inference.Chat, err
	}
	s, err := loadSession(ctx, store)
	if err != nil {
		return nil, inference.Chat, err
	}
	s, err = a.token(ctx, exec, store, s, "")
	if err != nil {
		return nil, inference.Chat, err
	}
	req.Headers.Set("Authorization", "Bearer "+s.JWT)
	req.Headers.Set("X-Session-Affinity", s.Affinity)
	resp, err := exec.Do(ctx, req)
	if err != nil {
		return nil, inference.Chat, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, inference.Chat, nil
	}
	recovery, ok := exec.(inference.AdapterRecoveryExecutor)
	if !ok {
		return resp, inference.Chat, nil
	}
	entered := false
	next, err := recovery.Recover(ctx, 2, func(admitted inference.AdapterExecutor) (*http.Response, error) {
		entered = true
		resp.Body.Close()
		// Only admitted recovery may invalidate a token. A denied replay leaves the
		// original response and state intact, as required by the pinned adapter.
		var e error
		s, e = a.token(ctx, admitted, store, s, s.JWT)
		if e != nil {
			return nil, e
		}
		req.Headers.Set("Authorization", "Bearer "+s.JWT)
		return admitted.Do(ctx, req)
	})
	if err != nil {
		var public *inference.Error
		if !entered && errors.As(err, &public) && public.Code == "request_send_budget_exhausted" {
			return resp, inference.Chat, nil
		}
		if !entered {
			resp.Body.Close()
		}
		return nil, inference.Chat, err
	}
	return next, inference.Chat, nil
}
