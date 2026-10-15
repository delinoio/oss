// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type claudeProfileOwner struct {
	Version                           uint32 `json:"version"`
	Server, Machine, Account, Profile domain.ID
	IdentityCommitment                string `json:"identity_commitment,omitempty"`
}
type nativeClaudeProfile struct {
	root, home   string
	original     os.FileInfo
	lock         *security.Lock
	owner        claudeProfileOwner
	config       claude.AuthConfig
	managedLease *managedSubscriptionLease
	newProfile   bool
	registry     *nativeprofile.Registry
	// historyVersion is set only on a shared-owner member's view of the owner's
	// profile. It selects canonical-profile (1) or session-private (2) history.
	historyVersion claudeProfileVersion
}

func claudeProfileRoot(root string, credential Credential, account, profile domain.ID) (string, error) {
	for _, id := range []domain.ID{credential.ServerID, credential.MachineID, account, profile} {
		if id.Validate() != nil {
			return "", subscription.Invalid()
		}
	}
	return filepath.Join(root, "native-claude", string(credential.ServerID), string(account), string(profile)), nil
}

// recoverRemovedClaudeProfile clears only the original Claude auth registration
// after the owning operation's journal and joined processes prove cleanup
// authority. It never opens a native home or starts another native operation.
func recoverRemovedClaudeProfile(ctx context.Context, config Config, credential Credential, account, profile domain.ID) error {
	root, err := claudeProfileRoot(config.Root, credential, account, profile)
	if err != nil {
		return err
	}
	lock, err := security.TryLock(root + ".lock")
	if err != nil {
		return domain.SafeError(err)
	}
	defer lock.Close()
	profiles, err := nativeProfiles(config, credential.MachineID)
	if err != nil {
		return err
	}
	relative := filepath.ToSlash(filepath.Join("native-claude", string(credential.ServerID), string(account), string(profile)))
	key := nativeprofile.Key{Harness: domain.ClaudeCode, Purpose: nativeprofile.PurposeAuth, Account: account}
	if err := profiles.DeleteRemovedClaudeProfile(ctx, key, relative); err != nil {
		return err
	}
	remaining, err := profiles.Exists(ctx, nativeprofile.DeleteScope{Account: account, Purpose: nativeprofile.PurposeAuth, Harness: domain.ClaudeCode})
	if err != nil {
		return err
	}
	if remaining {
		return subscription.Invalid()
	}
	return nil
}

// claudeInstallationAdmitted is the one admission rule for every executable that
// opens a native Claude profile: execution, login and historical cleanup. An
// inspected installation keeps the numeric baseline. A version-4 direct-startup
// identity carries only the resolved path and its executable digest, so it has
// no numeric gate; the original process proves its version before any use.
func claudeInstallationAdmitted(installation domain.Installation) bool {
	if installation.Harness != domain.ClaudeCode || installation.ResolvedPath == "" {
		return false
	}
	if installation.Version == "" {
		return domain.ValidExecutableSHA256(installation.ExecutableSHA256)
	}
	return domain.HarnessVersionAllowed(domain.ClaudeCode, installation.Version)
}

// retainClaudeInstallation durably records the exact executable evidence in the
// operation journal. Call it before any native profile side effect so that only
// journals written before this field existed ever lack it.
func (l *managedSubscriptionLease) retainClaudeInstallation(installation domain.Installation) error {
	retained := installation
	l.journal.Installation = &retained
	return writeJSON(l.journalPath, l.journal)
}

