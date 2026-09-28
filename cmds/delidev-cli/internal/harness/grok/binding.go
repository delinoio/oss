package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// SessionBinding is a copy of the already validated original native creation
// and optional initial-mode binding. It grants no input or publication lease.
type SessionBinding struct {
	OwnerID             domain.ID
	ProductSessionID    domain.ID
	CreationRequestID   domain.ID
	NativeSessionID     domain.ID
	ConfigurationDigest string
	InstructionsDigest  string
	Model               string
	ContextTokens       uint64
	Mode                NativeMode
	ModeBinding         *ModeClaim
}

func (a *OwnedAPI) SessionBinding(ctx context.Context) (SessionBinding, error) {
	if a == nil || a.connection == nil {
		return SessionBinding{}, sessionUncertain()
	}
	return a.connection.sessionBinding(ctx)
}

func (a *apiConnection) sessionBinding(ctx context.Context) (SessionBinding, error) {
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return SessionBinding{}, domain.SafeError(ctx.Err())
	}
	if !a.ready || a.inputStarted || ctx.Err() != nil || a.profile.checkInitialized() != nil {
		return SessionBinding{}, sessionUncertain()
	}
	select {
	case <-a.wire.Done():
		return SessionBinding{}, sessionUncertain()
	default:
	}
	mode := NativeDefaultMode
	var bound *ModeClaim
	if a.profile.mode == domain.PlanMode {
		if !a.modeStarted || a.modeBinding == nil || a.modeBinding.Validate() != nil || a.modeBinding.NativeSessionID != a.session || a.modeBinding.Phase != BindMode {
			return SessionBinding{}, sessionUncertain()
		}
		copy := *a.modeBinding
		bound, mode = &copy, NativePlanMode
	} else if a.modeStarted || a.modeBinding != nil {
		return SessionBinding{}, sessionUncertain()
	}
	digest := sha256.Sum256(a.profile.configuration)
	return SessionBinding{OwnerID: a.inspection.OwnerID, ProductSessionID: a.product, CreationRequestID: a.creationRequest, NativeSessionID: a.session, ConfigurationDigest: hex.EncodeToString(digest[:]), InstructionsDigest: a.profile.instructions.digest(), Model: a.profile.model, ContextTokens: a.profile.contextTokens, Mode: mode, ModeBinding: bound}, nil
}
