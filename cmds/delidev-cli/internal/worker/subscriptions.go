package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type managedSubscriptionLease struct {
	client      delidevv1connect.SubscriptionServiceClient
	credential  Credential
	instance    domain.ID
	account     domain.ID
	response    *pb.TakeSubscriptionResponse
	finishID    domain.ID
	journalPath string
	journal     managedSubscriptionJournal
}

type managedJournalState string

const (
	managedClaimed  managedJournalState = "claimed"
	managedClosed   managedJournalState = "closed"
	managedReported managedJournalState = "reported"
)

type managedSubscriptionJournal struct {
	Version                                                 uint32 `json:"version"`
	Lease, Account, Operation, Instance, Finish, Generation domain.ID
	Action                                                  pb.SubscriptionAction
	State                                                   managedJournalState
	Cleanup, Refresh, Success                               bool
}

func subscriptionRPC(credential Credential) (delidevv1connect.SubscriptionServiceClient, func()) {
	httpClient, transport := rpc.HTTPClient()
	return delidevv1connect.NewSubscriptionServiceClient(httpClient, credential.Endpoint, connect.WithReadMaxBytes(128<<10), connect.WithSendMaxBytes(128<<10)), transport.CloseIdleConnections
}

func takeManagedSubscription(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account, operation domain.ID, revision uint64, action pb.SubscriptionAction) (*managedSubscriptionLease, error) {
	leaseID := domain.NewID()
	root := filepath.Join(config.Root, "managed-auth")
	if err := security.PrivateDir(root); err != nil {
		return nil, domain.SafeError(err)
	}
	// A durable claim precedes the protected delivery and every native side
	// effect. Reconnect never regenerates this claim or replays the login.
	claim := managedSubscriptionJournal{Version: 1, Lease: leaseID, Account: account, Operation: operation, Instance: instance, Finish: domain.NewID(), Action: action, State: managedClaimed}
	journalPath := filepath.Join(root, string(leaseID)+".json")
	if err := writeJSON(journalPath, claim); err != nil {
		return nil, subscription.Invalid()
	}
	var response *connect.Response[pb.TakeSubscriptionResponse]
	var err error
	for {
		response, err = client.TakeSubscription(ctx, authenticated(credential, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(leaseID), Id: string(account), ExpectedRevision: revision}, MachineId: string(credential.MachineID), InstanceId: string(instance), OperationId: string(operation), Action: action}))
		if err == nil {
			break
		}
		problem := rpc.ClientError(err)
		if action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE || domain.SafeError(problem).Code != domain.ResourceExhausted {
			code := domain.SafeError(problem).Code
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE && (code == domain.ServerUnavailable || code == domain.Unavailable || code == domain.Canceled || code == domain.Internal || code == domain.RecoveryRequired) {
				return nil, &managedExecutionUncertain{problem}
			}
			return nil, problem
		}
		// A definite busy refusal has no delivery or native side effect. Wait on
		// the exact selected account using the unchanged claim; unknown delivery
		// never enters this retry path.
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, domain.SafeError(ctx.Err())
		case <-timer.C:
		}
	}
	if response.Msg.LeaseId != string(leaseID) || response.Msg.LeaseRevision == 0 {
		clear(response.Msg.Bundle)
		return nil, &managedExecutionUncertain{subscription.Invalid()}
	}
	claim.Generation = domain.ID(response.Msg.GenerationId)
	if err := writeJSON(journalPath, claim); err != nil {
		clear(response.Msg.Bundle)
		return nil, &managedExecutionUncertain{subscription.Invalid()}
	}
	return &managedSubscriptionLease{client: client, credential: credential, instance: instance, account: account, response: response.Msg, finishID: claim.Finish, journalPath: journalPath, journal: claim}, nil
}

func (l *managedSubscriptionLease) finish(bundle []byte, cleanup, refresh, success bool) error {
	defer clear(bundle)
	l.journal.State = managedClosed
	l.journal.Cleanup = cleanup
	l.journal.Refresh = refresh
	l.journal.Success = success
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := l.client.FinishSubscription(ctx, authenticated(l.credential, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(l.finishID), Id: string(l.account), ExpectedRevision: l.response.LeaseRevision}, LeaseId: l.response.LeaseId, GenerationId: l.response.GenerationId, MachineId: string(l.credential.MachineID), InstanceId: string(l.instance), Bundle: bundle, CleanupConfirmed: cleanup, RefreshConfirmed: refresh, Succeeded: success}))
	if err != nil {
		return rpc.ClientError(err)
	}
	l.journal.State = managedReported
	if err := writeJSON(l.journalPath, l.journal); err != nil {
		return subscription.Invalid()
	}
	return nil
}

