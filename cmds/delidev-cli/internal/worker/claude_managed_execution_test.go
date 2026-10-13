// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

const workerManagedToken = "sk-ant-oat01-WorkerFixtureTokenABCDEFGHIJKLMNOP0123456789_-"

// managedClaudeFixture is the protected lease side of a server-token account.
type managedClaudeFixture struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu                         sync.Mutex
	t                          *testing.T
	config                     Config
	credential                 Credential
	account, generation, lease domain.ID
	reboundGeneration          domain.ID
	profileLeak                bool
	finishes                   []*pb.FinishSubscriptionRequest
	logs                       *bytes.Buffer
}

func (f *managedClaudeFixture) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lease = domain.ID(req.Msg.Mutation.RequestId)
	if req.Msg.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		f.t.Error("a Worker requested a server-owned lifecycle operation")
	}
	raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "managed-auth", string(f.lease)+".json"), 16<<10)
	var claim managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.State != managedClaimed {
		f.t.Error("protected delivery preceded the original durable claim")
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	generation := f.generation
	if f.reboundGeneration != "" {
		generation = f.reboundGeneration
	}
	envelope, err := subscription.SealClaudeToken(f.credential.ServerID, f.account, generation, []byte(workerManagedToken))
	if err != nil {
		f.t.Fatal(err)
	}
	response := &pb.TakeSubscriptionResponse{LeaseId: string(f.lease), LeaseRevision: 3, GenerationId: string(f.generation), Bundle: envelope}
	if f.profileLeak {
		response.NativeProfileId = string(domain.NewID())
	}
	return connect.NewResponse(response), nil
}

func (f *managedClaudeFixture) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes = append(f.finishes, proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest))
	document, _ := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{}})
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: &pb.Resource{Id: string(f.account), Revision: 4, DocumentJson: document}}), nil
}

func newManagedClaudeFixture(t *testing.T) (*managedClaudeFixture, delidevv1connect.SubscriptionServiceClient) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	f := &managedClaudeFixture{t: t, logs: logs, config: Config{Root: root, Logger: slog.New(slog.NewJSONHandler(logs, nil))}, credential: Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}, account: domain.NewID(), generation: domain.NewID()}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.credential.Endpoint = server.URL
	return f, delidevv1connect.NewSubscriptionServiceClient(server.Client(), server.URL)
}

func (f *managedClaudeFixture) begin(t *testing.T) (*managedClaudeToken, func(bool) error, error) {
	t.Helper()
	f.config.execution = &PublicationConfig{Credential: f.credential, Instance: domain.NewID(), Assignment: &pb.Resource{Revision: 2}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	profile, managed, finish, err := beginClaudeSubscriptionExecution(ctx, f.config, domain.NewID(), domain.ExecutionJobInput{AccountID: f.account})
	if profile != nil {
		t.Fatal("a server-token lease opened a device-owned profile")
	}
	return managed, finish, err
}

func TestManagedClaudeLeaseDeliversTokenOnlyInMemoryAndReportsJoinedCleanup(t *testing.T) {
	f, _ := newManagedClaudeFixture(t)
	managed, finish, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if string(managed.token) != workerManagedToken {
		t.Fatal("the sealed generation was not delivered")
	}
	held := managed.token
	// A protected execution journal exists, but no file may contain the token.
	if err := filepath.WalkDir(f.config.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Contains(raw, []byte(workerManagedToken)) {
			t.Fatalf("the token reached %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(held, make([]byte, len(held))) || managed.token != nil {
		t.Fatal("the token buffer survived joined termination")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishes) != 1 || !f.finishes[0].CleanupConfirmed || !f.finishes[0].Succeeded || f.finishes[0].NativeIdentity != nil || len(f.finishes[0].Bundle) != 0 || f.finishes[0].LeaseId != string(f.lease) {
		t.Fatal("completion was not a metadata-only original cleanup claim")
	}
	if strings.Contains(f.logs.String(), workerManagedToken) {
		t.Fatal("the token entered logs")
	}
}

func TestManagedClaudeLeaseUncertainTerminationFencesAndForeignEnvelopesFail(t *testing.T) {
	f, _ := newManagedClaudeFixture(t)
	_, finish, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if len(f.finishes) != 1 || f.finishes[0].CleanupConfirmed || f.finishes[0].Succeeded {
		t.Fatal("unjoined termination claimed cleanup")
	}
	f.mu.Unlock()
	for name, mutate := range map[string]func(*managedClaudeFixture){
		"generation":   func(f *managedClaudeFixture) { f.reboundGeneration = domain.NewID() },
		"profile leak": func(f *managedClaudeFixture) { f.profileLeak = true },
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newManagedClaudeFixture(t)
			mutate(f)
			managed, _, err := f.begin(t)
			if err == nil || managed != nil {
				t.Fatal("a foreign envelope reached the supervised child")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.finishes) != 1 || f.finishes[0].CleanupConfirmed {
				t.Fatal("a rejected envelope was not fenced")
			}
		})
	}
}

func TestManagedClaudeRecoveryReportsOnlyOriginalCleanup(t *testing.T) {
	f, client := newManagedClaudeFixture(t)
	_, finish, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the child started: the lease and journal survive.
	path := filepath.Join(f.config.Root, "managed-auth", string(f.lease)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil {
		t.Fatal("original journal missing", err)
	}
	_ = finish
	state := &domain.SubscriptionState{ClaudeCredential: domain.ClaudeCredentialServerToken, RecoveryRequired: true, Generation: f.generation, Lease: &domain.SubscriptionLease{ID: f.lease, OperationID: journal.Operation, InstanceID: journal.Instance, MachineID: f.credential.MachineID, Generation: f.generation, Revision: 3, Action: domain.SubscriptionExecute}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	account := domain.Account{Subscription: state}
	f.mu.Lock()
	f.finishes = nil
	f.mu.Unlock()
	if err := runManagedClaudeRecovery(ctx, f.config, client, f.credential, f.account, account); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishes) != 1 || !f.finishes[0].CleanupConfirmed || f.finishes[0].Succeeded || f.finishes[0].NativeIdentity != nil {
		t.Fatal("recovery reported anything but original cleanup")
	}
	// Another machine's recovery state is never adopted.
	other := *state.Lease
	other.MachineID = domain.NewID()
	state.Lease = &other
	if runManagedClaudeRecovery(ctx, f.config, client, f.credential, f.account, account) == nil {
		t.Fatal("a foreign Worker cleaned a lease it did not own")
	}
}