func openClaudeProfile(config Config, credential Credential, account, profile domain.ID, installation domain.Installation, owner domain.ID, create bool) (*nativeClaudeProfile, error) {
	root, err := claudeProfileRoot(config.Root, credential, account, profile)
	if err != nil {
		return nil, err
	}
	if !claudeInstallationAdmitted(installation) {
		return nil, subscription.Invalid()
	}
	executable, err := filepath.EvalSymlinks(installation.ResolvedPath)
	if err != nil || !filepath.IsAbs(executable) || executable != installation.ResolvedPath {
		return nil, subscription.Invalid()
	}
	// Re-inspect the retained digest here, at the single point every
	// profile-bearing launch passes through, so a binary replaced after
	// resolution (for example while waiting for the lease) never receives the
	// profile directories.
	digestCtx, stopDigest := context.WithTimeout(context.Background(), 30*time.Second)
	digestErr := verifyClaudeExecutableDigest(digestCtx, config.Logger, installation)
	stopDigest()
	if digestErr != nil {
		return nil, digestErr
	}
	for _, dir := range []string{filepath.Join(config.Root, "native-claude"), filepath.Join(config.Root, "native-claude", string(credential.ServerID)), filepath.Dir(root)} {
		if err := security.PrivateDir(dir); err != nil {
			return nil, domain.SafeError(err)
		}
	}
	lock, err := security.TryLock(root + ".lock")
	if err != nil {
		return nil, domain.SafeError(err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = lock.Close()
		}
	}()
	expected := claudeProfileOwner{Version: 1, Server: credential.ServerID, Machine: credential.MachineID, Account: account, Profile: profile}
	observed := expected
	path := filepath.Join(root, "owner.json")
	if create {
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			return nil, subscription.Invalid()
		}
		if err := security.PrivateDir(root); err != nil {
			return nil, err
		}
		if _, err := harness.PrivateRuntimeEnvironment(root); err != nil {
			return nil, err
		}
		if err := writeJSON(path, expected); err != nil {
			return nil, err
		}
	} else {
		raw, err := security.ReadPrivate(path, 4096)
		if err != nil || domain.Decode(raw, &observed) != nil {
			return nil, subscription.Invalid()
		}
		owner := observed
		owner.IdentityCommitment = ""
		if owner != expected || observed.IdentityCommitment != "" && !domain.NativeIdentityCommitmentValid(observed.IdentityCommitment) {
			return nil, subscription.Invalid()
		}
	}
	if security.CheckPrivateDir(root) != nil {
		return nil, subscription.Invalid()
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return nil, subscription.Invalid()
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, subscription.Invalid()
	}
	env, err := harness.PrivateRuntimeEnvironment(root)
	if err != nil {
		return nil, err
	}
	auth := claude.AuthConfig{Version: installation.Version, DirectStartup: installation.Version == "", Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: root, Env: env, Logger: config.Logger}}
	// Register the durable profile in place so the shared registry owns its
	// deletion and reference semantics; nothing is moved or rewritten.
	profiles, err := nativeProfiles(config, credential.MachineID)
	if err != nil {
		return nil, err
	}
	if _, err := profiles.RegisterLegacy(context.Background(), nativeprofile.Key{Harness: domain.ClaudeCode, Purpose: nativeprofile.PurposeAuth, Account: account}, filepath.ToSlash(filepath.Join("native-claude", string(credential.ServerID), string(account), string(profile)))); err != nil {
		return nil, err
	}
	ok = true
	return &nativeClaudeProfile{root: root, home: auth.Home, original: info, lock: lock, owner: observed, config: auth, newProfile: create, registry: profiles}, nil
}
func (p *nativeClaudeProfile) close() error {
	// A shared-owner member's view holds no lock: the owner keeps the profile lock
	// for its whole lifetime.
	if p.lock == nil {
		return nil
	}
	return p.lock.Close()
}
func (p *nativeClaudeProfile) logout(ctx context.Context) error {
	if err := claude.LogoutSubscription(ctx, p.config); err != nil {
		return err
	}
	// The official CLI removes its scoped secure-store login. Whole-profile
	// removal then deletes only this owned directory without inspecting token
	// files. Native history cannot grant Resume after this connection is retired.
	// Logout is the explicit account action that owns this profile's deletion.
	if err := subscription.CleanupRuntime(p.root, p.original); err != nil {
		return err
	}
	if p.registry != nil {
		_, err := p.registry.Delete(context.Background(), nativeprofile.DeleteScope{Account: p.owner.Account, Purpose: nativeprofile.PurposeAuth, Harness: domain.ClaudeCode})
		return err
	}
	return nil
}
func (p *nativeClaudeProfile) identity(ctx context.Context, config Config) (*pb.NativeSubscriptionIdentity, error) {
	status, err := claude.ReadAuthStatus(ctx, p.config)
	if err != nil {
		return nil, err
	}
	raw, err := status.Identity()
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	root := filepath.Join(config.Root, "native-claude", string(p.owner.Server))
	lock, err := security.TryLock(filepath.Join(root, "identity.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	path := filepath.Join(root, "identity.key")
	key, err := security.ReadPrivate(path, 32)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, domain.SafeError(err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, subscription.Invalid()
		}
		_, written := file.Write(key)
		synced := file.Sync()
		closed := file.Close()
		if written != nil || synced != nil || closed != nil || security.SyncParent(path) != nil {
			return nil, subscription.Invalid()
		}
	} else if err != nil {
		return nil, subscription.Invalid()
	}
	defer clear(key)
	if len(key) != 32 {
		return nil, subscription.Invalid()
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	commitment := hex.EncodeToString(mac.Sum(nil))
	if p.owner.IdentityCommitment == "" {
		if !p.newProfile {
			return nil, subscription.Invalid()
		}
		// The original login pins only a keyed identity in the private owner
		// record. Reauthentication and execution compare it before native input;
		// an externally changed login cannot become this account's authority.
		p.owner.IdentityCommitment = commitment
		if err := writeJSON(filepath.Join(p.root, "owner.json"), p.owner); err != nil {
			return nil, subscription.Invalid()
		}
	} else if !hmac.Equal([]byte(p.owner.IdentityCommitment), []byte(commitment)) {
		return nil, domain.Fail(domain.Unauthenticated, "The owned Claude profile contains a different account.", "Log out this original profile before connecting another Claude account.")
	}
	return &pb.NativeSubscriptionIdentity{ProfileId: string(p.owner.Profile), IdentityCommitment: commitment}, nil
}
func (l *managedSubscriptionLease) finishClaude(identity *pb.NativeSubscriptionIdentity, cleanup, success bool) error {
	return l.finishClaudeScoped(identity, cleanup, success, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED, l.response.LeaseRevision)
}

// finishClaudeScoped reports one Finish. The unspecified scope settles a
// singleton lease. A member scope releases or retains exactly one execution member
// of a shared owner, and the owner scope retires the owner with the owner's own
// revision after its last runnable member.
func (l *managedSubscriptionLease) finishClaudeScoped(identity *pb.NativeSubscriptionIdentity, cleanup, success bool, scope pb.SubscriptionFinishScope, revision uint64) error {
	l.journal.State = managedClosed
	l.journal.Cleanup = cleanup
	l.journal.Success = success
	l.journal.NativeIdentity = ""
	if identity != nil {
		l.journal.NativeIdentity = identity.IdentityCommitment
	}
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := l.client.FinishSubscription(ctx, authenticated(l.credential, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(l.finishID), Id: string(l.account), ExpectedRevision: revision}, LeaseId: l.response.LeaseId, GenerationId: l.response.GenerationId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), NativeIdentity: identity, CleanupConfirmed: cleanup, Succeeded: success, FinishScope: scope}))
	if err != nil {
		return rpc.ClientError(err)
	}
	var account domain.Account
	if response.Msg.Account == nil || domain.Decode(response.Msg.Account.DocumentJson, &account) != nil || account.Subscription == nil {
		return &managedExecutionUncertain{subscription.Invalid()}
	}
	state := account.Subscription
	leaseID := domain.ID(l.response.LeaseId)
	switch scope {
	case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER:
		// An acknowledged member Finish leaves the owner in place. The member is
		// gone when its closure was proven and retained otherwise.
		member, present := state.Member(leaseID)
		if state.Lease == nil || !state.Lease.Shared || cleanup && present || !cleanup && (!present || member.State != domain.SubscriptionMemberRetained) {
			return &managedExecutionUncertain{subscription.Invalid()}
		}
	default:
		if state.Lease != nil && (state.Lease.ID == leaseID || state.Lease.Shared && state.Lease.ID == domain.ID(l.response.OwnerLeaseId)) {
			return &managedExecutionUncertain{subscription.Invalid()}
		}
	}
	l.journal.State = managedReported
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}
func runClaudeAccount(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account domain.ID, revision uint64, op domain.SubscriptionOperation) (returned error) {
	action := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	if op.Action == domain.SubscriptionRefresh {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	}
	if op.Action == domain.SubscriptionLogout {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	}
	// An operation queued behind a live shared owner waits inside Take for as
	// long as the owner's members run. Its lifetime begins at the grant, exactly
	// like the server's rebased operation window, so the wait is bound only by the
	// watch context and the server's own decision to stop admitting the Take.
	lease, err := takeManagedSubscription(ctx, config, client, credential, instance, account, op.ID, revision, action)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cleanup, success := false, false
	var identity *pb.NativeSubscriptionIdentity
	phase := domain.NativeSubscriptionRuntime
	// observedVersion is the executable version reported by discovery. It is
	// never replaced by the pinned baseline.
	observedVersion := ""
	defer func() {
		if returned != nil {
			diagnostic := &pb.NativeSubscriptionDiagnostic{DetectedVersion: observedVersion, RequiredVersion: domain.ClaudeProtocolVersion, Phase: pb.NativeSubscriptionDiagnosticPhase(phase), Code: string(domain.SafeError(returned).Code), CorrelationId: string(op.ID)}
			reportCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = client.PublishSubscriptionProgress(reportCtx, authenticated(credential, &pb.PublishSubscriptionProgressRequest{AccountId: string(account), LeaseId: lease.response.LeaseId, MachineId: string(credential.MachineID), InstanceId: string(instance), State: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED, NativeDiagnostic: diagnostic}))
			stop()
		}
		if err := lease.finishClaude(identity, cleanup, success); err != nil {
			returned = err
		} else if returned != nil {
			returned = &managedReportedFailure{returned}
		}
	}()
	if len(lease.response.Bundle) != 0 || domain.ID(lease.response.NativeProfileId).Validate() != nil {
		clear(lease.response.Bundle)
		return subscription.Invalid()
	}
	var installation domain.Installation
	if domain.Decode(lease.response.InstallationJson, &installation) != nil {
		return subscription.Invalid()
	}
	if domain.ValidInstallationVersion(installation.Version) {
		observedVersion = installation.Version
	}
	// Account operations use inspected inventory; only execution may carry the
	// versionless direct-startup identity.
	if installation.Version == "" {
		return subscription.Invalid()
	}
	// The profile identity and original executable are durable before the profile
	// is created or touched, so a crash in this window still recovers with the
	// original binary.
	lease.journal.NativeProfileID = domain.ID(lease.response.NativeProfileId)
	if err := lease.retainClaudeInstallation(installation); err != nil {
		return subscription.Invalid()
	}
	profile, err := openClaudeProfile(config, credential, account, domain.ID(lease.response.NativeProfileId), installation, domain.ID(lease.response.LeaseId), op.Action == domain.SubscriptionLogin)
	if err != nil {
		return err
	}
	defer func() {
		if err := profile.close(); err != nil {
			cleanup = false
			returned = domain.SafeError(err)
		}
	}()
	lease.journal.NativeStarted = true
	if err := security.PrivateDir(filepath.Join(config.Root, "processes", lease.response.LeaseId)); err != nil {
		return err
	}
	if err := writeJSON(lease.journalPath, lease.journal); err != nil {
		return err
	}
	phase = domain.NativeSubscriptionVersion
	if err := claude.VerifyAuthVersion(bounded, profile.config); err != nil {
		return err
	}
	phase = domain.NativeSubscriptionLogin
	if op.Action == domain.SubscriptionLogout {
		phase = domain.NativeSubscriptionCleanup
		if err := profile.logout(bounded); err != nil {
			return err
		}
		cleanup, success = true, true
		return nil
	}
	lease.journal.NativeStarted = true
	if err := writeJSON(lease.journalPath, lease.journal); err != nil {
		return err
	}
	login, err := claude.StartSubscriptionLogin(bounded, profile.config)
	if err != nil {
		if domain.SafeError(err).Code != domain.RecoveryRequired {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			cleanup = profile.logout(cleanupCtx) == nil
			stop()
		}
		return err
	}
	finished := false
	defer func() {
		if err := login.Close(); err != nil {
			cleanup = false
			returned = err
			return
		}
		if finished {
			return
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		phase = domain.NativeSubscriptionCleanup
		if err := profile.logout(cleanupCtx); err != nil {
			cleanup = false
			returned = err
		} else {
			cleanup = true
			identity = nil
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	submitted := false
	for {
		progress, err := login.Progress()
		if err != nil {
			return err
		}
		state := pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING
		if progress.URL != "" {
			state = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING
		}
		report, err := client.PublishSubscriptionProgress(bounded, authenticated(credential, &pb.PublishSubscriptionProgressRequest{AccountId: string(account), LeaseId: lease.response.LeaseId, MachineId: string(credential.MachineID), InstanceId: string(instance), Url: progress.URL, State: state, LoginMethod: pb.SubscriptionLoginMethod(progress.Method)}))
		if err != nil {
			return rpc.ClientError(err)
		}
		if report.Msg.Canceled {
			return domain.Fail(domain.Canceled, "The original Claude login was canceled.", "Wait for the original Runner to confirm native cleanup.")
		}
		if progress.Method == domain.SubscriptionBrowserCode && !submitted {
			response, err := client.TakeSubscriptionLoginCode(bounded, authenticated(credential, &pb.TakeSubscriptionLoginCodeRequest{AccountId: string(account), LeaseId: lease.response.LeaseId, MachineId: string(credential.MachineID), InstanceId: string(instance), OperationId: string(op.ID)}))
			if err != nil {
				return rpc.ClientError(err)
			}
			if len(response.Msg.Code) != 0 {
				if domain.ID(response.Msg.SubmissionId).Validate() != nil {
					clear(response.Msg.Code)
					return subscription.Invalid()
				}
				// A durable local claim precedes stdin. A crash, lost Take reply or partial
				// write never permits resending the original approval input.
				lease.journal.CodeSubmissionID = domain.ID(response.Msg.SubmissionId)
				submitted = true
				if err := writeJSON(lease.journalPath, lease.journal); err != nil {
					clear(response.Msg.Code)
					return subscription.Invalid()
				}
				if err := login.Submit(response.Msg.Code); err != nil {
					return err
				}
			}
		}
		select {
		case <-bounded.Done():
			return domain.SafeError(bounded.Err())
		case <-ticker.C:
		case <-login.Done():
			if err := login.Wait(); err != nil {
				return err
			}
			if err := login.Close(); err != nil {
				return err
			}
			phase = domain.NativeSubscriptionStatus
			identity, err = profile.identity(bounded, config)
			if err != nil {
				return err
			}
			cleanup, success, finished = true, true, true
			return nil
		}
	}
}

// claudeCleanupInstallation resolves the original installation for historical
// device-owned cleanup. The retained journal evidence wins; only journals
// written before it was retained fall back to the single passively decoded,
// fully verified inventory entry of the current attach. Whichever source
// supplies it, any digest it retains must still match the executable on disk
// before cleanup launches it with access to the original profile; a mismatch
// fails closed.
func claudeCleanupInstallation(ctx context.Context, config Config, journal managedSubscriptionJournal) (domain.Installation, error) {
	installation, err := claudeRetainedInstallation(config, journal)
	if err != nil {
		return domain.Installation{}, err
	}
	if err := verifyClaudeExecutableDigest(ctx, config.Logger, installation); err != nil {
		return domain.Installation{}, err
	}
	return installation, nil
}

// verifyClaudeExecutableDigest is the one executable-identity recheck for every
// launch that carries a native Claude profile. An installation that retains a
// digest must still match the bytes on disk right now; one that retains none
// (an inspected entry without a digest) has nothing to compare. A mismatch
// fails closed before any process receives the profile. The check cannot close
// the window between this read and the later exec; that residual race is
// documented in the execution startup contract.
func verifyClaudeExecutableDigest(ctx context.Context, logger *slog.Logger, installation domain.Installation) error {
	if installation.ExecutableSHA256 == "" {
		return nil
	}
	digest, err := harness.InspectExecutable(ctx, installation.ResolvedPath)
	if err != nil || digest != installation.ExecutableSHA256 {
		if logger != nil {
			logger.WarnContext(ctx, "claude_profile_executable_changed", "inspected", err == nil)
		}
		return domain.Fail(domain.RecoveryRequired, "The original native executable changed.", "Restore the original executable before opening its native profile.")
	}
	return nil
}

func claudeRetainedInstallation(config Config, journal managedSubscriptionJournal) (domain.Installation, error) {
	if journal.Installation != nil {
		return *journal.Installation, nil
	}
	var machine domain.Machine
	if config.attachedMachine == nil || domain.Decode(config.attachedMachine.DocumentJson, &machine) != nil {
		return domain.Installation{}, subscription.Invalid()
	}
	var found *domain.Installation
	for i := range machine.Installations {
		candidate := &machine.Installations[i]
		if candidate.Harness == domain.ClaudeCode && domain.HarnessVersionAllowed(domain.ClaudeCode, candidate.Version) && candidate.ProtocolVerified && candidate.State == domain.InstallationDetected && candidate.Problem == nil && candidate.Protocol != nil && candidate.Protocol.State == domain.ProtocolVerified && candidate.Protocol.Problem == nil {
			if found != nil {
				return domain.Installation{}, subscription.Invalid()
			}
			found = candidate
		}
	}
	if found == nil {
		return domain.Installation{}, subscription.Invalid()
	}
	return *found, nil
}

// managedClaudeToken is one server-owned setup token delivered by the protected
// execution lease. It exists only in this Worker process until the joined child
// has terminated; it is never written to a journal, file or log.
type managedClaudeToken struct {
	token []byte
	lease *managedSubscriptionLease
}

func (m *managedClaudeToken) clear() {
	clear(m.token)
	m.token = nil
}

func beginClaudeSubscriptionExecution(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput) (*nativeClaudeProfile, *managedClaudeToken, func(bool) error, error) {
	if config.claudeMembers && config.claudeOwners != nil {
		return beginClaudeSharedExecution(ctx, config, owner, input)
	}
	connection := config.execution
	client, closeHTTP, err := subscriptionRPC(ctx, config, connection.Credential)
	if err != nil {
		return nil, nil, nil, err
	}
	lease, err := takeManagedSubscription(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
	if err != nil {
		closeHTTP()
		return nil, nil, nil, err
	}
	if len(lease.response.Bundle) != 0 {
		return claudeTokenExecution(config, input, lease, closeHTTP)
	}
	var profile *nativeClaudeProfile
	finish := func(joined bool) error {
		defer closeHTTP()
		var identity *pb.NativeSubscriptionIdentity
		var closeProblem error
		bounded, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if joined && profile != nil {
			identity, err = profile.identity(bounded, config)
			if err != nil {
				joined = false
			}
		} else {
			joined = false
		}
		if profile != nil {
			if closed := profile.close(); closed != nil {
				joined, identity, closeProblem = false, nil, closed
			}
		}
		// Keep the server lease until the original local lock is released. A
		// queued logout can start as soon as Finish commits and must not race
		// the closing execution profile or turn a settled execution into recovery.
		if err := lease.finishClaude(identity, joined, joined); err != nil {
			return err
		}
		return closeProblem
	}
	if len(lease.response.Bundle) != 0 || domain.ID(lease.response.NativeProfileId).Validate() != nil {
		clear(lease.response.Bundle)
		_ = finish(false)
		return nil, nil, nil, subscription.Invalid()
	}
	// Retain the original executable before the profile opens (see runClaudeAccount).
	err = lease.retainClaudeInstallation(input.Installation)
	if err == nil {
		profile, err = openClaudeProfile(config, connection.Credential, input.AccountID, domain.ID(lease.response.NativeProfileId), input.Installation, domain.ID(lease.response.LeaseId), false)
	}
	if err == nil {
		if err = security.PrivateDir(filepath.Join(config.Root, "processes", lease.response.LeaseId)); err == nil {
			lease.journal.NativeStarted = true
			err = writeJSON(lease.journalPath, lease.journal)
		}
	}
	if err == nil {
		err = claude.VerifyAuthVersion(ctx, profile.config)
	}
	if err == nil {
		_, err = profile.identity(ctx, config)
	}
	if err != nil {
		_ = finish(false)
		return nil, nil, nil, err
	}
	profile.managedLease = lease
	return profile, nil, finish, nil
}

// Recovery consumes the original local claim and performs cleanup only. It
// never retakes a lease, launches login or reads/replays an approval code.
func runClaudeRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, accountID domain.ID, account domain.Account) error {
	state := account.Subscription
	if state == nil || !state.RecoveryRequired || state.Lease == nil || state.OwnerMachineID != credential.MachineID {
		return subscription.Invalid()
	}
	original := state.Lease
	path := filepath.Join(config.Root, "managed-auth", string(original.ID)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Lease != original.ID || journal.Account != accountID || journal.Operation != original.OperationID || journal.Instance != original.InstanceID || journal.Finish.Validate() != nil || journal.NativeProfileID != "" && journal.NativeProfileID != state.NativeProfileID || journal.Generation != original.Generation {
		return subscription.Invalid()
	}
	if journal.NativeStarted {
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), original.ID); err != nil {
			return err
		}
	}
	if original.Action == domain.SubscriptionExecute && journal.ExecutionStarted {
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), original.OperationID); err != nil {
			return err
		}
	}
	if original.Shared {
		// A lost shared owner is cleaned as one whole owner/member set: every
		// member's process owner is joined before the canonical profile is touched.
		if err := reconcileSharedClaudeMembers(ctx, config, state.Members); err != nil {
			return err
		}
	}
	if journal.CleanupFinish == "" {
		journal.CleanupFinish = domain.NewID()
		if err := writeJSON(path, journal); err != nil {
			return subscription.Invalid()
		}
	}
	// A lost shared owner always cleans the canonical profile: the founding
	// member's per-session Cleanup and NativeIdentity say nothing about the
	// owner's shared authentication, so only the owner-level claim counts.
	profileCleanupPending := !journal.Cleanup || journal.NativeIdentity != ""
	if original.Shared {
		profileCleanupPending = !journal.OwnerProfileCleaned
	}
	if profileCleanupPending {
		root, err := claudeProfileRoot(config.Root, credential, accountID, state.NativeProfileID)
		if err != nil {
			return err
		}
		_, statErr := os.Lstat(root)
		noNative := claudeNativeNeverTouched(original, journal, statErr)
		originalCleanupAction := (original.Action == domain.SubscriptionLogout && journal.Action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT) ||
			(original.Shared && original.Action == domain.SubscriptionExecute && journal.Action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
		removedOriginal := errors.Is(statErr, os.ErrNotExist) && journal.NativeStarted && journal.NativeProfileID == state.NativeProfileID && originalCleanupAction
		if removedOriginal {
			if err := recoverRemovedClaudeProfile(ctx, config, credential, accountID, state.NativeProfileID); err != nil {
				return err
			}
		} else if !noNative {
			installation, err := claudeCleanupInstallation(ctx, config, journal)
			if err != nil {
				return err
			}
			profile, err := openClaudeProfile(config, credential, accountID, state.NativeProfileID, installation, original.ID, false)
			if err != nil {
				return err
			}
			cleaned := profile.logout(ctx)
			closed := profile.close()
			if cleaned != nil {
				return cleaned
			}
			if closed != nil {
				return closed
			}
		}
		journal.Cleanup = true
		journal.Success = false
		journal.NativeIdentity = ""
		journal.State = managedClosed
		journal.OwnerProfileCleaned = original.Shared
		if err := writeJSON(path, journal); err != nil {
			return subscription.Invalid()
		}
	}
	lease := &managedSubscriptionLease{client: client, credential: credential, instance: original.InstanceID, account: accountID, finishID: journal.CleanupFinish, journalPath: path, journal: journal, response: &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), GenerationId: string(original.Generation), LeaseRevision: original.Revision}}
	config.Logger.InfoContext(ctx, "claude_subscription_original_cleanup_confirmed", "lease_id", original.ID, "account_id", accountID, "shared_owner", original.Shared, "members", len(state.Members))
	if original.Shared {
		if err := lease.finishClaudeScoped(nil, true, false, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, original.Revision); err != nil {
			return err
		}
		settleSharedClaudeMemberJournals(config, state.Members)
		return nil
	}
	return lease.finishClaude(nil, true, false)
}

