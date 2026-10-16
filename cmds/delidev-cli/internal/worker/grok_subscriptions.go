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
// the file is retired. An execution exit follows the same protocol (see
// sealGrokExecutionExit): the latest auth.json is verified against the original
// identity, a changed credential is returned through Finish, and auth.json and
// auth.json.lock are retired before the lease may be released. Every filesystem operation on the home is anchored to the
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
	raw := l.response.Bundle
	defer func() {
		clear(raw)
		l.response.Bundle = nil
	}()
	// The directory is bound (opened and validated through its handle) before
	// anything is written or accepted; every later step uses that handle only.
	home, err := subscription.BindGrokHome(grokHome)
	if err != nil {
		return subscription.GrokBundle{}, err
	}
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
	// Refreshed is true only for an execution exit whose native process rewrote
	// auth.json with a verified successor of the original credential: Bundle then
	// carries that successor and Finish reports it as a confirmed refresh.
	Refreshed bool
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
	switch {
	case action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE:
		// The native process may have refreshed the credential itself, and a
		// rotated refresh token that is not returned is lost, so the exit reads
		// the latest file whatever the process outcome was.
		sealed, refreshed, ok := sealGrokExecutionExit(home, server, account, generation, original)
		out.Success, out.Bundle, out.Refreshed = ok, sealed, refreshed
	case success && action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT:
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
		out.Bundle, out.Refreshed = nil, false
	}
	return out
}

// sealGrokExecutionExit is the credential half of a Grok execution exit. It
// reads the latest auth.json once and verifies it against the original bundle:
//
//   - unreadable or invalid file, or another identity: not ok, nothing returned;
//   - the original credential, unchanged: ok, no bundle (nothing to replace);
//   - a changed credential: it must pass subscription.GrokRefreshed (same
//     identity, later expiry, changed key or refresh token), and is then sealed
//     for the generation the Finish creates and reported as refreshed.
//
// A result that is not ok keeps the lease: the Finish carries no success, so the
// server fences the account for recovery instead of releasing it. The caller
// retires auth.json and auth.json.lock afterwards and releases nothing before
// that cleanup is confirmed.
func sealGrokExecutionExit(home subscription.GrokHome, server, account, generation domain.ID, original subscription.GrokBundle) (sealed []byte, refreshed, ok bool) {
	if original.Identity().UserID == "" {
		return nil, false, false
	}
	latest, err := home.ReadAuthFile()
	if err != nil {
		return nil, false, false
	}
	defer latest.Clear()
	if latest.IdentityKey() != original.IdentityKey() {
		return nil, false, false
	}
	if latest.Equal(original) {
		return nil, false, true
	}
	if subscription.GrokRefreshed(original, latest) != nil {
		return nil, false, false
	}
	sealed, err = subscription.SealGrokBundle(server, account, generation, latest)
	if err != nil {
		return nil, false, false
	}
	return sealed, true, true
}

// grokCleanupRetry is what a Grok lease keeps after a Finish whose private home
// cleanup was not confirmed: the original credential the exit is verified
// against and the outcome the native operation reached. The bound home stays on
// the lease, so the retry removes the credential only in the directory it was
// materialized in.
type grokCleanupRetry struct {
	original subscription.GrokBundle
	success  bool
}

// finishGrok settles one Grok lease: it prepares the outcome from the native
// file and reports it through the generic protected Finish. refresh is true only
// for a refresh whose native process already exited. When the private cleanup is
// not confirmed the lease keeps its bound home and the original credential, and
// retryGrokCleanup (the original owner only) reports the cleanup again.
func (l *managedSubscriptionLease) finishGrok(original subscription.GrokBundle, success bool) error {
	return l.settleGrok(original, success, false)
}

// retryGrokCleanup retries the private cleanup of a lease whose Finish reported
// it unconfirmed. The retry uses a fresh Finish request identity, because the
// first Finish committed under the old one, and runs on the same lease owner
// only: the server refuses it from any other Worker device or instance.
func (l *managedSubscriptionLease) retryGrokCleanup() error {
	retry := l.grokRetry
	if retry == nil {
		return subscription.Invalid()
	}
	l.finishID = domain.NewID()
	l.journal.Finish = l.finishID
	return l.settleGrok(retry.original, retry.success, true)
}

func (l *managedSubscriptionLease) settleGrok(original subscription.GrokBundle, success, retried bool) error {
	retained := original
	defer original.Clear()
	action := l.journal.Action
	outcome := prepareGrokFinish(l.grokHome, l.credential.ServerID, l.account, l.finishID, action, original, success)
	if outcome.Cleanup {
		// The bound home is released once the credential is confirmed removed.
		defer func() {
			_ = l.grokHome.Close()
			l.grokHome = subscription.GrokHome{}
			l.grokRetry = nil
			retained.Clear()
		}()
	} else {
		// The home stays bound for the original owner's retry.
		l.grokRetry = &grokCleanupRetry{original: retained, success: success}
	}
	refresh := outcome.Success && (action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH || outcome.Refreshed)
	if l.logger != nil {
		attrs := []slog.Attr{slog.String("account_id", string(l.account)), slog.String("lease_id", l.response.LeaseId), slog.String("action", action.String()), slog.Bool("success", outcome.Success), slog.Bool("refreshed", outcome.Refreshed), slog.Bool("cleanup_confirmed", outcome.Cleanup), slog.Bool("retried", retried)}
		if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
			attrs = append(attrs, slog.String("phase", grokExecutionPhase))
		} else if refresh {
			attrs = append(attrs, slog.String("phase", "REFRESH"))
		}
		if !outcome.Cleanup || retried {
			attrs = append(attrs, slog.String("cleanup_phase", "CLEANUP"))
		}
		l.logger.LogAttrs(context.Background(), slog.LevelInfo, "grok_subscription_finish", attrs...)
	}
	return l.finish(outcome.Bundle, outcome.Cleanup, refresh, outcome.Success)
}
