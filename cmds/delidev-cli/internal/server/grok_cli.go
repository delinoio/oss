// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The server owns the native auth profile and process journals. It never
// registers a Worker or borrows the interactive user's Grok home.
type serverGrokCLI struct {
	service *Service
	lease   *nativeprofile.Lease
	home    subscription.GrokHome
	config  process.Config
	version string
}

func (s *Service) openGrokCLI(ctx context.Context, id, operation domain.ID) (*serverGrokCLI, error) {
	scope, err := s.nativeScope(id)
	if err != nil {
		return nil, err
	}
	lease, err := acquireAccountProfile(ctx, scope, domain.GrokBuild, nativeprofile.PurposeAuth)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*serverGrokCLI, error) { _ = lease.Close(); return nil, err }
	env, err := harness.PrivateRuntimeEnvironment(lease.Root)
	if err != nil {
		return fail(err)
	}
	home, err := subscription.BindGrokHome(filepath.Join(lease.Root, "grok"))
	if err != nil {
		return fail(err)
	}
	discover := s.grokCLIDiscover
	if discover == nil {
		discover = harness.DiscoverGrok
	}
	root := filepath.Join(scope.Root, "subscription-runtime")
	installation, err := discover(ctx, harness.DiscoveryConfig{Root: root, OwnerID: operation, Logger: s.logger, Profiles: scope.Profiles})
	if err == nil && installation.State != domain.InstallationDetected {
		err = domain.InstallationProblem(installation.State)
	}
	if err == nil && !domain.GrokSubscriptionVersionAllowed(installation.Version) {
		err = domain.Fail(domain.Unsupported, "The server's Grok CLI version is unsupported.", "Update Grok Build on the server.")
	}
	if err != nil {
		_ = home.Close()
		return fail(err)
	}
	return &serverGrokCLI{service: s, lease: lease, home: home, version: installation.Version, config: process.Config{
		Directory: filepath.Join(root, "processes"), OwnerID: operation, Executable: installation.ResolvedPath,
		Env: env, Cwd: lease.Root, Stdout: io.Discard, Stderr: io.Discard, Logger: s.logger,
	}}, nil
}

func (r *serverGrokCLI) close() { _ = r.home.Close(); _ = r.lease.Close() }

func (r *serverGrokCLI) run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	config := r.config
	config.Args = append([]string{"--no-auto-update"}, args...)
	if stdout != nil {
		config.Stdout = stdout
	}
	if stderr != nil {
		config.Stderr = stderr
	}
	err := process.Run(ctx, config)
	// Reconciliation uses its own bounded lifetime even after user cancellation.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), grokLoginSettleTimeout)
	defer cancel()
	if joined := process.ReconcileOwnerContext(cleanup, config.Directory, config.OwnerID); joined != nil {
		return joined
	}
	return err
}

// claimGrokCLI records uncertainty before the CLI can exchange a device code.
// The CLI owns its polling, so Go cannot observe the exact token-send boundary.
func (s *Service) claimGrokCLI(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.login.native-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch ||
			st.ServerOperation.State != domain.SubscriptionPreparing || !st.ServerOperation.NativeStarted || st.ServerOperation.ExchangeClaimed ||
			st.Pending == nil || st.Pending.ID != o.ID || st.Pending.Canceled || st.RecoveryRequired ||
			subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.ExchangeClaimed = true
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	return err
}

func (s *Service) performGrokCLILogin(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation, canceled *atomic.Bool) grokLoginOutcome {
	flow := &grokFlowState{ctx: ctx, original: original, cancelSeen: canceled}
	runtime, err := s.openGrokCLI(ctx, id, original.ID)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_cli_login_unavailable", "phase", domain.GrokDiscovery, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
		return flow.failed(err)
	}
	defer runtime.close()
	// A retained file is never proof of this new login. Keep it for explicit
	// logout/deletion rather than silently replacing or adopting its identity.
	present, err := runtime.home.AuthFilePresent()
	if err != nil || present {
		return grokLoginOutcome{state: domain.SubscriptionRecovery, code: domain.RecoveryRequired}
	}
	if err := s.claimGrokCLI(ctx, id, original); err != nil {
		return flow.failed(err)
	}
	flow.claimed.Store(true)
	child, stop := context.WithCancel(ctx)
	defer stop()
	prompt := &grokCLIPrompt{publish: func(address, code string) error {
		err := s.publishGrokDeviceProgress(child, id, original, address, code)
		if err != nil {
			stop()
		}
		return err
	}, reject: stop}
	s.logger.InfoContext(ctx, "grok_cli_login_started", "phase", grokLoginPhase, "operation_id", original.ID, "version", runtime.version)
	err = runtime.run(child, []string{"login", "--device-auth"}, prompt.stream(0), prompt.stream(1))
	prompt.flush()
	if err != nil {
		s.logger.WarnContext(ctx, "grok_cli_login_stopped", "phase", grokLoginPhase, "operation_id", original.ID, "code", domain.SafeError(err).Code)
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return grokLoginOutcome{state: domain.SubscriptionRecovery, code: domain.RecoveryRequired}
		}
		// A native exit status cannot prove that a device token was not issued.
		// Keep the durable claim fenced even when the CLI wrote no auth file.
		return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonTokenUncertain, code: domain.SafeError(err).Code}
	}
	if prompt.err != nil || !prompt.published || runtime.home.Verify() != nil {
		return grokLoginOutcome{state: domain.SubscriptionRecovery, code: domain.RecoveryRequired}
	}
	bundle, err := runtime.home.ReadAuthFile()
	if err != nil {
		return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonMapping}
	}
	if !bundle.Identity().Expiry.After(time.Now()) {
		bundle.Clear()
		return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonExpired}
	}
	return grokLoginOutcome{state: domain.SubscriptionSucceeded, bundle: bundle}
}

