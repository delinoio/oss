// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Grok Worker custody of a taken credential. The server delivers the sealed
// subscription.GrokBundle envelope only through protected Take; the Worker opens
// it against its own server, the account and the delivered generation, writes
// the native $GROK_HOME/auth.json atomically with owner-only permission inside a
// private home, and returns a freshly sealed bundle only through Finish after
// the file is retired. Nothing here logs a credential, path or identity value.

// materializeGrok opens the delivered envelope and writes grokHome/auth.json.
// A fresh login delivers no bundle and writes nothing, but the native login
// creates auth.json itself, so the home is validated and must hold no credential
// file or interrupted-write temporary before any login or refresh proceeds. The
// returned bundle holds the original identity for the Finish binding check; the
// caller clears it.
func (l *managedSubscriptionLease) materializeGrok(grokHome string) (subscription.GrokBundle, error) {
	raw := l.response.Bundle
	defer func() {
		clear(raw)
		l.response.Bundle = nil
	}()
	// The identity of the home is bound before anything is written or accepted;
	// Finish retires and proves absence on that same directory only.
	home, err := subscription.BindGrokHome(grokHome)
	if err != nil {
		return subscription.GrokBundle{}, err
	}
	l.grokHome = home
	if len(raw) == 0 {
		if l.response.GenerationId != "" {
			return subscription.GrokBundle{}, subscription.GrokBundleInvalid()
		}
		// A leftover credential would otherwise be sealed as the result of this login.
		if present, err := subscription.GrokAuthFilePresent(grokHome); err != nil || present {
			return subscription.GrokBundle{}, subscription.GrokBundleInvalid()
		}
		return subscription.GrokBundle{}, nil
	}
	bundle, err := subscription.OpenGrokBundle(raw, l.credential.ServerID, l.account, domain.ID(l.response.GenerationId))
	if err != nil {
		return subscription.GrokBundle{}, err
	}
	if err := subscription.WriteGrokAuthFile(grokHome, bundle); err != nil {
		bundle.Clear()
		return subscription.GrokBundle{}, err
	}
	return bundle, nil
}

// grokFinishOutcome is the closed result the Worker reports to Finish.
type grokFinishOutcome struct {
	Bundle  []byte
	Cleanup bool
	Success bool
}

// prepareGrokFinish reads the native credential file once, seals the next
// generation when the operation succeeded, and then retires the file. Cleanup is
// confirmed only when the file and its interrupted-write temporaries are gone
// and the path still names the directory the credential was materialized in, so
// an unconfirmed retirement can never release the lease. The home is verified
// before anything is read or removed and again after the final absence check: a
// home that was renamed away or replaced (even by an empty directory, which
// would read as clean) downgrades to an unsuccessful, uncleaned Finish that
// carries no bundle, for logout as well. A failed read or an identity that
// differs from the original also downgrades to an unsuccessful Finish.
func prepareGrokFinish(home subscription.GrokHome, server, account, generation domain.ID, action pb.SubscriptionAction, original subscription.GrokBundle, success bool) grokFinishOutcome {
	out := grokFinishOutcome{Success: success}
	grokHome := home.Path()
	if home.Verify() != nil {
		return grokFinishOutcome{}
	}
	if success && action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT {
		sealed, bundle, err := subscription.SealGrokFinish(grokHome, server, account, generation, original)
		if err != nil {
			out.Success = false
		} else {
			out.Bundle = sealed
			bundle.Clear()
		}
	}
	out.Cleanup = subscription.RetireGrokAuthFile(grokHome) == nil
	if present, err := subscription.GrokAuthFilePresent(grokHome); err != nil || present {
		out.Cleanup = false
	}
	if home.Verify() != nil {
		out.Cleanup = false
	}
	if !out.Cleanup {
		// Never hand a credential back with unconfirmed local cleanup.
		clear(out.Bundle)
		out.Bundle = nil
	}
	return out
}

// finishGrok settles one Grok lease: it prepares the outcome from the native
// file and reports it through the generic protected Finish. refresh is true only
// for a refresh whose native process already exited.
func (l *managedSubscriptionLease) finishGrok(original subscription.GrokBundle, success bool) error {
	defer original.Clear()
	action := l.journal.Action
	outcome := prepareGrokFinish(l.grokHome, l.credential.ServerID, l.account, l.finishID, action, original, success)
	refresh := outcome.Success && action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	if l.logger != nil {
		l.logger.LogAttrs(context.Background(), slog.LevelInfo, "grok_subscription_finish", slog.String("account_id", string(l.account)), slog.String("lease_id", l.response.LeaseId), slog.String("action", action.String()), slog.Bool("success", outcome.Success), slog.Bool("cleanup_confirmed", outcome.Cleanup))
	}
	return l.finish(outcome.Bundle, outcome.Cleanup, refresh, outcome.Success)
}
