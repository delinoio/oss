package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type ClosurePhase string

const (
	ClaimClosure ClosurePhase = "claim-closure"
	BindClosure  ClosurePhase = "bind-closure"
)

type ClosureClaim struct {
	Phase            ClosurePhase `json:"phase"`
	RequestID        domain.ID    `json:"request_id"`
	ProductSessionID domain.ID    `json:"product_session_id"`
	NativeSessionID  domain.ID    `json:"native_session_id"`
	NativePromptID   string       `json:"native_prompt_id"`
	BodyDigest       string       `json:"body_digest"`
}

type closeParams struct {
	Session domain.ID `json:"sessionId"`
}

func ClosureClaimDigest(session domain.ID) (string, error) {
	if session.Validate() != nil {
		return "", apiConfigurationError()
	}
	raw, _ := json.Marshal(closeParams{Session: session})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (c ClosureClaim) Validate() error {
	digest, err := ClosureClaimDigest(c.NativeSessionID)
	if err != nil || c.RequestID.Validate() != nil || c.ProductSessionID.Validate() != nil || c.RequestID == c.ProductSessionID || !nativeUUID(c.NativePromptID, 4) || c.BodyDigest != digest || (c.Phase != ClaimClosure && c.Phase != BindClosure) {
		return apiConfigurationError()
	}
	return nil
}

type completedText struct {
	request       domain.ID
	prompt        string
	summary       string
	summarySeen   bool
	idle          bool
	bodyDigest    string
	chunks        [][32]byte
	lastChunk     [32]byte
	output        [32]byte
	terminal      [32]byte
	terminalFacts *TextTerminal
	retries       []RetryObservation
	usage         ModelUsage
	closed        domain.ID
	home          os.FileInfo
	history       *TextHistory
}

// TextClosure proves original native idle/summary, acknowledged session closure,
// native residency removal and joined owned process cleanup. It does not prove
// persisted conversation contents, continuation or durable completion reporting.
type TextClosure struct {
	RequestID       domain.ID `json:"request_id"`
	NativeSessionID domain.ID `json:"native_session_id"`
	NativePromptID  string    `json:"native_prompt_id"`
	Summary         string    `json:"-"`
}

func validateCloseResult(raw []byte) error {
	var result struct {
		Meta struct {
			Outcome string `json:"x.ai/closeOutcome"`
		} `json:"_meta"`
	}
	if decode(raw, &result) != nil || result.Meta.Outcome != "closed" {
		return incompatible()
	}
	return nil
}

func validateRemoval(raw []byte, session domain.ID) error {
	var result struct {
		Upserted json.RawMessage `json:"upserted"`
		Removed  []domain.ID     `json:"removed"`
	}
	if decode(raw, &result) != nil || !emptyArray(result.Upserted) || len(result.Removed) != 1 || result.Removed[0] != session {
		return incompatible()
	}
	return nil
}

// CloseText joins the native successful-text closure protocol before terminating
// its process. Killing immediately after last_turn_summary can lose the native
// asynchronous summary write; a delay or idle alone cannot prove its closure.
func (a *apiConnection) CloseText(ctx context.Context, request domain.ID, record func(context.Context, ClosureClaim) error) (result TextClosure, returned error) {
	if request.Validate() != nil || record == nil {
		return result, apiConfigurationError()
	}
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return result, domain.SafeError(ctx.Err())
	}
	completed := a.completedText
	if completed == nil || a.closureStarted || request == a.creationRequest || request == completed.request || request == a.product {
		return result, sessionUncertain()
	}
	a.closureStarted = true
	life, cancel := context.WithCancel(ctx)
	watchStop, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-a.wire.Done():
			cancel()
		case <-watchStop:
		}
	}()
	defer func() {
		cancel()
		if err := a.Close(); err != nil {
			returned = sessionUncertain()
		}
		if returned == nil {
			completed.closed = request
		}
		close(watchStop)
		<-watchDone
		if logger := a.inspection.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "Grok Build original text closure failed", "owner_id", a.inspection.OwnerID, "session_id", a.product, "request_id", request, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "Grok Build original text closure completed", "owner_id", a.inspection.OwnerID, "session_id", a.product, "request_id", request)
			}
		}
	}()
	for !completed.idle || !completed.summarySeen {
		event, err := a.wire.Next(life)
		if err != nil {
			return result, sessionUncertain()
		}
		if event.Kind != nativewire.Notification {
			return result, incompatible()
		}
		switch event.Method {
		case "_x.ai/sessions/changed":
			state, err := parseActivity(event.Params, a.session, a.workspace)
			if err != nil || state != idleActivity || completed.idle {
				return result, incompatible()
			}
			completed.idle = true
		case "_x.ai/session_notification":
			observation, err := parsePassiveObservation(event.Params, event.Method, a.session, completed.prompt)
			if err != nil || observation.kind != lastTurnMetadata || completed.summarySeen {
				return result, incompatible()
			}
			completed.summary, completed.summarySeen = observation.text, true
		default:
			return result, incompatible()
		}
	}
	if err := a.profile.checkInitialized(); err != nil {
		return result, err
	}
	digest, _ := ClosureClaimDigest(a.session)
	claim := ClosureClaim{Phase: ClaimClosure, RequestID: request, ProductSessionID: a.product, NativeSessionID: a.session, NativePromptID: completed.prompt, BodyDigest: digest}
	if err := record(life, claim); err != nil {
		return result, sessionUncertain()
	}
	response, err := a.wire.Call(life, request, "session/close", closeParams{Session: a.session})
	if err != nil || response.ErrorCode != nil {
		return result, sessionUncertain()
	}
	if err := validateCloseResult(response.Result); err != nil {
		return result, err
	}
	event, err := a.wire.Next(life)
	if err != nil {
		return result, sessionUncertain()
	}
	if event.Kind != nativewire.Notification || event.Method != "_x.ai/sessions/changed" || validateRemoval(event.Params, a.session) != nil {
		return result, incompatible()
	}
	claim.Phase = BindClosure
	if err := record(life, claim); err != nil {
		return result, sessionUncertain()
	}
	return TextClosure{RequestID: request, NativeSessionID: a.session, NativePromptID: completed.prompt, Summary: completed.summary}, nil
}
