// SPDX-License-Identifier: Apache-2.0
// Refusal predicates adapted from opencodex; see LICENSE.upstream.
package commandcode

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"regexp"
	"slices"
	"strings"
)

var effortRefusal = regexp.MustCompile(`(?i)reasoning[_ -]?effort|unsupported effort|invalid effort`)

// ClassifyRefusal recognizes only verified, bounded structured provider refusals.
// It never uses upstream free text as a public error or a secondary classifier.
func ClassifyRefusal(status int, body []byte, oauth bool) *inference.Error {
	if len(body) > 8192 {
		return nil
	}
	var m object
	if json.Unmarshal(body, &m) != nil || m["success"] != false {
		return nil
	}
	e := obj(m["error"])
	message := str(e["message"])
	if status == 403 && e["code"] == "FORBIDDEN" && (message == "MODEL_NOT_IN_PLAN" || strings.HasPrefix(message, "MODEL_NOT_IN_PLAN:")) {
		return &inference.Error{Status: 400, Code: "model_not_in_plan"}
	}
	if oauth && status == 400 && e["code"] == "BAD_REQUEST" && strings.HasPrefix(message, "You have insufficient credits to make this request.") {
		return &inference.Error{Status: 429, Code: "insufficient_credits"}
	}
	return nil
}
func (*Adapter) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if err := ctx.Err(); err != nil {
		return inference.RecoveryDecision{}, err
	}
	e := ClassifyRefusal(f.Status, f.Body, in.Target.Auth == inference.OAuth)
	if e == nil {
		return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
	}
	action := inference.RecoveryStop
	if e.Status == 429 {
		action = inference.RecoveryRotate
	}
	return inference.RecoveryDecision{Action: action, Error: e}, nil
}
func (a *Adapter) RepairRequest(ctx context.Context, in inference.AdapterInput, r inference.AdapterRequest, f inference.HTTPFailure) (inference.AdapterRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return r, false, err
	}
	if f.Repairs > 0 || (f.Status != 400 && f.Status != 422) || len(f.Body) > 8192 || !effortRefusal.Match(f.Body) {
		return r, false, nil
	}
	if ClassifyRefusal(f.Status, f.Body, in.Target.Auth == inference.OAuth) != nil {
		return r, false, nil
	}
	var body object
	if json.Unmarshal(r.Body, &body) != nil {
		return r, false, nil
	}
	params := obj(body["params"])
	model, effort := str(params["model"]), str(params["reasoning_effort"])
	p, err := a.resolvePolicy(ctx, in)
	if err != nil {
		return r, false, err
	}
	if effort == "" {
		return r, false, nil
	}
	if _, ok := lookup(p.ReasoningEfforts, model); p.Authoritative && ok {
		return r, false, nil
	}
	// A refusal alone does not authorize a silent downgrade. The profile owner
	// must supply refreshed evidence that excludes the rejected shipped rung.
	refreshed, ok := lookup(p.RefreshedEfforts, model)
	if !ok {
		var err error
		refreshed, ok, err = a.refreshProfile(ctx, in, p, model, effort)
		if err != nil {
			return r, false, err
		}
	}
	if !ok || slices.Contains(refreshed, effort) {
		return r, false, nil
	}
	delete(params, "reasoning_effort")
	data, err := json.Marshal(body)
	if err != nil {
		return r, false, err
	}
	r.Body = data
	return r, true, nil
}
