// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (m *management) operator(ctx context.Context) (core.Identity, error) {
	i := identity(ctx)
	if !m.auth.IsOperator(i) {
		return core.Identity{}, rpcError(store.ErrForbidden)
	}
	return core.Identity{Issuer: i.Issuer, Subject: i.Subject}, nil
}
func settingsMessage(s store.Settings) *v1.InstanceSettings {
	out := &v1.InstanceSettings{RequestLimits: &v1.RequestLimits{RequestsPerMinute: uint32(s.RPM), ConcurrentRequests: uint32(s.Concurrency)}, Retention: &v1.RetentionSettings{RequestDays: uint32(s.UsageDays), AggregateDays: uint32(s.AggregateDays), AuditDays: uint32(s.AuditDays)}}
	for _, rule := range s.DestinationRules {
		out.AllowedDestinations = append(out.AllowedDestinations, &v1.AllowedDestination{Id: rule.ID, Origin: rule.Origin, AllowedCidrs: append([]string(nil), rule.AllowedCIDRs...)})
	}
	out.Revision = strconv.FormatInt(s.Revision, 10)
	return out
}
func (m *management) GetInstanceSettings(ctx context.Context, r *connect.Request[v1.GetInstanceSettingsRequest]) (*connect.Response[v1.GetInstanceSettingsResponse], error) {
	if _, e := m.operator(ctx); e != nil {
		return nil, e
	}
	s, e := m.store.Settings(ctx)
	return connect.NewResponse(&v1.GetInstanceSettingsResponse{Settings: settingsMessage(s)}), rpcError(e)
}
func (m *management) UpdateInstanceSettings(ctx context.Context, r *connect.Request[v1.UpdateInstanceSettingsRequest]) (*connect.Response[v1.UpdateInstanceSettingsResponse], error) {
	i, e := m.operator(ctx)
	if e != nil {
		return nil, e
	}
	q := r.Msg.Settings
	if q == nil || q.RequestLimits == nil || q.Retention == nil {
		return nil, invalid("complete settings required")
	}
	old, e := m.store.Settings(ctx)
	if e != nil {
		return nil, rpcError(e)
	}
	revision, e := settingsRevision(r.Msg.ExpectedRevision)
	if e != nil {
		return nil, e
	}
	next := old
	next.RPM = int(q.RequestLimits.RequestsPerMinute)
	next.Concurrency = int(q.RequestLimits.ConcurrentRequests)
	next.UsageDays = int(q.Retention.RequestDays)
	next.AggregateDays = int(q.Retention.AggregateDays)
	next.AuditDays = int(q.Retention.AuditDays)
	if next.RPM < 1 || next.RPM > 6000 || next.Concurrency < 1 || next.Concurrency > 100 || next.UsageDays < 1 || next.UsageDays > 90 || next.AggregateDays < 30 || next.AggregateDays > 730 || next.AuditDays < 30 || next.AuditDays > 365 {
		return nil, invalid("settings values outside supported bounds")
	}
	// A non-nil empty typed list clears every rule; the legacy origin projection
	// must not resurrect settings removed by this request.
	next.Destinations = nil
	next.DestinationRules = make([]store.AllowedDestination, 0, len(q.AllowedDestinations))
	for _, d := range q.AllowedDestinations {
		if d == nil {
			return nil, invalid("invalid allowed destination")
		}
		next.DestinationRules = append(next.DestinationRules, store.AllowedDestination{ID: d.Id, Origin: d.Origin, AllowedCIDRs: append([]string(nil), d.AllowedCidrs...)})
	}
	next.DestinationRules, e = core.NormalizeDestinationRules(next.DestinationRules)
	if e != nil {
		return nil, invalid("invalid destination origin, CIDR, or duplicate origin")
	}
	saved, e := m.store.UpdateSettingsCAS(ctx, i, revision, next)
	if e != nil {
		return nil, settingsError(e)
	}
	return connect.NewResponse(&v1.UpdateInstanceSettingsResponse{Settings: settingsMessage(saved)}), nil
}
func (m *management) GetInstanceHealth(ctx context.Context, r *connect.Request[v1.GetInstanceHealthRequest]) (*connect.Response[v1.GetInstanceHealthResponse], error) {
	i, err := m.operator(ctx)
	if err != nil {
		return nil, err
	}
	out := &v1.InstanceHealth{Service: v1.HealthState_HEALTH_STATE_HEALTHY, Database: v1.HealthState_HEALTH_STATE_HEALTHY}
	health, err := m.store.OperationalHealth(ctx, i)
	if err != nil {
		out.Database = v1.HealthState_HEALTH_STATE_UNAVAILABLE
		out.UsageRecording = v1.HealthState_HEALTH_STATE_UNAVAILABLE
	} else {
		if health.Cleanup.LastSuccess != nil {
			out.LastCleanupSuccessAt = timestamp(*health.Cleanup.LastSuccess)
		}
		out.ConsecutiveCleanupFailures = uint32(min(max(health.Cleanup.ConsecutiveFailures, 0), int64(1<<32-1)))
		out.UsageRecording = jobHealth(health.UsageRecording)
		if health.Cleanup.ConsecutiveFailures >= 3 || health.Cleanup.LastSuccess != nil && time.Since(*health.Cleanup.LastSuccess) > 15*time.Minute {
			out.Service = v1.HealthState_HEALTH_STATE_DEGRADED
		}
	}
	return connect.NewResponse(&v1.GetInstanceHealthResponse{Health: out}), nil
}
func jobHealth(h store.JobHealth) v1.HealthState {
	if h.ConsecutiveFailures > 0 {
		return v1.HealthState_HEALTH_STATE_DEGRADED
	}
	if h.LastSuccess == nil {
		return v1.HealthState_HEALTH_STATE_UNSPECIFIED
	}
	return v1.HealthState_HEALTH_STATE_HEALTHY
}
func unsigned(v *int64) *uint64 {
	if v == nil || *v < 0 {
		return nil
	}
	n := uint64(*v)
	return &n
}
func summary(a store.Aggregate) *v1.UsageSummary {
	in, out := uint64(a.InputTokens), uint64(a.OutputTokens)
	outcome := &v1.UsageSummary{RequestCount: uint64(a.Requests), KnownUsage: &v1.TokenUsage{InputTokens: &in, OutputTokens: &out}, KnownCosts: []*v1.Money{{Currency: "USD", DecimalAmount: a.Cost}}, MissingUsageCount: uint64(a.MissingUsage), MissingCostCount: uint64(a.MissingCost), MissingPriceCount: uint64(a.MissingPrice), FailedRequestCount: uint64(a.FailedRequests)}
	if a.KnownInput == 0 {
		outcome.KnownUsage.InputTokens = nil
	}
	if a.KnownOutput == 0 {
		outcome.KnownUsage.OutputTokens = nil
	}
	if a.KnownCost == 0 {
		outcome.KnownCosts = nil
	}
	return outcome
}
func (m *management) GetInstanceUsage(ctx context.Context, r *connect.Request[v1.GetInstanceUsageRequest]) (*connect.Response[v1.GetInstanceUsageResponse], error) {
	i, e := m.operator(ctx)
	if e != nil {
		return nil, e
	}
	f, e := usageFilter(r.Msg.TimeRange, "", "", nil)
	if e != nil {
		return nil, e
	}
	a, e := m.store.QueryInstanceAggregate(ctx, i, f)
	return connect.NewResponse(&v1.GetInstanceUsageResponse{Summary: summary(a)}), usageError(e)
}
func (m *management) ListUsage(ctx context.Context, r *connect.Request[v1.ListUsageRequest]) (*connect.Response[v1.ListUsageResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	user := sc.UserID
	if r.Msg.UserId != nil {
		user = r.Msg.GetUserId()
	}
	f, e := usageFilter(r.Msg.TimeRange, user, r.Msg.GetModelId(), r.Msg.Page)
	if e != nil {
		return nil, e
	}
	if e = extendUsageFilter(&f, r.Msg.ProviderId, r.Msg.Outcome); e != nil {
		return nil, e
	}
	rows, next, e := m.store.QueryUsage(ctx, sc, f)
	if e != nil {
		return nil, usageError(e)
	}
	out := &v1.ListUsageResponse{Page: &v1.PageResponse{NextPageToken: next}}
	for _, u := range rows {
		record := &v1.UsageRecord{Outcome: storedUsageOutcome(u), AppliedPrice: appliedPrice(u), Id: u.ID, OrganizationId: u.OrganizationID, ModelId: u.Model, ProviderId: u.Provider, OccurredAt: timestamp(u.CreatedAt), Usage: &v1.TokenUsage{InputTokens: unsigned(u.InputTokens), OutputTokens: unsigned(u.OutputTokens)}, RequestId: u.ID}
		if u.UserID != "" {
			id := u.UserID
			record.UserId = &id
		}
		if u.Cost != nil {
			record.EstimatedCost = &v1.Money{Currency: "USD", DecimalAmount: *u.Cost}
		}
		if u.ErrorCode != "" {
			code := u.ErrorCode
			record.ErrorCode = &code
		}
		out.Records = append(out.Records, record)
	}
	return connect.NewResponse(out), nil
}
func (m *management) GetUsageSummary(ctx context.Context, r *connect.Request[v1.GetUsageSummaryRequest]) (*connect.Response[v1.GetUsageSummaryResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	user := sc.UserID
	if r.Msg.UserId != nil {
		user = r.Msg.GetUserId()
	}
	f, e := usageFilter(r.Msg.TimeRange, user, r.Msg.GetModelId(), nil)
	if e != nil {
		return nil, e
	}
	if e = extendUsageFilter(&f, r.Msg.ProviderId, r.Msg.Outcome); e != nil {
		return nil, e
	}
	a, e := m.store.QueryAggregate(ctx, sc, f)
	return connect.NewResponse(&v1.GetUsageSummaryResponse{Summary: summary(a)}), usageError(e)
}
func (m *management) ListDailyUsage(ctx context.Context, r *connect.Request[v1.ListDailyUsageRequest]) (*connect.Response[v1.ListDailyUsageResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	user := sc.UserID
	if r.Msg.UserId != nil {
		user = r.Msg.GetUserId()
	}
	f, e := usageFilter(r.Msg.TimeRange, user, r.Msg.GetModelId(), r.Msg.Page)
	if e != nil {
		return nil, e
	}
	if e = extendUsageFilter(&f, r.Msg.ProviderId, r.Msg.Outcome); e != nil {
		return nil, e
	}
	rows, next, e := m.store.QueryDailyUsagePage(ctx, sc, f)
	if e != nil {
		return nil, usageError(e)
	}
	out := &v1.ListDailyUsageResponse{Page: &v1.PageResponse{NextPageToken: next}}
	for _, d := range rows {
		row := &v1.DailyUsage{Date: d.Date.Format("2006-01-02"), ModelId: d.Model, Summary: summary(d.Aggregate)}
		if d.UserID != "" {
			id := d.UserID
			row.UserId = &id
		}
		out.Days = append(out.Days, row)
	}
	return connect.NewResponse(out), nil
}
func usageFilter(r *v1.TimeRange, user, model string, p *v1.PageRequest) (store.UsageFilter, error) {
	f := store.UsageFilter{UserID: user, Model: model, After: p.GetPageToken(), Limit: int(p.GetPageSize())}
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 || len(f.After) > 2048 {
		return f, invalid("invalid page")
	}
	if r != nil {
		if r.Start != nil {
			if r.Start.CheckValid() != nil {
				return f, invalid("invalid range start")
			}
			v := r.Start.AsTime()
			f.Start = &v
		}
		if r.End != nil {
			if r.End.CheckValid() != nil {
				return f, invalid("invalid range end")
			}
			v := r.End.AsTime()
			f.End = &v
		}
		if f.Start != nil && f.End != nil && !f.Start.Before(*f.End) {
			return f, invalid("range end must follow start")
		}
	}
	return f, nil
}
func auditMessage(a store.Audit) *v1.AuditRecord {
	action := strings.ReplaceAll(strings.ToUpper(a.Action), ".", "_")
	aliases := map[string]string{"MEMBERSHIP_ROLE_CHANGED": "MEMBER_ROLE_CHANGED", "MEMBERSHIP_REMOVED": "MEMBER_REMOVED", "MEMBERSHIP_LEFT": "MEMBER_LEFT", "PROVIDER_CONNECTION_COMPLETED": "PROVIDER_UPDATED", "PROVIDER_CONFIGURATION_CHANGED": "PROVIDER_UPDATED", "PROVIDER_CONFIGURATION_DELETED": "PROVIDER_DELETED", "ORGANIZATION_SETTINGS_CHANGED": "ORGANIZATION_UPDATED", "INSTANCE_SETTINGS_CHANGED": "INSTANCE_SETTINGS_UPDATED"}
	if v, ok := aliases[action]; ok {
		action = v
	}
	out := &v1.AuditRecord{Action: v1.AuditAction(v1.AuditAction_value["AUDIT_ACTION_"+action]), Outcome: auditOutcome(a.Outcome), Id: a.ID, OccurredAt: timestamp(a.CreatedAt), ResourceId: a.TargetID}
	if a.ActorID != "" {
		out.ActorUserId = &a.ActorID
	}
	return out
}
func (m *management) ListOrganizationAudit(ctx context.Context, r *connect.Request[v1.ListOrganizationAuditRequest]) (*connect.Response[v1.ListOrganizationAuditResponse], error) {
	sc, err := m.scope(ctx, r.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	f, err := auditFilter(r.Msg.TimeRange, r.Msg.Page, r.Msg.Action, r.Msg.ActorUserId, r.Msg.Outcome)
	if err != nil {
		return nil, err
	}
	rows, next, err := m.store.QueryAudits(ctx, sc, f)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &v1.ListOrganizationAuditResponse{Page: &v1.PageResponse{NextPageToken: next}}
	for _, a := range rows {
		out.Records = append(out.Records, auditMessage(a))
	}
	return connect.NewResponse(out), nil
}
func (m *management) ListInstanceAudit(ctx context.Context, r *connect.Request[v1.ListInstanceAuditRequest]) (*connect.Response[v1.ListInstanceAuditResponse], error) {
	i, err := m.operator(ctx)
	if err != nil {
		return nil, err
	}
	f, err := auditFilter(r.Msg.TimeRange, r.Msg.Page, r.Msg.Action, r.Msg.ActorUserId, r.Msg.Outcome)
	if err != nil {
		return nil, err
	}
	rows, next, err := m.store.QueryInstanceAudits(ctx, i, f)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &v1.ListInstanceAuditResponse{Page: &v1.PageResponse{NextPageToken: next}}
	for _, a := range rows {
		out.Records = append(out.Records, auditMessage(a))
	}
	return connect.NewResponse(out), nil
}
func auditFilter(r *v1.TimeRange, p *v1.PageRequest, action *v1.AuditAction, actor *string, outcome *v1.AuditOutcome) (store.AuditFilter, error) {
	base, err := usageFilter(r, "", "", p)
	f := store.AuditFilter{Start: base.Start, End: base.End, After: base.After, Limit: base.Limit}
	if err != nil {
		return f, err
	}
	if actor != nil {
		if *actor == "" {
			return f, invalid("actor filter cannot be empty")
		}
		f.ActorID = *actor
	}
	if action != nil {
		name, ok := v1.AuditAction_name[int32(*action)]
		if !ok || *action == v1.AuditAction_AUDIT_ACTION_UNSPECIFIED {
			return f, invalid("invalid audit action")
		}
		name = strings.TrimPrefix(name, "AUDIT_ACTION_")
		aliases := map[string]string{"MEMBER_ROLE_CHANGED": "membership.role_changed", "MEMBER_REMOVED": "membership.removed", "MEMBER_LEFT": "membership.left", "PROVIDER_UPDATED": "provider.configuration_changed", "PROVIDER_DELETED": "provider.configuration_deleted", "ORGANIZATION_UPDATED": "organization.settings_changed", "INSTANCE_SETTINGS_UPDATED": "instance.settings_changed", "PRICE_OVERRIDE_SET": "price.override_set", "PRICE_OVERRIDE_DELETED": "price.override_deleted"}
		f.Action = aliases[name]
		if f.Action == "" {
			parts := strings.SplitN(strings.ToLower(name), "_", 2)
			f.Action = strings.Join(parts, ".")
		}
	}
	if outcome != nil {
		switch *outcome {
		case v1.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED:
			f.Outcome = store.AuditUnknown
		case v1.AuditOutcome_AUDIT_OUTCOME_SUCCEEDED:
			f.Outcome = store.AuditSucceeded
		case v1.AuditOutcome_AUDIT_OUTCOME_FAILED:
			f.Outcome = store.AuditFailed
		case v1.AuditOutcome_AUDIT_OUTCOME_DENIED:
			f.Outcome = store.AuditDenied
		default:
			return f, invalid("invalid audit outcome")
		}
	}
	return f, nil
}

func (m *management) ListPriceOverrides(ctx context.Context, r *connect.Request[v1.ListPriceOverridesRequest]) (*connect.Response[v1.ListPriceOverridesResponse], error) {
	if _, err := m.operator(ctx); err != nil {
		return nil, err
	}
	s, err := m.store.Settings(ctx)
	if err != nil {
		return nil, rpcError(err)
	}
	entries, err := storedPriceOverrides(s.Prices)
	if err != nil {
		return nil, rpcError(err)
	}
	limit := int(r.Msg.Page.GetPageSize())
	if limit == 0 {
		limit = 100
	}
	if limit > 1000 {
		return nil, invalid("invalid page size")
	}
	revision := strconv.FormatInt(s.Revision, 10)
	after := ""
	if token := r.Msg.Page.GetPageToken(); token != "" {
		if len(token) > 512 {
			return nil, invalid("invalid price page token")
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		parts := strings.SplitN(string(raw), ":", 2)
		if err != nil || len(parts) != 2 || parts[0] != revision {
			return nil, invalid("price page revision changed or token invalid")
		}
		after = parts[1]
		if _, ok := entries[after]; !ok {
			return nil, invalid("invalid price page token")
		}
	}
	ids := make([]string, 0, len(entries))
	for id := range entries {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := &v1.ListPriceOverridesResponse{Revision: revision, Page: &v1.PageResponse{}}
	if len(ids) > limit {
		ids = ids[:limit]
		out.Page.NextPageToken = base64.RawURLEncoding.EncodeToString([]byte(revision + ":" + ids[len(ids)-1]))
	}
	for _, id := range ids {
		out.Overrides = append(out.Overrides, entries[id])
	}
	return connect.NewResponse(out), nil
}
func (m *management) SetPriceOverride(ctx context.Context, r *connect.Request[v1.SetPriceOverrideRequest]) (*connect.Response[v1.SetPriceOverrideResponse], error) {
	i, err := m.operator(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := settingsRevision(r.Msg.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	value := &v1.PriceOverride{ProviderId: r.Msg.ProviderId, ModelId: r.Msg.ModelId, ServiceTier: r.Msg.ServiceTier, Rates: r.Msg.Rates, UpdatedAt: timestamp(time.Now())}
	value.ServiceTier = strings.ToLower(strings.TrimSpace(value.ServiceTier))
	if value.ServiceTier == "fast" {
		value.ServiceTier = "priority"
	}
	if err = validatePriceOverride(value); err != nil {
		return nil, err
	}
	settings, err := m.store.Settings(ctx)
	if err != nil {
		return nil, rpcError(err)
	}
	if settings.Revision != revision {
		return nil, settingsError(store.ErrConflict)
	}
	entries, err := storedPriceOverrides(settings.Prices)
	if err != nil {
		return nil, rpcError(err)
	}
	value.Id = core.ID()
	for _, entry := range entries {
		if entry.ProviderId == value.ProviderId && entry.ModelId == value.ModelId && entry.ServiceTier == value.ServiceTier {
			value.Id = entry.Id
			break
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, rpcError(err)
	}
	saved, err := m.store.PutPriceOverride(ctx, i, revision, value.Id, raw)
	if err != nil {
		return nil, settingsError(err)
	}
	return connect.NewResponse(&v1.SetPriceOverrideResponse{Override: value, Revision: strconv.FormatInt(saved.Revision, 10)}), nil
}
func (m *management) DeletePriceOverride(ctx context.Context, r *connect.Request[v1.DeletePriceOverrideRequest]) (*connect.Response[v1.DeletePriceOverrideResponse], error) {
	i, err := m.operator(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := settingsRevision(r.Msg.ExpectedRevision)
	if err != nil {
		return nil, err
	}
	if r.Msg.OverrideId == "" {
		return nil, invalid("override ID required")
	}
	saved, err := m.store.DeletePriceOverride(ctx, i, revision, r.Msg.OverrideId)
	if err != nil {
		return nil, settingsError(err)
	}
	return connect.NewResponse(&v1.DeletePriceOverrideResponse{Revision: strconv.FormatInt(saved.Revision, 10)}), nil
}
func settingsError(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return connect.NewError(connect.CodeAborted, errors.New("settings revision changed"))
	}
	return rpcError(err)
}

// A missing revision cannot authorize an unconditional replacement of concurrent edits.
func settingsRevision(raw string) (int64, error) {
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != raw {
		return 0, invalid("valid expected revision required")
	}
	return revision, nil
}
