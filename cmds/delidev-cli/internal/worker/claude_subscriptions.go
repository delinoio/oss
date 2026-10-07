// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
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
}

func claudeProfileRoot(root string, credential Credential, account, profile domain.ID) (string, error) {
	for _, id := range []domain.ID{credential.ServerID, credential.MachineID, account, profile} {
		if id.Validate() != nil {
			return "", subscription.Invalid()
		}
	}
	return filepath.Join(root, "native-claude", string(credential.ServerID), string(account), string(profile)), nil
}
func openClaudeProfile(config Config, credential Credential, account, profile domain.ID, installation domain.Installation, owner domain.ID, create bool) (*nativeClaudeProfile, error) {
	root, err := claudeProfileRoot(config.Root, credential, account, profile)
	if err != nil {
		return nil, err
	}
	if installation.Harness != domain.ClaudeCode || installation.Version != claude.SupportedVersion || installation.ResolvedPath == "" {
		return nil, subscription.Invalid()
	}
	executable, err := filepath.EvalSymlinks(installation.ResolvedPath)
	if err != nil || !filepath.IsAbs(executable) || executable != installation.ResolvedPath {
		return nil, subscription.Invalid()
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
	auth := claude.AuthConfig{Version: installation.Version, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: root, Env: env, Logger: config.Logger}}
	ok = true
	return &nativeClaudeProfile{root: root, home: auth.Home, original: info, lock: lock, owner: observed, config: auth, newProfile: create}, nil
}
func (p *nativeClaudeProfile) close() error { return p.lock.Close() }
func (p *nativeClaudeProfile) logout(ctx context.Context) error {
	if err := claude.LogoutSubscription(ctx, p.config); err != nil {
		return err
	}
	// The official CLI removes its scoped secure-store login. Whole-profile
	// removal then deletes only this owned directory without inspecting token
	// files. Native history cannot grant Resume after this connection is retired.
	return subscription.CleanupRuntime(p.root, p.original)
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
	response, err := l.client.FinishSubscription(ctx, authenticated(l.credential, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(l.finishID), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, GenerationId: l.response.GenerationId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), NativeIdentity: identity, CleanupConfirmed: cleanup, Succeeded: success}))
	if err != nil {
		return rpc.ClientError(err)
	}
	var account domain.Account
	if response.Msg.Account == nil || domain.Decode(response.Msg.Account.DocumentJson, &account) != nil || account.Subscription == nil || account.Subscription.Lease != nil && string(account.Subscription.Lease.ID) == l.response.LeaseId {
		return &managedExecutionUncertain{subscription.Invalid()}
	}
	l.journal.State = managedReported
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}
func runClaudeAccount(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account domain.ID, revision uint64, op domain.SubscriptionOperation) (returned error) {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	action := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	if op.Action == domain.SubscriptionRefresh {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	}
	if op.Action == domain.SubscriptionLogout {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	}
	lease, err := takeManagedSubscription(bounded, config, client, credential, instance, account, op.ID, revision, action)
	if err != nil {
		return err
	}
	cleanup, success := false, false
	var identity *pb.NativeSubscriptionIdentity
	phase := domain.NativeSubscriptionRuntime
	defer func() {
		if returned != nil {
			detected := claude.SupportedVersion
			if phase == domain.NativeSubscriptionVersion {
				detected = ""
			}
			diagnostic := &pb.NativeSubscriptionDiagnostic{DetectedVersion: detected, RequiredVersion: claude.SupportedVersion, Phase: pb.NativeSubscriptionDiagnosticPhase(phase), Code: string(domain.SafeError(returned).Code), CorrelationId: string(op.ID)}
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
	lease.journal.NativeProfileID = domain.ID(lease.response.NativeProfileId)
	if err := writeJSON(lease.journalPath, lease.journal); err != nil {
		return subscription.Invalid()
	}
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

// Admission verifies the installed original CLI against a disposable empty
// profile. It never probes a personal login or creates subscription authority.
func verifyNativeClaudeSubscriptionProfile(ctx context.Context, config Config, resource *pb.Resource) (bool, error) {
	var machine domain.Machine
	if resource == nil || domain.Decode(resource.DocumentJson, &machine) != nil {
		return false, subscription.Invalid()
	}
	var installation *domain.Installation
	for i := range machine.Installations {
		candidate := &machine.Installations[i]
		if candidate.Harness == domain.ClaudeCode && candidate.Version == claude.SupportedVersion && candidate.ProtocolVerified && candidate.State == domain.InstallationDetected && candidate.Problem == nil && candidate.Protocol != nil && candidate.Protocol.State == domain.ProtocolVerified && candidate.Protocol.Problem == nil {
			if installation != nil {
				return false, subscription.Invalid()
			}
			installation = candidate
		}
	}
	if installation == nil {
		return false, nil
	}
	base := filepath.Join(config.Root, "native-claude")
	if err := security.PrivateDir(base); err != nil {
		return false, err
	}
	root, err := os.MkdirTemp(base, "probe-")
	if err != nil {
		return false, domain.SafeError(err)
	}
	env, err := harness.PrivateRuntimeEnvironment(root)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return false, subscription.Invalid()
	}
	auth := claude.AuthConfig{Version: installation.Version, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.NewID(), Executable: installation.ResolvedPath, Cwd: root, Env: env, Logger: config.Logger}}
	err = claude.VerifyAuthVersion(ctx, auth)
	if err == nil {
		var status claude.AuthStatus
		status, err = claude.ReadAuthStatus(ctx, auth)
		if err == nil && (status.LoggedIn || status.AuthMethod != "none" || status.APIKeySource != nil) {
			err = subscription.Invalid()
		}
	}
	if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
		return false, err
	}
	if cleanupErr := subscription.CleanupRuntime(root, info); cleanupErr != nil {
		return false, cleanupErr
	}
	return err == nil, err
}