// A failed operation with acknowledged completion no longer owns a lease.
// It must not interrupt unrelated accounts still running on this lane.
type managedReportedFailure struct{ error }

func (e *managedReportedFailure) Unwrap() error { return e.error }

// Protected execution completion cannot become an ordinary reported job error:
// losing its acknowledgment must close the primary lane and fence the lease.
type managedExecutionUncertain struct{ error }

func (e *managedExecutionUncertain) Unwrap() error { return e.error }

func watchSubscriptions(ctx context.Context, config Config, credential Credential, instance domain.ID) (returned error) {
	// The stream and child operations share cancellation so uncertain delivery
	// closes the server's ownership lane and triggers lost-lease reconciliation.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client, closeRPC := subscriptionRPC(credential)
	defer closeRPC()
	stream, err := client.WatchSubscription(ctx, authenticated(credential, &pb.WatchSubscriptionRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		return err
	}
	defer stream.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	active := map[domain.ID]bool{}
	failures := make(chan error, 1)
	defer func() {
		cancel()
		wg.Wait()
		select {
		case err := <-failures:
			returned = err
		default:
		}
	}()
	for stream.Receive() {
		r := stream.Msg().Account
		if r == nil {
			continue
		}
		var account domain.Account
		if domain.Decode(r.DocumentJson, &account) != nil || account.Subscription == nil || account.Subscription.Pending == nil {
			return subscription.Invalid()
		}
		op := *account.Subscription.Pending
		mu.Lock()
		if active[op.ID] || len(active) >= 4 {
			mu.Unlock()
			continue
		}
		active[op.ID] = true
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { mu.Lock(); delete(active, op.ID); mu.Unlock() }()
			if err := runManagedAccount(ctx, config, client, credential, instance, domain.ID(r.Id), r.Revision, op); err != nil {
				if config.Logger != nil {
					config.Logger.WarnContext(ctx, "managed_subscription_requires_reconciliation", "account_id", r.Id, "operation_id", op.ID, "action", op.Action, "code", domain.SafeError(err).Code)
				}
				var reported *managedReportedFailure
				if !errors.As(err, &reported) {
					select {
					case failures <- err:
					default:
					}
					cancel()
				}
			}
		}()
	}
	return stream.Err()
}

