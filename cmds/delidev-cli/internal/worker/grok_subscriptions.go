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
// the file is retired. Every filesystem operation on the home is anchored to the
// directory handle bound at materialization (see subscription.GrokHome), so a
// home swapped after binding can never receive or lose a credential through a
// path lookup. Nothing here logs a credential, path or identity value.

// materializeGrok opens the delivered envelope and writes auth.json into the
// bound home. A fresh login delivers no bundle and writes nothing, but the
// native login creates auth.json itself, so the home is validated and must hold
// no credential file or interrupted-write temporary before any login or refresh
// proceeds. The returned bundle holds the original identity for the Finish
// binding check; the caller clears it. The bound home is retained on the lease
// for Finish and released by finishGrok; a failed materialization releases it
// here.
func (l *managedSubscriptionLease) materializeGrok(grokHome string) (bundle subscription.GrokBundle, err error) {
	// The directory is bound (opened and validated through its handle) before
	// anything is written or accepted; every later step uses that handle only.
	home, err := subscription.BindGrokHome(grokHome)
	if err != nil {
		clear(l.response.Bundle)
		l.response.Bundle = nil
		return subscription.GrokBundle{}, err
	}
	return l.materializeGrokBound(home)
}

// materializeGrokBound is materializeGrok for a home the caller already bound.
// The lease takes ownership of home: it is retained for Finish and released by
// finishGrok, or released here when materialization fails.
func (l *managedSubscriptionLease) materializeGrokBound(home subscription.GrokHome) (bundle subscription.GrokBundle, err error) {
	raw := l.response.Bundle
	defer func() {
		clear(raw)
		l.response.Bundle = nil
	}()
	l.grokHome = home
	defer func() {
		if err != nil {
			_ = home.Close()
			l.grokHome = subscription.GrokHome{}
		}
	}()
	if len(raw) == 0 {
		if l.response.GenerationId != "" {
			return subscription.GrokBundle{}, subscription.GrokBundleInvalid()
		}
		// A leftover credential would otherwise be sealed as the result of this login.
		if present, err := home.AuthFilePresent(); err != nil || present {
			return subscription.GrokBundle{}, subscription.GrokBundleInvalid()
		}
		return subscription.GrokBundle{}, nil
	}
	opened, err := subscription.OpenGrokBundle(raw, l.credential.ServerID, l.account, domain.ID(l.response.GenerationId))
	if err != nil {
		return subscription.GrokBundle{}, err
	}
	if err := home.WriteAuthFile(opened); err != nil {
		opened.Clear()
		return subscription.GrokBundle{}, err
	}
	return opened, nil
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
// differs from the original also downgrades to an unsuccessful Finish. Reads and
// removals run through the held directory handle, so a swap that lands between
// a verification and the operation after it can neither redirect them into the
// substitute nor leave the final verification unaware of it.
func prepareGrokFinish(home subscription.GrokHome, server, account, generation domain.ID, action pb.SubscriptionAction, original subscription.GrokBundle, success bool) grokFinishOutcome {
	out := grokFinishOutcome{Success: success}
	if home.Verify() != nil {
		// The bound path no longer names the held directory, so nothing is read,
		// sealed or removed and cleanup is never confirmed; the original directory
		// keeps its file for recovery.
		return grokFinishOutcome{}
	}
	if success && action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT {
		sealed, bundle, err := home.SealFinish(server, account, generation, original)
		if err != nil {
			out.Success = false
		} else {
			out.Bundle = sealed
			bundle.Clear()
		}
	}
	out.Cleanup = home.RetireAuthFile() == nil
	if present, err := home.AuthFilePresent(); err != nil || present {
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
	// The bound home is released once this Finish has settled its credential.
	defer func() {
		_ = l.grokHome.Close()
		l.grokHome = subscription.GrokHome{}
	}()
	action := l.journal.Action
	outcome := prepareGrokFinish(l.grokHome, l.credential.ServerID, l.account, l.finishID, action, original, success)
	refresh := outcome.Success && action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	if l.logger != nil {
		l.logger.LogAttrs(context.Background(), slog.LevelInfo, "grok_subscription_finish", slog.String("account_id", string(l.account)), slog.String("lease_id", l.response.LeaseId), slog.String("action", action.String()), slog.Bool("success", outcome.Success), slog.Bool("cleanup_confirmed", outcome.Cleanup))
	}
	return l.finish(outcome.Bundle, outcome.Cleanup, refresh, outcome.Success)
}

// finishGrokExecution settles one Grok execute lease. attempted records whether
// the Worker began to materialize the credential: when it did not, no file was
// ever written and the cleanup is confirmed by construction. Otherwise the
// native file is read once (sealing a native refresh when the process was
// joined and kept the original identity) and retired through the bound home.
// An unjoined native process never confirms cleanup, because it could still
// rewrite the credential: the file is retired best-effort and the server keeps
// the lease fenced. The bound home is released here.
func (l *managedSubscriptionLease) finishGrokExecution(original subscription.GrokBundle, attempted, joined bool) error {
	defer original.Clear()
	defer clear(l.response.Bundle)
	defer func() {
		_ = l.grokHome.Close()
		l.grokHome = subscription.GrokHome{}
	}()
	outcome := grokFinishOutcome{Cleanup: true}
	if attempted {
		outcome = prepareGrokFinish(l.grokHome, l.credential.ServerID, l.account, l.finishID, l.journal.Action, original, joined)
		if !joined {
			clear(outcome.Bundle)
			outcome = grokFinishOutcome{}
		}
	}
	if l.logger != nil {
		l.logger.LogAttrs(context.Background(), slog.LevelInfo, "grok_subscription_execution_finish", slog.String("phase", "EXECUTION"), slog.String("step", "finish"), slog.String("account_id", string(l.account)), slog.String("lease_id", l.response.LeaseId), slog.Bool("credential_written", attempted), slog.Bool("process_joined", joined), slog.Bool("success", outcome.Success), slog.Bool("cleanup_confirmed", outcome.Cleanup))
	}
	return l.finish(outcome.Bundle, outcome.Cleanup, false, outcome.Success)
}
