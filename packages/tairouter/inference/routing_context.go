// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type routingRequestKey struct{}
type routingDecisionKey struct{}
type routingBudgetKey struct{}
type routingRequest struct {
	h      *Handler
	p      Principal
	record *UsageRecord
	body   json.RawMessage
}

func RoutingRequestBody(ctx context.Context) json.RawMessage {
	r, _ := ctx.Value(routingRequestKey{}).(routingRequest)
	return append(json.RawMessage(nil), r.body...)
}
func RoutingDestinationPolicy(ctx context.Context) DestinationPolicy {
	r, _ := ctx.Value(routingRequestKey{}).(routingRequest)
	if r.h == nil {
		return DestinationPolicy{}
	}
	return r.h.c.Destinations
}

// RoutingDecisionContext binds preselection sends to the enclosing usage record.
// The router calls it only after determining the logical route's physical send cap.
func RoutingDecisionContext(ctx context.Context, maxSends int) context.Context {
	r, ok := ctx.Value(routingRequestKey{}).(routingRequest)
	if !ok || maxSends < 1 {
		return ctx
	}
	if existing, ok := ctx.Value(routingBudgetKey{}).(int); ok && existing < maxSends {
		maxSends = existing
	}
	ctx = context.WithValue(ctx, routingBudgetKey{}, maxSends)
	return requestSupportBudget(ctx, r.record, maxSends)
}

// RoutingModelInvocation must disable JEV selection in the ordinary model router.
func RoutingModelInvocation(ctx context.Context) bool {
	v, _ := ctx.Value(routingDecisionKey{}).(bool)
	return v
}
func InvokeRoutingModel(ctx context.Context, alias string, raw json.RawMessage) (string, error) {
	if RoutingModelInvocation(ctx) {
		return "", &Error{400, "recursive_routing_model", 0}
	}
	r, ok := ctx.Value(routingRequestKey{}).(routingRequest)
	if !ok {
		return "", ErrUnavailable
	}
	if cap, ok := ctx.Value(routingBudgetKey{}).(int); !ok || cap < 1 {
		return "", &Error{503, "routing_budget_required", 0}
	}
	body, e := decode(raw)
	if e != nil {
		return "", e
	}
	body["model"] = alias
	body["stream"] = false
	body["store"] = false
	saved := *r.record
	buffer := &compactBuffer{header: make(map[string][]string)}
	ctx = context.WithValue(ctx, routingDecisionKey{}, true)
	e = r.h.execute(ctx, buffer, r.p, Responses, Generate, body, r.record)
	decisionRecord := *r.record
	decisionRecord.RequestID = newID()
	decisionRecord.AccountingRole = UsageAccountingNestedOperation
	decisionRecord.OperationID = decisionRecord.RequestID
	decisionRecord.Sends -= saved.Sends
	decisionRecord.UnmeasuredAttempts -= saved.UnmeasuredAttempts
	decisionRecord.IntermediateUsage = append([]Usage(nil), r.record.IntermediateUsage[len(saved.IntermediateUsage):]...)
	r.record.IntermediateUsage = saved.IntermediateUsage
	if e == nil {
		decisionRecord.Outcome = OutcomeSuccess
	} else {
		decisionRecord.Outcome = OutcomeGatewayError
		decisionRecord.Status = publicError(e).Status
	}
	decisionRecord.Canceled = ctx.Err() != nil
	if decisionRecord.Canceled {
		decisionRecord.Outcome = OutcomeCanceled
	}
	aggregateIntermediateUsage(&decisionRecord)
	// A decision model can have a different price/model/account than the final
	// target. Persist its measured counters separately instead of repricing them
	// at the selected model's rate. The shared parent send counter is retained.
	if decisionRecord.Sends > 0 {
		accountingCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := r.h.c.Usage.Record(accountingCtx, r.p, decisionRecord); err != nil {
			r.h.event(ctx, "routing_usage_write_failed")
		}
		stop()
	}
	r.record.Provider = saved.Provider
	r.record.Model = saved.Model
	r.record.AccountID = saved.AccountID
	r.record.ResponseID = saved.ResponseID
	r.record.Status = saved.Status
	r.record.Outcome = saved.Outcome
	r.record.ServiceTier = saved.ServiceTier
	r.record.Usage = saved.Usage
	if e != nil {
		return "", e
	}
	response, e := decode(buffer.body.Bytes())
	if e != nil {
		return "", e
	}
	var text strings.Builder
	for _, item := range arr(response["output"]) {
		for _, part := range arr(obj(item)["content"]) {
			if obj(part)["type"] == "output_text" {
				text.WriteString(str(obj(part)["text"]))
			}
		}
	}
	if text.Len() > 16<<10 {
		return "", &Error{502, "routing_decision_too_large", 0}
	}
	return text.String(), nil
}