func runManagedAccount(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, instance, account domain.ID, revision uint64, op domain.SubscriptionOperation) (returned error) {
	action := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	if op.Action == domain.SubscriptionRefresh {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	}
	if op.Action == domain.SubscriptionLogout {
		action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	lease, err := takeManagedSubscription(ctx, config, client, credential, instance, account, op.ID, revision, action)
	if err != nil {
		return err
	}
	defer clear(lease.response.Bundle)
	var latest []byte
	cleanup, refresh, success := false, false, false
	defer func() {
		if err := lease.finish(latest, cleanup, refresh, success); err != nil {
			returned = err
		} else if returned != nil {
			returned = &managedReportedFailure{returned}
		}
		clear(latest)
	}()
	var installation domain.Installation
	if domain.Decode(lease.response.InstallationJson, &installation) != nil || installation.Harness != domain.Codex || installation.Version != codex.SupportedVersion {
		return subscription.Invalid()
	}
	executable, err := filepath.EvalSymlinks(installation.ResolvedPath)
	if err != nil || executable != installation.ResolvedPath || !filepath.IsAbs(executable) {
		return subscription.Invalid()
	}
	home := filepath.Join(config.Root, "managed-auth", lease.response.LeaseId)
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return subscription.Invalid()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return domain.SafeError(err)
	}
	homeInfo, err := os.Stat(home)
	if err != nil {
		return subscription.Invalid()
	}
	nativeHome := filepath.Join(home, "codex")
	if len(lease.response.Bundle) > 0 {
		if _, _, err := subscription.Parse(lease.response.Bundle); err != nil {
			return err
		}
		if err := security.WriteAtomic(filepath.Join(nativeHome, "auth.json"), lease.response.Bundle); err != nil {
			return subscription.Invalid()
		}
	}
	native, nativeErr := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: nativeHome, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.ID(lease.response.LeaseId), Executable: executable, Cwd: home, Env: env, Logger: config.Logger}})
	if nativeErr != nil {
		// Open reports recovery-required if its independently joined cleanup
		// could not be proved. Never release that lease on an optimistic unlink.
		if domain.SafeError(nativeErr).Code != domain.RecoveryRequired {
			cleanup = cleanupManagedHome(home, homeInfo) == nil
		}
		return nativeErr
	}
	defer func() {
		if err := native.Close(); err != nil {
			returned = domain.SafeError(err)
			cleanup = false
			return
		}
		if success && op.Action != domain.SubscriptionLogout {
			closed, err := security.ReadPrivate(filepath.Join(nativeHome, "auth.json"), subscription.MaxBundle)
			if err != nil || !bytes.Equal(closed, latest) {
				clear(closed)
				returned = subscription.Invalid()
				success = false
			} else {
				clear(closed)
			}
		}
		cleanup = cleanupManagedHome(home, homeInfo) == nil
		if !cleanup {
			returned = subscription.Invalid()
		}
	}()
	if op.Action == domain.SubscriptionLogin {
		progress, err := native.StartManagedLogin(ctx, op.DeviceCode)
		if err != nil {
			return err
		}
		loginCtx, stopLogin := context.WithCancel(ctx)
		pollDone := make(chan struct{})
		go func() {
			defer close(pollDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				report, err := client.PublishSubscriptionProgress(loginCtx, authenticated(credential, &pb.PublishSubscriptionProgressRequest{AccountId: string(account), LeaseId: lease.response.LeaseId, MachineId: string(credential.MachineID), InstanceId: string(instance), Url: progress.URL, UserCode: progress.UserCode}))
				if err != nil || report.Msg.Canceled {
					stopLogin()
					return
				}
				select {
				case <-loginCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		err = native.WaitManagedLogin(loginCtx, progress.LoginID)
		stopLogin()
		<-pollDone
		if err != nil {
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			cancelErr := native.CancelManagedLogin(bounded, progress.LoginID)
			stop()
			if cancelErr != nil {
				return subscription.Invalid()
			}
			return err
		}
	}
	if op.Action == domain.SubscriptionLogout {
		if err := native.LogoutManaged(ctx); err != nil {
			return err
		}
		success = true
		return nil
	}
	latest, err = native.ManagedBundle(ctx, op.Action == domain.SubscriptionRefresh)
	if err != nil {
		return err
	}
	refresh = op.Action == domain.SubscriptionRefresh
	success = true
	return nil
}

func cleanupManagedHome(home string, original os.FileInfo) error {
	if err := security.CheckPrivateDir(home); err != nil {
		return subscription.Invalid()
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return subscription.Invalid()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return subscription.Invalid()
	}
	defer root.Close()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(anchored, original) {
		return subscription.Invalid()
	}
	count, total := 0, int64(0)
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return subscription.Invalid()
		}
		if entry.IsDir() {
			return nil
		}
		count++
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || count > 2048 {
			return subscription.Invalid()
		}
		total += info.Size()
		if total > 64<<20 {
			return subscription.Invalid()
		}
		return nil
	}); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return subscription.Invalid()
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.Name()); err != nil {
			return subscription.Invalid()
		}
	}
	current, err = os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return subscription.Invalid()
	}
	if err := os.Remove(home); err != nil {
		return subscription.Invalid()
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		return subscription.Invalid()
	}
	if err := security.SyncParent(home); err != nil {
		return subscription.Invalid()
	}
	return nil
}

// Only the caller's pre-native barrier permits an unpublished auth destination.
// A failed atomic write still needs a durable absence and the complete retained
// file scan: a leftover temporary credential file cannot release the lease.
func cleanupUnusedExecutionAuthentication(home string, original []byte) error {
	if err := security.CheckPrivateDir(home); err != nil {
		return subscription.Invalid()
	}
	path := filepath.Join(home, "auth.json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return cleanupExecutionAuthentication(home, original, original)
	}
	if err := security.SyncParent(path); err != nil {
		return subscription.Invalid()
	}
	if err := scanExecutionAuthentication(home, original, original); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return subscription.Invalid()
	}
	return nil
}