// Logout preserves the existing provider-revocation guarantee, then asks the
// CLI to clear its own native login in the same permanent account profile.
func (s *Service) performGrokCLILogout(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) (grokLogoutOutcome, bool) {
	runtime, err := s.openGrokCLI(ctx, id, o.ID)
	if err != nil {
		return grokLogoutUnsent(err), false
	}
	defer runtime.close()
	bound, err := s.openGrokBoundBundle(ctx, id, o)
	if err != nil {
		return grokLogoutUnsent(err), false
	}
	defer bound.Clear()
	// Refresh may have rotated the authoritative vault generation since login.
	if err := runtime.home.WriteAuthFile(bound); err != nil {
		return grokLogoutUnsent(err), false
	}
	out, crashed := s.performGrokLogout(ctx, id, o)
	if crashed || !out.revoked {
		return out, crashed
	}
	if err := runtime.run(ctx, []string{"logout"}, nil, nil); err != nil {
		return grokLogoutOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonRevocationUncertain}, false
	}
	// Removal is allowed here only after confirmed native closure and provider
	// revocation. Include the native lock and attributed interrupted writes.
	if err := runtime.home.RetireAuthFile(); err != nil {
		return grokLogoutOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonRevocationUncertain}, false
	}
	return out, false
}

var grokCLIANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var grokCLIURL = regexp.MustCompile(`https://[^\s]+`)
var grokCLICode = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)

// Only the closed verification URL/code pair escapes this bounded filter.
// Raw CLI output (including the post-login identity) is discarded immediately.
type grokCLIPrompt struct {
	mu            sync.Mutex
	pending       [2][]byte
	overrun       [2]bool
	address, code string
	published     bool
	err           error
	publish       func(string, string) error
	reject        func()
}
type grokCLIStream struct {
	prompt *grokCLIPrompt
	index  int
}

func (p *grokCLIPrompt) stream(index int) io.Writer { return grokCLIStream{p, index} }
func (w grokCLIStream) Write(raw []byte) (int, error) {
	p := w.prompt
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range raw {
		if b == '\n' {
			if !p.overrun[w.index] {
				p.line(p.pending[w.index])
			}
			clear(p.pending[w.index])
			p.pending[w.index] = p.pending[w.index][:0]
			p.overrun[w.index] = false
			continue
		}
		if p.overrun[w.index] {
			continue
		}
		if len(p.pending[w.index]) >= 4096 {
			clear(p.pending[w.index])
			p.pending[w.index] = nil
			p.overrun[w.index] = true
			continue
		}
		p.pending[w.index] = append(p.pending[w.index], b)
	}
	return len(raw), nil
}
func (p *grokCLIPrompt) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.pending {
		if !p.overrun[i] {
			p.line(p.pending[i])
		}
		clear(p.pending[i])
		p.pending[i] = nil
	}
}
func (p *grokCLIPrompt) line(raw []byte) {
	if p.published || p.err != nil {
		return
	}
	text := strings.TrimSpace(grokCLIANSI.ReplaceAllString(string(raw), ""))
	if address := grokCLIURL.FindString(text); address != "" {
		p.address = address
	}
	if grokCLICode.MatchString(text) {
		p.code = text
	}
	if p.address == "" || p.code == "" {
		return
	}
	if !subscription.ValidGrokDeviceVerification(p.address, p.code) {
		p.err = errors.New("invalid Grok CLI verification prompt")
		if p.reject != nil {
			p.reject()
		}
		return
	}
	p.err = p.publish(p.address, p.code)
	p.published = p.err == nil
}
