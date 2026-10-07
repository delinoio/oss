// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func nativeWorkerFixtureURL() string {
	q := url.Values{"client_id": {"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}, "response_type": {"code"}, "redirect_uri": {"https://platform.claude.com/oauth/code/callback"}, "scope": {"user:profile user:inference user:sessions:claude_code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {strings.Repeat("b", 32)}, "code": {"true"}}
	return "https://claude.com/cai/oauth/authorize?" + q.Encode()
}

// The test binary substitutes for the original CLI only inside this explicitly
// named private fixture. It cannot inspect personal profiles or use real auth.
func init() {
	home := os.Getenv("HOME")
	if len(os.Args) < 2 || !strings.Contains(filepath.ToSlash(home), "/worker-native-claude-fixture/native-claude/") {
		return
	}
	profile := os.Getenv("CLAUDE_CONFIG_DIR")
	if profile != filepath.Join(home, "claude") || os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR") != profile {
		os.Exit(70)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "HTTPS_PROXY", "NODE_OPTIONS"} {
		if os.Getenv(key) != "" {
			os.Exit(71)
		}
	}
	if os.Args[1] == "--version" {
		fmt.Println(domain.ClaudeProtocolVersion + " (Claude Code)")
		os.Exit(0)
	}
	if len(os.Args) < 3 || os.Args[1] != "auth" {
		os.Exit(72)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(home))))
	trace, err := os.OpenFile(filepath.Join(root, "fixture-native-calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(73)
	}
	_, _ = fmt.Fprintln(trace, os.Args[2])
	_ = trace.Close()
	marker := filepath.Join(profile, "fictional-account.json")
	switch os.Args[2] {
	case "login":
		if len(os.Args) != 4 || os.Args[3] != "--claudeai" {
			os.Exit(74)
		}
		fmt.Println(nativeWorkerFixtureURL())
		fmt.Print("Paste code here if prompted > ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || scanner.Text() != "fixture-approval#original-state" {
			os.Exit(75)
		}
		files, err := filepath.Glob(filepath.Join(root, "managed-auth", "*.json"))
		if err != nil || len(files) != 1 {
			os.Exit(76)
		}
		raw, err := security.ReadPrivate(files[0], 16<<10)
		var claim managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &claim) != nil || !claim.NativeStarted || claim.CodeSubmissionID.Validate() != nil || string(claim.NativeProfileID) != filepath.Base(home) {
			os.Exit(77)
		}
		if os.WriteFile(marker, []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"worker@example.invalid","orgId":"fixture-org","subscriptionType":"pro"}`), 0600) != nil {
			os.Exit(78)
		}
		os.Exit(0)
	case "status":
		raw, err := os.ReadFile(marker)
		if err != nil {
			fmt.Println(`{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(raw)
		os.Exit(0)
	case "logout":
		_ = os.Remove(marker)
		os.Exit(0)
	}
	os.Exit(79)
}

type nativeClaudeLifecycleFixture struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu                                  sync.Mutex
	t                                   *testing.T
	config                              Config
	credential                          Credential
	installation                        domain.Installation
	account, profile, generation, lease domain.ID
	submission                          domain.ID
	takes, codeTakes                    int
	cancel, loseFinish                  bool
	finishes                            []*pb.FinishSubscriptionRequest
}

func (f *nativeClaudeLifecycleFixture) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takes++
	f.lease = domain.ID(req.Msg.Mutation.RequestId)
	raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "managed-auth", string(f.lease)+".json"), 16<<10)
	var claim managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.Lease != f.lease || claim.Operation != domain.ID(req.Msg.OperationId) || claim.State != managedClaimed || claim.NativeStarted {
		f.t.Error("native delivery preceded the original durable claim")
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	installation, _ := json.Marshal(f.installation)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(f.lease), LeaseRevision: 3, GenerationId: string(f.generation), NativeProfileId: string(f.profile), InstallationJson: installation}), nil
}