// claudeNativeNeverTouched is the one rule that settles a device-owned lease
// without any native cleanup because the Worker provably never opened its
// profile. Two independent proofs exist. A LOGIN whose profile directory does not
// exist never created it. A current-schema journal (InstallationRetention) that
// still lacks the retained installation never reached the retention that
// precedes every profile open, so a crash right after Take leaves the existing
// account profile untouched for a non-LOGOUT action. A LOGOUT must still remove
// the profile, and journals without the marker are ambiguous legacy evidence.
func claudeNativeNeverTouched(original *domain.SubscriptionLease, journal managedSubscriptionJournal, statErr error) bool {
	if original.Shared || journal.NativeStarted {
		return false
	}
	if original.Action == domain.SubscriptionLogin && errors.Is(statErr, os.ErrNotExist) {
		return true
	}
	return journal.InstallationRetention && journal.Installation == nil && original.Action != domain.SubscriptionLogout
}

// runManagedClaudeRecovery consumes the original local claim of a server-token
// execution lease and performs cleanup only. It holds no token, never retakes a
// lease and cannot report execution success.
func runManagedClaudeRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, accountID domain.ID, account domain.Account) error {
	state := account.Subscription
	if state == nil || !state.ManagedClaude() || !state.RecoveryRequired || state.Lease == nil || state.Lease.Action != domain.SubscriptionExecute || state.Lease.MachineID != credential.MachineID {
		return subscription.Invalid()
	}
	original := state.Lease
	path := filepath.Join(config.Root, "managed-auth", string(original.ID)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Lease != original.ID || journal.Account != accountID || journal.Operation != original.OperationID || journal.Instance != original.InstanceID || journal.Finish.Validate() != nil || journal.NativeProfileID != "" {
		return subscription.Invalid()
	}
	// The durable claim precedes the protected delivery. If the Take response was
	// lost, or its fields could not be persisted, the journal still has no
	// generation or lease revision and never started an execution while the server
	// lease carries the settled generation. That exact original claim is accepted;
	// once any field was persisted it must agree with the server lease.
	preDelivery := journal.Generation == "" && journal.LeaseRevision == 0 && !journal.ExecutionStarted
	if !preDelivery && journal.Generation != original.Generation || journal.LeaseRevision != 0 && journal.LeaseRevision != original.Revision {
		return subscription.Invalid()
	}
	// The token lived only in the original execution child's environment. Joined
	// termination of that exact owner is the whole cleanup proof.
	if journal.ExecutionStarted {
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), original.OperationID); err != nil {
			return err
		}
	}
	if journal.CleanupFinish == "" {
		journal.CleanupFinish = domain.NewID()
		if err := writeJSON(path, journal); err != nil {
			return subscription.Invalid()
		}
	}
	journal.Cleanup, journal.Success, journal.NativeIdentity, journal.State = true, false, "", managedClosed
	if err := writeJSON(path, journal); err != nil {
		return subscription.Invalid()
	}
	lease := &managedSubscriptionLease{client: client, credential: credential, instance: original.InstanceID, account: accountID, finishID: journal.CleanupFinish, journalPath: path, journal: journal, response: &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), GenerationId: string(original.Generation), LeaseRevision: original.Revision}}
	config.Logger.InfoContext(ctx, "claude_managed_original_cleanup_confirmed", "lease_id", original.ID, "account_id", accountID, "pre_delivery", preDelivery)
	return lease.finishClaude(nil, true, false)
}

// claudeTokenExecution handles a server-owned setup-token lease. Only that lane
// delivers a sealed envelope; it is bound to this server, account and settled
// generation, carries no profile and always keeps exclusive custody.
func claudeTokenExecution(config Config, input domain.ExecutionJobInput, lease *managedSubscriptionLease, closeHTTP func()) (*nativeClaudeProfile, *managedClaudeToken, func(bool) error, error) {
	connection := config.execution
	managed := &managedClaudeToken{lease: lease}
	finish := func(joined bool) error {
		defer closeHTTP()
		managed.clear()
		return lease.finishClaude(nil, joined, joined)
	}
	opened, openErr := subscription.OpenClaudeToken(lease.response.Bundle, connection.Credential.ServerID, input.AccountID, domain.ID(lease.response.GenerationId))
	clear(lease.response.Bundle)
	if openErr != nil || domain.ID(lease.response.NativeProfileId) != "" {
		opened.Clear()
		_ = finish(false)
		return nil, nil, nil, subscription.ClaudeTokenInvalid()
	}
	managed.token = opened.Token
	config.Logger.InfoContext(context.Background(), "claude_managed_lease_taken", "account_id", input.AccountID, "lease_id", lease.response.LeaseId)
	return nil, managed, finish, nil
}
