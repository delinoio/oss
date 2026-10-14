// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

// finishRecorder captures the one Finish a native refresh reports.
type finishRecorder struct {
	delidevv1connect.SubscriptionServiceClient
	requests []*pb.FinishSubscriptionRequest
}

func (r *finishRecorder) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	r.requests = append(r.requests, proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest))
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

// nativeRefreshFixture is a refresh lease whose private home holds the delivered
// credential, as materialization leaves it before the native process runs.
type nativeRefreshFixture struct {
	lease    *managedSubscriptionLease
	recorder *finishRecorder
	original subscription.GrokBundle
	home     string
	server   domain.ID
	account  domain.ID
}

func newNativeRefreshFixture(t *testing.T) *nativeRefreshFixture {
	t.Helper()
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	envelope := mustSeal(t, server, account, generation, mustParse(t, grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z")))
	lease := grokWorkerLease(t, server, account, generation, envelope)
	recorder := &finishRecorder{}
	lease.client = recorder
	lease.journalPath = filepath.Join(t.TempDir(), "journal.json")
	lease.journal.Action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
	home := grokHome(t)
	original, err := lease.materializeGrok(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, subscription.GrokAuthFileName)); err != nil {
		t.Fatal("the delivered credential was not materialized")
	}
	return &nativeRefreshFixture{lease: lease, recorder: recorder, original: original, home: home, server: server, account: account}
}

func mustParse(t *testing.T, raw []byte) subscription.GrokBundle {
	t.Helper()
	bundle, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// nativeRewrite is what the native Grok process does when it refreshes itself:
// it rewrites auth.json and leaves its advisory lock file beside it.
func (f *nativeRefreshFixture) nativeRewrite(t *testing.T, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.home, subscription.GrokAuthFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, subscription.GrokLockFileName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *nativeRefreshFixture) finish(t *testing.T, processExited bool) *pb.FinishSubscriptionRequest {
	t.Helper()
	if err := f.lease.finishGrok(f.original, processExited); err != nil {
		t.Fatal(err)
	}
	if len(f.recorder.requests) != 1 {
		t.Fatal("the lease did not report exactly one Finish")
	}
	return f.recorder.requests[0]
}

func (f *nativeRefreshFixture) requireHomeEmpty(t *testing.T) {
	t.Helper()
	if entries, err := os.ReadDir(f.home); err != nil || len(entries) != 0 {
		t.Fatalf("the private home still holds %d entries", len(entries))
	}
}

// A native refresh that rewrote auth.json for the original identity is returned
// through Finish as the next generation, and both auth.json and its lock file
// are removed before cleanup is confirmed.
func TestNativeRefreshFinishReturnsTheRotatedCredentialAndRetiresBothFiles(t *testing.T) {
	f := newNativeRefreshFixture(t)
	f.nativeRewrite(t, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
	request := f.finish(t, true)
	if !request.Succeeded || !request.CleanupConfirmed || !request.RefreshConfirmed || len(request.Bundle) == 0 {
		t.Fatalf("a rotated credential was not reported: %+v", request)
	}
	next, err := subscription.OpenGrokBundle(request.Bundle, f.server, f.account, domain.ID(request.Mutation.RequestId))
	if err != nil || subscription.GrokRefreshed(f.original, next) != nil {
		t.Fatal("the reported bundle is not the refreshed credential bound to the next generation")
	}
	f.requireHomeEmpty(t)
}

// A refresh whose latest auth.json names another identity, cannot be read, or
// whose native process did not exit cleanly is never reported as a refresh. The
// file and its lock are still retired, so the server (which sees no
// RefreshConfirmed) keeps the lease fenced and sets RECOVERY_REQUIRED.
func TestNativeRefreshFinishNeverConfirmsAMismatchedOrUnreadableCredential(t *testing.T) {
	for name, c := range map[string]struct {
		raw           []byte
		processExited bool
	}{
		"identity mismatch": {grokWorkerFile(t, "synthetic-principal-2", "two", "2026-10-14T12:00:00Z"), true},
		"unreadable file":   {[]byte(`{"not":"a bundle"}`), true},
		"truncated file":    {[]byte(`{"https://auth.x.ai::`), true},
		"process failed":    {grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNativeRefreshFixture(t)
			f.nativeRewrite(t, c.raw)
			request := f.finish(t, c.processExited)
			if request.Succeeded || request.RefreshConfirmed || len(request.Bundle) != 0 {
				t.Fatalf("an unusable refresh was reported as usable: %+v", request)
			}
			if !request.CleanupConfirmed {
				t.Fatal("cleanup of the credential and its lock was not confirmed")
			}
			f.requireHomeEmpty(t)
		})
	}
}

// Cleanup that cannot be proven keeps the lease: no bundle, no cleanup
// confirmation, and the credential material stays reachable only in the bound
// home for recovery.
func TestNativeRefreshFinishWithholdsEverythingWhenCleanupIsUnconfirmed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	for name, remnant := range map[string]string{
		"lock file that is a directory":       subscription.GrokLockFileName,
		"interrupted temporary that is a dir": security.OwnedTemporaryPrefix(subscription.GrokAuthFileName) + "stuck",
	} {
		t.Run(name, func(t *testing.T) {
			f := newNativeRefreshFixture(t)
			if err := os.WriteFile(filepath.Join(f.home, subscription.GrokAuthFileName), grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(f.home, remnant), 0o700); err != nil {
				t.Fatal(err)
			}
			request := f.finish(t, true)
			if request.CleanupConfirmed || len(request.Bundle) != 0 {
				t.Fatalf("an unconfirmed cleanup released the credential or the lease: %+v", request)
			}
		})
	}
}

// The lease is bound to the home that was materialized: a swapped home confirms
// nothing, whatever the substitute holds.
func TestNativeRefreshFinishRefusesASwappedHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	f := newNativeRefreshFixture(t)
	f.nativeRewrite(t, grokWorkerFile(t, "synthetic-principal-1", "two", "2026-10-14T12:00:00Z"))
	moved := f.home + "-moved"
	if err := os.Rename(f.home, moved); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(f.home); err != nil {
		t.Fatal(err)
	}
	request := f.finish(t, true)
	if request.CleanupConfirmed || request.RefreshConfirmed || len(request.Bundle) != 0 {
		t.Fatalf("a swapped home was trusted: %+v", request)
	}
}