func (f *nativeClaudeLifecycleFixture) PublishSubscriptionProgress(_ context.Context, req *connect.Request[pb.PublishSubscriptionProgressRequest]) (*connect.Response[pb.PublishSubscriptionProgressResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Msg.Url != "" && (req.Msg.Url != nativeWorkerFixtureURL() || req.Msg.LoginMethod != pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE) {
		f.t.Error("native progress changed its original authority")
	}
	return connect.NewResponse(&pb.PublishSubscriptionProgressResponse{Canceled: f.cancel && req.Msg.Url != ""}), nil
}

func (f *nativeClaudeLifecycleFixture) TakeSubscriptionLoginCode(_ context.Context, req *connect.Request[pb.TakeSubscriptionLoginCodeRequest]) (*connect.Response[pb.TakeSubscriptionLoginCodeResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codeTakes++
	if req.Msg.LeaseId != string(f.lease) || req.Msg.MachineId != string(f.credential.MachineID) {
		f.t.Error("code delivery changed the original Worker")
	}
	if f.codeTakes > 1 {
		f.t.Error("approval input was fetched again after delivery")
	}
	return connect.NewResponse(&pb.TakeSubscriptionLoginCodeResponse{SubmissionId: string(f.submission), Code: []byte("fixture-approval#original-state")}), nil
}

func (f *nativeClaudeLifecycleFixture) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes = append(f.finishes, proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest))
	raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "managed-auth", string(f.lease)+".json"), 16<<10)
	var claim managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.State != managedClosed || len(req.Msg.Bundle) != 0 || req.Msg.RefreshConfirmed || !req.Msg.CleanupConfirmed {
		f.t.Error("completion did not retain its metadata-only original cleanup claim")
	}
	profile, _ := claudeProfileRoot(f.config.Root, f.credential, f.account, f.profile)
	lock, err := security.TryLock(profile + ".lock")
	if err != nil {
		f.t.Error("server completion raced the retained original profile lock")
	} else {
		_ = lock.Close()
	}
	if f.loseFinish && len(f.finishes) == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	document, _ := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{}})
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: &pb.Resource{Id: string(f.account), Revision: 4, DocumentJson: document}}), nil
}

func TestNativeClaudeWorkerLoginCancelAndOriginalRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "cancel", "lost-completion"} {
		t.Run(scenario, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "worker-native-claude-fixture")
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			f := &nativeClaudeLifecycleFixture{t: t, config: Config{Root: root, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}, credential: Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}, installation: domain.Installation{Harness: domain.ClaudeCode, Version: domain.ClaudeProtocolVersion, ResolvedPath: executable}, account: domain.NewID(), profile: domain.NewID(), generation: domain.NewID(), submission: domain.NewID(), cancel: scenario == "cancel", loseFinish: scenario == "lost-completion"}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
			server := httptest.NewServer(handler)
			defer server.Close()
			client := delidevv1connect.NewSubscriptionServiceClient(server.Client(), server.URL)
			f.credential.Endpoint = server.URL
			instance := domain.NewID()
			op := domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionLogin, MachineID: f.credential.MachineID}
			ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			err = runClaudeAccount(ctx, f.config, client, f.credential, instance, f.account, 2, op)
			if scenario == "success" && err != nil || scenario != "success" && err == nil {
				t.Fatal("native operation outcome changed", scenario, err)
			}
			// Joining the HTTP request alone is not an in-memory synchronization
			// primitive for the fixture's request observations.
			f.mu.Lock()
			locked := true
			defer func() {
				if locked {
					f.mu.Unlock()
				}
			}()
			if f.takes != 1 || len(f.finishes) != 1 {
				t.Fatal("native delivery or completion was repeated")
			}
			finish := f.finishes[0]
			if scenario == "cancel" {
				var reported *managedReportedFailure
				if !errors.As(err, &reported) || finish.Succeeded || finish.NativeIdentity != nil || f.codeTakes != 0 {
					t.Fatal("canceled login published identity or consumed code")
				}
			} else if !finish.Succeeded || finish.NativeIdentity == nil || finish.NativeIdentity.ProfileId != string(f.profile) || len(finish.NativeIdentity.IdentityCommitment) != 64 || f.codeTakes != 1 {
				t.Fatal("original process and owned status did not confirm identity")
			}
			if err := process.ReconcileOwner(filepath.Join(root, "processes"), f.lease); err != nil {
				t.Fatal("native completion left unjoined original process", err)
			}
			journalPath := filepath.Join(root, "managed-auth", string(f.lease)+".json")
			raw, err := security.ReadPrivate(journalPath, 16<<10)
			var claim managedSubscriptionJournal
			if err != nil || domain.Decode(raw, &claim) != nil {
				t.Fatal("original operation journal missing", err)
			}
			if scenario == "lost-completion" {
				if claim.State != managedClosed || claim.NativeIdentity == "" {
					t.Fatal("lost response erased original identity uncertainty")
				}
				state := &domain.SubscriptionState{RecoveryRequired: true, NativeProfileID: f.profile, OwnerMachineID: f.credential.MachineID, Lease: &domain.SubscriptionLease{ID: f.lease, OperationID: op.ID, InstanceID: instance, Generation: f.generation, Revision: 3, Action: domain.SubscriptionLogin}}
				f.config.nativeClaudeInstallation = &f.installation
				f.mu.Unlock()
				locked = false
				if err = runClaudeRecovery(ctx, f.config, client, f.credential, f.account, domain.Account{Subscription: state}); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				locked = true
				if f.takes != 1 || f.codeTakes != 1 || len(f.finishes) != 2 || f.finishes[1].NativeIdentity != nil || f.finishes[1].Succeeded || f.finishes[1].LeaseId != finish.LeaseId || f.finishes[1].Mutation.RequestId == finish.Mutation.RequestId {
					t.Fatal("recovery replaced native authority or replayed authentication")
				}
				raw, _ = security.ReadPrivate(journalPath, 16<<10)
				_ = domain.Decode(raw, &claim)
			}
			if claim.State != managedReported || !claim.Cleanup {
				t.Fatal("acknowledged native cleanup did not settle original journal")
			}
			profile, _ := claudeProfileRoot(root, f.credential, f.account, f.profile)
			if scenario != "success" {
				if _, err := os.Lstat(profile); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("canceled or recovered original profile retained native authority", err)
				}
			} else {
				f.config.execution = &PublicationConfig{Credential: f.credential, Instance: instance, Assignment: &pb.Resource{Revision: 3}}
				f.mu.Unlock()
				locked = false
				owned, complete, err := beginClaudeSubscriptionExecution(ctx, f.config, domain.NewID(), domain.ExecutionJobInput{AccountID: f.account, Installation: f.installation})
				if err != nil || owned == nil {
					t.Fatal("original native subscription execution could not acquire its profile", err)
				}
				if err = complete(true); err != nil {
					t.Fatal("joined execution did not release local ownership before server completion", err)
				}
				f.mu.Lock()
				locked = true
				if f.takes != 2 || len(f.finishes) != 2 || f.finishes[1].NativeIdentity == nil || f.finishes[1].NativeIdentity.IdentityCommitment != finish.NativeIdentity.IdentityCommitment || f.finishes[1].LeaseId == finish.LeaseId {
					t.Fatal("execution replaced the pinned native identity")
				}
				owned, err = openClaudeProfile(f.config, f.credential, f.account, f.profile, f.installation, domain.NewID(), false)
				if err != nil {
					t.Fatal(err)
				}
				defer owned.close()
				identity, err := owned.identity(ctx, f.config)
				if err != nil || identity.IdentityCommitment != finish.NativeIdentity.IdentityCommitment {
					t.Fatal("original private identity pin was not retained", err)
				}
				marker := filepath.Join(owned.home, "fictional-account.json")
				if err := os.WriteFile(marker, []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"changed@example.invalid","orgId":"fixture-org","subscriptionType":"pro"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err = owned.identity(ctx, f.config); domain.SafeError(err).Code != domain.Unauthenticated {
					t.Fatal("changed native account was accepted before execution", err)
				}
			}
			trace, err := os.ReadFile(filepath.Join(root, "fixture-native-calls"))
			if err != nil || strings.Count(string(trace), "login\n") != 1 || scenario != "success" && strings.Count(string(trace), "logout\n") != 1 {
				t.Fatal("original login or cleanup was repeated", err)
			}
			for _, data := range [][]byte{raw, logs.Bytes()} {
				if bytes.Contains(data, []byte("worker@example.invalid")) || bytes.Contains(data, []byte("fixture-approval")) || bytes.Contains(data, []byte("claude.com/cai/oauth")) {
					t.Fatal("raw native identity, approval code or URL entered durable evidence")
				}
			}
		})
	}
}

