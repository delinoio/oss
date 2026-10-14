// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// grokDiagnosticPhases maps every domain phase to its wire enum member. The
// table test in grok_diagnostic_test.go asserts that no domain phase is missing.
var grokDiagnosticPhases = map[domain.GrokPhase]pb.GrokDiagnosticPhase{
	domain.GrokDiscovery: pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_DISCOVERY,
	domain.GrokVersion:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_VERSION,
	domain.GrokProfile:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_PROFILE,
	domain.GrokRuntime:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_RUNTIME,
	domain.GrokLogin:     pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN,
	domain.GrokRefresh:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH,
	domain.GrokLogout:    pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT,
	domain.GrokModels:    pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_MODELS,
	domain.GrokExecution: pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_EXECUTION,
	domain.GrokHistory:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_HISTORY,
	domain.GrokCleanup:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_CLEANUP,
}

// grokDiagnosticMessage returns only locally reconstructed, validated metadata.
// Invalid stored metadata is omitted rather than forwarded.
func grokDiagnosticMessage(d *domain.GrokDiagnostic) *pb.GrokDiagnostic {
	if d == nil || d.Validate() != nil {
		return nil
	}
	return &pb.GrokDiagnostic{DetectedVersion: d.DetectedVersion, SupportedVersion: d.SupportedVersion, Phase: grokDiagnosticPhases[d.Phase], Code: string(d.Code), Message: d.Message, Guidance: d.Guidance, CorrelationId: d.CorrelationID}
}

// grokAdmissionFailure classifies a refused Grok installation by the original
// phase. Discovery validity and the subscription version baseline are decided
// first: a release between the discovery baseline and the subscription
// baseline can retain a stored profile diagnostic from its probe, but admission
// must still report it as a version failure. Only an installation that is
// discovered and at or above the subscription baseline restores its stored
// failed-validation diagnostic unchanged, so the first native failure stays
// authoritative.
func grokAdmissionFailure(found *domain.Installation) error {
	if found == nil {
		return domain.WithGrokDiagnostic("", domain.GrokDiscovery, grokInstallationRequired())
	}
	switch {
	case found.State != domain.InstallationDetected || found.ResolvedPath == "" || found.ObservedAt == nil || found.ObservedAt.IsZero():
		return domain.WithGrokDiagnostic(found.Version, domain.GrokDiscovery, grokInstallationRequired())
	case !domain.GrokSubscriptionVersionAllowed(found.Version):
		return domain.GrokVersionFailure(found.Version)
	}
	if stored := found.Protocol; stored != nil && stored.GrokDiagnostic != nil && stored.GrokDiagnostic.DetectedVersion == found.Version && stored.GrokDiagnostic.Validate() == nil {
		return domain.RestoreGrokDiagnostic(*stored.GrokDiagnostic)
	}
	return domain.WithGrokDiagnostic(found.Version, domain.GrokProfile, grokInstallationRequired())
}

func grokInstallationRequired() *domain.Error {
	return domain.Fail(domain.Unsupported, "Grok subscription authentication requires a verified, supported Grok Build installation.", "Discover the installed Grok Build on the selected Runner Device and install the pinned supported release yourself.")
}

// grokSubscriptionProgress reports the original Grok operation's state and its
// safe diagnostic. It never carries URLs, codes or identity, and the Codex
// diagnostic (7) and native diagnostic (9) fields stay unset.
func (s *Service) grokSubscriptionProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	response := &pb.GetSubscriptionProgressResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || string(st.ServerOperation.ID) != req.Msg.OperationId || st.ServerOperation.Actor != actor {
			return subscriptionDenied()
		}
		o := st.ServerOperation
		if err := subscriptionActorValid(tx, o.Actor); err != nil {
			return err
		}
		state := o.State
		if st.RecoveryRequired || o.Active() && o.Epoch != s.subscriptionServerEpoch() {
			state = domain.SubscriptionRecovery
		}
		if (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) && !o.ExpiresAt.After(time.Now().UTC()) {
			state = domain.SubscriptionExpired
		}
		response.State = loginState(state)
		response.Canceled = state == domain.SubscriptionCanceled || st.Pending != nil && st.Pending.ID == o.ID && st.Pending.Canceled
		response.GrokDiagnostic = grokDiagnosticMessage(o.GrokDiagnostic)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_progress_observed", "operation_id", req.Msg.OperationId, "state", response.State, "diagnostic_reported", response.GrokDiagnostic != nil)
	return connect.NewResponse(response), nil
}
