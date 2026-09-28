package grok

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type NativeMode string
type ModePhase string

const (
	NativeDefaultMode NativeMode = "default"
	NativePlanMode    NativeMode = "plan"
	ClaimMode         ModePhase  = "claim-mode"
	BindMode          ModePhase  = "bind-mode"
)

type modeParams struct {
	Session domain.ID  `json:"sessionId"`
	Mode    NativeMode `json:"modeId"`
}

// ModeClaim is the original pre-input mutation, not authority to restore or
// change a running session. Its bound phase retains independent native mode
// evidence after the exact RPC acknowledgment; neither alone permits input.
type ModeClaim struct {
	Phase            ModePhase  `json:"phase"`
	OwnerID          domain.ID  `json:"owner_id"`
	RequestID        domain.ID  `json:"request_id"`
	ProductSessionID domain.ID  `json:"product_session_id"`
	NativeSessionID  domain.ID  `json:"native_session_id"`
	Mode             NativeMode `json:"mode"`
	BodyDigest       string     `json:"body_digest"`
	EventID          string     `json:"event_id,omitempty"`
	TimestampMS      uint64     `json:"timestamp_ms,omitempty"`
}

func (c ModeClaim) Validate() error {
	seen := map[domain.ID]bool{}
	for _, id := range []domain.ID{c.OwnerID, c.RequestID, c.ProductSessionID, c.NativeSessionID} {
		if id.Validate() != nil || seen[id] {
			return apiConfigurationError()
		}
		seen[id] = true
	}
	body, _ := json.Marshal(modeParams{c.NativeSessionID, c.Mode})
	if c.Mode != NativePlanMode || c.BodyDigest != fileDigest(body) {
		return apiConfigurationError()
	}
	if c.Phase == ClaimMode && c.EventID == "" && c.TimestampMS == 0 {
		return nil
	}
	if c.Phase == BindMode && c.TimestampMS != 0 && c.TimestampMS <= 253402300799999 {
		if _, err := eventIndex(c.EventID, c.NativeSessionID); err == nil {
			return nil
		}
	}
	return apiConfigurationError()
}

type modeObservation struct {
	Session domain.ID `json:"sessionId"`
	Update  struct {
		Kind string     `json:"sessionUpdate"`
		Mode NativeMode `json:"currentModeId"`
	} `json:"update"`
	Meta struct {
		Event     string `json:"eventId"`
		Timestamp uint64 `json:"agentTimestampMs"`
	} `json:"_meta"`
}

func parseModeObservation(event nativewire.Event, session domain.ID) (modeObservation, error) {
	var observation modeObservation
	if event.Kind != nativewire.Notification || event.Method != "session/update" || event.EmittedAtMS != nil || session.Validate() != nil || decode(event.Params, &observation) != nil || observation.Session != session || observation.Update.Kind != "current_mode_update" || observation.Update.Mode != NativeDefaultMode && observation.Update.Mode != NativePlanMode || observation.Meta.Timestamp == 0 || observation.Meta.Timestamp > 253402300799999 {
		return observation, incompatible()
	}
	if _, err := eventIndex(observation.Meta.Event, session); err != nil {
		return observation, err
	}
	return observation, nil
}

func (a *apiConnection) SelectPlan(ctx context.Context, request domain.ID, record func(context.Context, ModeClaim) error) (binding ModeClaim, returned error) {
	if request.Validate() != nil || record == nil || a.profile.mode != domain.PlanMode {
		return binding, apiConfigurationError()
	}
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return binding, domain.SafeError(ctx.Err())
	}
	if !a.ready || a.modeStarted || a.inputStarted || request == a.product || request == a.creationRequest || request == a.session || request == a.inspection.OwnerID {
		return binding, sessionUncertain()
	}
	life, cancel := context.WithTimeout(ctx, 15*time.Second)
	watchStop, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-a.wire.Done():
			cancel()
		case <-watchStop:
		}
	}()
	stage := "inspection"
	defer func() {
		cancel()
		close(watchStop)
		<-watchDone
		if returned != nil && a.modeStarted {
			_ = a.Close()
			returned = sessionUncertain()
		}
		if logger := a.inspection.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "Grok Build initial mode selection failed", "owner_id", a.inspection.OwnerID, "request_id", request, "stage", stage, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "Grok Build initial mode bound", "owner_id", a.inspection.OwnerID, "request_id", request, "mode", NativePlanMode)
			}
		}
	}()
	if err := a.profile.checkInitialized(); err != nil {
		return binding, err
	}
	if err := inspectProfile(life, a.inspection, a.profile); err != nil {
		return binding, err
	}
	if err := a.profile.checkInitialized(); err != nil {
		return binding, err
	}
	params := modeParams{a.session, NativePlanMode}
	body, _ := json.Marshal(params)
	claim := ModeClaim{Phase: ClaimMode, OwnerID: a.inspection.OwnerID, RequestID: request, ProductSessionID: a.product, NativeSessionID: a.session, Mode: NativePlanMode, BodyDigest: fileDigest(body)}
	if claim.Validate() != nil {
		return binding, apiConfigurationError()
	}
	a.modeStarted, a.modeRequest = true, request
	stage = "claim"
	if err := record(life, claim); err != nil {
		return binding, sessionUncertain()
	}
	stage = "native-acknowledgment"
	response, err := a.wire.Call(life, request, "session/set_mode", params)
	if err != nil || response.ErrorCode != nil || decode(response.Result, &struct{}{}) != nil {
		return binding, sessionUncertain()
	}
	stage = "native-mode"
	observed, err := a.observeInitialPlan(life)
	if err != nil {
		return binding, incompatible()
	}
	claim.Phase, claim.EventID, claim.TimestampMS = BindMode, observed.Meta.Event, observed.Meta.Timestamp
	stage = "binding"
	if claim.Validate() != nil || record(life, claim) != nil || life.Err() != nil {
		return binding, sessionUncertain()
	}
	a.modeBinding = &claim
	return claim, nil
}

func (a *apiConnection) observeInitialPlan(ctx context.Context) (modeObservation, error) {
	var last uint64
	seen, size := false, 0
	for count := 0; count < 128; count++ {
		event, err := a.wire.Next(ctx)
		if err != nil {
			return modeObservation{}, err
		}
		size += len(event.Params)
		if size > nativewire.MaxFrame || event.Kind != nativewire.Notification || event.Method != "session/update" || event.EmittedAtMS != nil {
			return modeObservation{}, incompatible()
		}
		var variant struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		}
		if json.Unmarshal(event.Params, &variant) != nil {
			return modeObservation{}, incompatible()
		}
		if variant.Update.Kind == "current_mode_update" {
			observed, err := parseModeObservation(event, a.session)
			index, indexErr := eventIndex(observed.Meta.Event, a.session)
			if err != nil || indexErr != nil || observed.Update.Mode != NativePlanMode || seen && index <= last {
				return modeObservation{}, incompatible()
			}
			return observed, nil
		}
		// Native session creation can leave its original command inventory in
		// the event queue. Validate and discard it without granting commands;
		// no input, prompt or tool may precede the original mode binding.
		metadata, err := parsePassiveObservation(event.Params, event.Method, a.session, "")
		index, indexErr := eventIndex(metadata.event, a.session)
		if err != nil || indexErr != nil || metadata.kind != commandMetadata || seen && index <= last {
			return modeObservation{}, incompatible()
		}
		last, seen = index, true
	}
	return modeObservation{}, domain.Fail(domain.ResourceExhausted, "Native initial mode observations reached their bound.", "Retain the original selection and reconcile without replay.")
}