func cleanupExecutionAuthentication(home string, latest, original []byte) error {
	if err := security.CheckPrivateDir(home); err != nil {
		return subscription.Invalid()
	}
	closed, err := security.ReadPrivate(filepath.Join(home, "auth.json"), subscription.MaxBundle)
	if err != nil || len(latest) == 0 || !bytes.Equal(closed, latest) {
		clear(closed)
		return subscription.Invalid()
	}
	clear(closed)
	if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
		return subscription.Invalid()
	}
	if err := security.SyncParent(filepath.Join(home, "auth.json")); err != nil {
		return subscription.Invalid()
	}
	return scanExecutionAuthentication(home, latest, original)
}

func scanExecutionAuthentication(home string, latest, original []byte) error {
	// Native history is retained for resume. Check the bounded original tree
	// rather than claiming credential cleanup merely from auth.json absence.
	var needles [][]byte
	defer func() {
		for _, n := range needles {
			clear(n)
		}
	}()
	for _, raw := range [][]byte{original, latest} {
		b, _, err := subscription.Parse(raw)
		if err != nil {
			return err
		}
		for _, v := range []string{b.Tokens.ID, b.Tokens.Access, b.Tokens.Refresh} {
			plain := []byte(v)
			needles = append(needles, plain)
			// Retained native files may encode credentials. Cleanup must reject
			// padded and unpadded standard/URL Base64 copies as well as raw tokens.
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				encoded := make([]byte, encoding.EncodedLen(len(plain)))
				encoding.Encode(encoded, plain)
				needles = append(needles, encoded)
			}
		}
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return subscription.Invalid()
	}
	defer root.Close()
	originalInfo, err := root.Stat(".")
	if err != nil {
		return subscription.Invalid()
	}
	count, total := 0, int64(0)
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return subscription.Invalid()
		}
		if entry.IsDir() {
			return nil
		}
		count++
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || count > 2048 || info.Size() > 16<<20 {
			return subscription.Invalid()
		}
		total += info.Size()
		if total > 64<<20 {
			return subscription.Invalid()
		}
		file, err := root.Open(path)
		if err != nil {
			return subscription.Invalid()
		}
		raw, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || len(raw) > 16<<20 {
			clear(raw)
			return subscription.Invalid()
		}
		defer clear(raw)
		for _, needle := range needles {
			if bytes.Contains(raw, needle) {
				return subscription.Invalid()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(originalInfo, current) {
		return subscription.Invalid()
	}
	return nil
}

func managedSubscriptionCapability(resource *pb.Resource) bool {
	if resource == nil {
		return false
	}
	var m domain.Machine
	return domain.Decode(resource.DocumentJson, &m) == nil && m.Validate() == nil && slicesContainManagedCapability(m.WorkerCapabilities)
}

func slicesContainManagedCapability(values []domain.WorkerCapability) bool {
	for _, v := range values {
		if v == domain.ManagedCodexSubscriptionsV1 {
			return true
		}
	}
	return false
}

// Capability probing is a read-only empty-home handshake. It neither starts a
// login nor reads host credentials and does not establish real-account support.
func verifyManagedSubscriptionProfile(ctx context.Context, config Config, executable string) (bool, error) {
	root := filepath.Join(config.Root, "managed-auth")
	if err := security.PrivateDir(root); err != nil {
		return false, domain.SafeError(err)
	}
	home, err := os.MkdirTemp(root, "probe-")
	if err != nil {
		return false, domain.SafeError(err)
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return false, domain.SafeError(err)
	}
	info, err := os.Stat(home)
	if err != nil {
		return false, subscription.Invalid()
	}
	client, err := codex.Open(ctx, codex.Config{Version: codex.SupportedVersion, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: filepath.Join(home, "codex"), Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.NewID(), Executable: executable, Cwd: home, Env: env, Logger: config.Logger}})
	if err != nil {
		if domain.SafeError(err).Code != domain.RecoveryRequired {
			_ = cleanupManagedHome(home, info)
		}
		return false, err
	}
	if err := client.Close(); err != nil {
		return false, domain.SafeError(err)
	}
	if err := cleanupManagedHome(home, info); err != nil {
		return false, err
	}
	return true, nil
}