func beginClaudeSubscriptionExecution(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput) (*nativeClaudeProfile, func(bool) error, error) {
	connection := config.execution
	client, closeHTTP, err := subscriptionRPC(ctx, config, connection.Credential)
	if err != nil {
		return nil, nil, err
	}
	lease, err := takeManagedSubscription(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
	if err != nil {
		closeHTTP()
		return nil, nil, err
	}
	var profile *nativeClaudeProfile
	finish := func(joined bool) error {
		defer closeHTTP()
		var identity *pb.NativeSubscriptionIdentity
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
		finished := lease.finishClaude(identity, joined, joined)
		if profile != nil {
			if closed := profile.close(); closed != nil {
				return closed
			}
		}
		return finished
	}
	if len(lease.response.Bundle) != 0 || domain.ID(lease.response.NativeProfileId).Validate() != nil {
		clear(lease.response.Bundle)
		_ = finish(false)
		return nil, nil, subscription.Invalid()
	}
	profile, err = openClaudeProfile(config, connection.Credential, input.AccountID, domain.ID(lease.response.NativeProfileId), input.Installation, domain.ID(lease.response.LeaseId), false)
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
		return nil, nil, err
	}
	profile.managedLease = lease
	return profile, finish, nil
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
	if journal.CleanupFinish == "" {
		journal.CleanupFinish = domain.NewID()
		if err := writeJSON(path, journal); err != nil {
			return subscription.Invalid()
		}
	}
	if !journal.Cleanup || journal.NativeIdentity != "" {
		root, err := claudeProfileRoot(config.Root, credential, accountID, state.NativeProfileID)
		if err != nil {
			return err
		}
		_, statErr := os.Lstat(root)
		noNative := !journal.NativeStarted && original.Action == domain.SubscriptionLogin && errors.Is(statErr, os.ErrNotExist)
		if !noNative {
			if config.nativeClaudeInstallation == nil {
				return subscription.Invalid()
			}
			installation := *config.nativeClaudeInstallation
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
		if err := writeJSON(path, journal); err != nil {
			return subscription.Invalid()
		}
	}
	lease := &managedSubscriptionLease{client: client, credential: credential, instance: original.InstanceID, account: accountID, finishID: journal.CleanupFinish, journalPath: path, journal: journal, response: &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), GenerationId: string(original.Generation), LeaseRevision: original.Revision}}
	config.Logger.InfoContext(ctx, "claude_subscription_original_cleanup_confirmed", "lease_id", original.ID, "account_id", accountID)
	return lease.finishClaude(nil, true, false)
}
