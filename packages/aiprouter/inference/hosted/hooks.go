// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func (a *Adapter) TokenProjection(t inference.Target) inference.TokenProjection {
	if p, ok := a.inner.(inference.TokenProjector); ok {
		return p.TokenProjection(t)
	}
	return inference.TokenProjection{Text: true, Signature: true, Redacted: true}
}
func (a *Adapter) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if a.config.RecoveryPolicy != nil {
		return a.config.RecoveryPolicy.RecoverHTTP(ctx, in, f)
	}
	if p, ok := a.inner.(inference.HTTPRecoveryPolicy); ok {
		return p.RecoverHTTP(ctx, in, f)
	}
	return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
}
func (a *Adapter) RepairRequest(ctx context.Context, in inference.AdapterInput, r inference.AdapterRequest, f inference.HTTPFailure) (inference.AdapterRequest, bool, error) {
	if p, ok := a.inner.(inference.RequestRepairer); ok {
		return p.RepairRequest(ctx, in, r, f)
	}
	return r, false, nil
}

// repair owns the refusal body before public output. It permits at most two
// changed POSTs, each through the original executor's origin and physical gate.
func (a *Adapter) repair(ctx context.Context, in inference.AdapterInput, req inference.AdapterRequest, r *http.Response, e inference.AdapterExecutor) (*http.Response, inference.AdapterRequest, error) {
	if _, ok := a.inner.(inference.RequestRepairer); !ok {
		return r, req, nil
	}
	for attempt := 0; attempt < 2 && (r.StatusCode == http.StatusBadRequest || r.StatusCode == http.StatusUnprocessableEntity); attempt++ {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		refusalBody := r.Body
		stop := context.AfterFunc(readCtx, func() { refusalBody.Close() })
		body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		stop()
		r.Body.Close()
		expired := readCtx.Err()
		cancel()
		if expired != nil {
			return nil, req, expired
		}
		if err != nil {
			return nil, req, err
		}
		if len(body) > 1<<20 {
			return nil, req, fault(502, "hosted_refusal_limit")
		}
		updated, changed, err := a.RepairRequest(ctx, in, req, inference.HTTPFailure{Status: r.StatusCode, Header: r.Header.Clone(), Body: body, Attempt: attempt + 1, Repairs: attempt})
		if err != nil {
			return nil, req, err
		}
		if !changed {
			r.Body = io.NopCloser(bytes.NewReader(body))
			return r, req, nil
		}
		if updated.Method != "" && updated.Method != "POST" {
			return nil, req, inference.ErrDestination
		}
		if updated.ResponseWire != inference.Chat && updated.ResponseWire != inference.Responses && updated.ResponseWire != inference.Anthropic {
			return nil, req, inference.ErrUnsupported
		}
		if bytes.Equal(updated.Body, req.Body) && updated.URL == req.URL {
			return nil, req, fault(502, "hosted_unchanged_repair")
		}
		req = updated
		r, err = e.Do(ctx, req)
		if err != nil {
			return nil, req, err
		}
		if r == nil || r.Body == nil {
			return nil, req, fault(502, "hosted_invalid_response")
		}
	}
	return r, req, nil
}

// A successful HTTP response can request credential refresh through headers. It
// must reach the core classifier before proprietary Decode consumes the body.
type recoveryBody struct{ io.ReadCloser }

func (a *Adapter) preempt(ctx context.Context, in inference.AdapterInput, r *http.Response) (bool, error) {
	if r == nil || r.Body == nil {
		return false, fault(502, "hosted_invalid_response")
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return false, nil
	}
	decision, err := a.RecoverHTTP(ctx, in, inference.HTTPFailure{Status: r.StatusCode, Header: r.Header.Clone()})
	if err != nil {
		r.Body.Close()
		return false, err
	}
	if decision.Action != "" && decision.Action != inference.RecoveryDefault {
		r.Body = &recoveryBody{r.Body}
		return true, nil
	}
	return false, nil
}
