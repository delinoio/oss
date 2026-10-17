// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The supervised native refresh-login renews the usage credential through the
// original CLI instead of an OAuth client of DeliDev. The original
// `claude auth login --claudeai` child runs in the exclusive usage profile with
// only the stored refresh token, the granted scopes and the optional client ID
// in its own environment. The CLI exchanges the refresh token itself and writes
// the rotated credential into the profile storage, which the caller captures
// after the child is joined. The adapter never reads, enumerates or adopts any
// other profile, and no secret is logged or placed on a command line.

// UsageRefreshTimeout bounds the one refresh-login child.
const UsageRefreshTimeout = time.Minute

const (
	usageRefreshTokenVariable    = "CLAUDE_CODE_OAUTH_REFRESH_TOKEN"
	usageRefreshScopesVariable   = "CLAUDE_CODE_OAUTH_SCOPES"
	usageRefreshClientIDVariable = "CLAUDE_CODE_OAUTH_CLIENT_ID"
)

// UsageRefreshStatus is the closed outcome of one refresh-login child. It tells
// the caller whether an exchange could have happened, which decides whether the
// refresh token may ever be offered again.
type UsageRefreshStatus string

const (
	// UsageRefreshNotStarted means no child ran, so no exchange can have been
	// attempted and the stored refresh token is untouched.
	UsageRefreshNotStarted UsageRefreshStatus = "not-started"
	// UsageRefreshCompleted means the child exited successfully and was joined.
	UsageRefreshCompleted UsageRefreshStatus = "completed"
	// UsageRefreshRejected means the child ran, was joined and reported failure
	// by its exit status. The renewal did not produce a credential.
	UsageRefreshRejected UsageRefreshStatus = "rejected"
	// UsageRefreshUncertain means the child ran but its result is ambiguous: it
	// hit the deadline, was canceled, failed in a non-exit way or could not be
	// proven stopped. The refresh token may have been consumed.
	UsageRefreshUncertain UsageRefreshStatus = "uncertain"
)

// UsageRefreshEnvironment returns the isolated child environment of one
// refresh-login: the usage profile environment of UsageEnvironment plus the
// three child-only OAuth variables. The refresh token exists as a string only
// inside this slice, which the process layer hands to the child and drops.
func UsageRefreshEnvironment(config AuthConfig, bundle subscription.ClaudeUsageBundle) ([]string, error) {
	if len(bundle.RefreshToken) == 0 || len(bundle.Scopes) == 0 {
		return nil, usageRefreshUnsupported()
	}
	env, err := UsageEnvironment(config)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case usageRefreshTokenVariable, usageRefreshScopesVariable, usageRefreshClientIDVariable:
			continue
		}
		result = append(result, entry)
	}
	result = append(result, usageRefreshTokenVariable+"="+string(bundle.RefreshToken), usageRefreshScopesVariable+"="+strings.Join(bundle.Scopes, " "))
	if bundle.ClientID != "" {
		result = append(result, usageRefreshClientIDVariable+"="+bundle.ClientID)
	}
	return result, nil
}

func usageRefreshUnsupported() *domain.Error {
	return domain.Fail(domain.Unsupported, "The Claude usage credential cannot be renewed.", "Connect the usage account again.")
}

// usageRefreshChild is the supervised child surface RunUsageRefresh drives. The
// process handle implements it; tests substitute a fake to fail the start
// barrier deterministically.
type usageRefreshChild interface {
	Resume() error
	CloseInput() error
	Done() <-chan struct{}
	Wait() error
	Close() error
}

// startUsageRefreshChild starts the suspended child behind its start barrier.
var startUsageRefreshChild = func(ctx context.Context, config process.Config) (usageRefreshChild, error) {
	return process.Start(ctx, config)
}

// RunUsageRefresh runs the one supervised refresh-login child and joins it. The
// returned status is meaningful even when err is non-nil. It never repeats the
// child: a caller that sees any status but UsageRefreshNotStarted must treat the
// refresh token as spent.
func RunUsageRefresh(ctx context.Context, config AuthConfig, bundle subscription.ClaudeUsageBundle) (UsageRefreshStatus, error) {
	if config.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Process.Executable) {
		return UsageRefreshNotStarted, authProblem()
	}
	env, err := UsageRefreshEnvironment(config, bundle)
	if err != nil {
		return UsageRefreshNotStarted, err
	}
	defer clear(env)
	stdout, stderr := &authOutput{limit: 32 << 10}, &authOutput{limit: 32 << 10}
	defer stdout.close()
	defer stderr.close()
	prepared := config.Process
	prepared.Env = env
	prepared.Args = []string{"auth", "login", "--claudeai"}
	prepared.Stdout, prepared.Stderr = stdout, stderr
	prepared.StartupObserver = nil
	bounded, cancel := context.WithTimeout(ctx, UsageRefreshTimeout)
	defer cancel()
	handle, err := startUsageRefreshChild(bounded, prepared)
	if err != nil {
		return UsageRefreshNotStarted, err
	}
	if err := handle.Resume(); err != nil {
		// The launch message carries the single-use refresh token. A failed
		// Resume may follow a write that already crossed the private channel, and
		// stopping the process afterwards proves only that it is gone, not that
		// the child never received the token. The process layer cannot prove a
		// start-barrier failure happened before transmission, so the exchange is
		// uncertain and the token is never offered again.
		if handle.Close() != nil {
			return UsageRefreshUncertain, streamUncertain()
		}
		return UsageRefreshUncertain, err
	}
	// The refresh needs no input: close stdin so a prompt can never wait.
	closeInputErr := handle.CloseInput()
	var waitErr error
	interrupted := false
	if closeInputErr == nil {
		select {
		case <-handle.Done():
			waitErr = handle.Wait()
		case <-bounded.Done():
			interrupted = true
		}
	}
	if handle.Close() != nil {
		return UsageRefreshUncertain, streamUncertain()
	}
	switch {
	case interrupted || closeInputErr != nil:
		return UsageRefreshUncertain, domain.Fail(domain.Unavailable, "The Claude usage renewal did not finish.", "Connect the usage account again.")
	case waitErr == nil:
		return UsageRefreshCompleted, nil
	}
	if _, exited := process.CommandExitCode(waitErr); exited {
		return UsageRefreshRejected, authProblem()
	}
	return UsageRefreshUncertain, domain.SafeError(waitErr)
}