func TestNativeClaudeProfileExclusiveOriginalMachineOwnership(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Root: root}
	credential := Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}
	account, profile := domain.NewID(), domain.NewID()
	installation := domain.Installation{Harness: domain.ClaudeCode, Version: domain.ClaudeProtocolVersion, ResolvedPath: executable}
	first, err := openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), false); err == nil {
		t.Fatal("two native owners acquired one profile")
	}
	other, err := openClaudeProfile(config, credential, domain.NewID(), domain.NewID(), installation, domain.NewID(), true)
	if err != nil {
		t.Fatal("independent accounts shared an exclusive owner", err)
	}
	if first.home == other.home {
		t.Fatal("native profiles overlap")
	}
	if err = other.close(); err != nil {
		t.Fatal(err)
	}
	if err = first.close(); err != nil {
		t.Fatal(err)
	}
	foreign := credential
	foreign.MachineID = domain.NewID()
	if _, err = openClaudeProfile(config, foreign, account, profile, installation, domain.NewID(), false); err == nil {
		t.Fatal("another machine adopted native authentication")
	}
	if _, err = openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), true); err == nil {
		t.Fatal("duplicate login replaced the original profile")
	}
	original, err := openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = subscription.CleanupRuntime(original.root, original.original); err != nil {
		t.Fatal(err)
	}
	if err = original.close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(original.root); !os.IsNotExist(err) {
		t.Fatal("owned cleanup did not remove whole native profile", err)
	}
	if _, err = os.Stat(other.root); err != nil {
		t.Fatal("cleanup removed an independent profile", err)
	}
}
