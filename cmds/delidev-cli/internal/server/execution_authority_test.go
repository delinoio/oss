package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type authorityFixture struct {
	service               *Service
	http                  *httptest.Server
	client                delidevv1connect.WorkerServiceClient
	workerToken, token    string
	job, device, instance domain.ID
	input                 domain.ExecutionJobInput
	register              *pb.RegisterExecutionRequest
}

func newAuthorityFixture(t *testing.T, upstream string) *authorityFixture {
	return newConfiguredAuthorityFixture(t, upstream, nil, false)
}

func newHarnessAuthorityFixture(t *testing.T, upstream string, harness domain.Harness) *authorityFixture {
	t.Helper()
	protocol := domain.OpenAIResponses
	if harness == domain.OpenCode {
		protocol = domain.OpenAIChat
	} else if harness == domain.ClaudeCode {
		protocol = domain.AnthropicMessages
	}
	return newProfileAuthorityFixture(t, upstream, harness, protocol, nil, false)
}

func newConfiguredAuthorityFixture(t *testing.T, upstream string, configure func(*domain.ExecutionJobInput), queued bool) *authorityFixture {
	return newProfileAuthorityFixture(t, upstream, domain.Codex, domain.OpenAIResponses, configure, queued)
}

func newProfileAuthorityFixture(t *testing.T, upstream string, harness domain.Harness, protocol domain.APIProtocol, configure func(*domain.ExecutionJobInput), queued bool, nativeModels ...string) *authorityFixture {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	workerToken, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	rawToken, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	f := &authorityFixture{workerToken: workerToken, token: apiproxy.TokenPrefix + rawToken, job: domain.NewID(), device: domain.NewID(), instance: domain.NewID()}
	providerID, modelID, agentID, accountID, connectionID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	nativeModel := "fixture-model"
	if len(nativeModels) > 1 {
		t.Fatal("fixture accepts one explicit native model")
	}
	if len(nativeModels) == 1 {
		nativeModel = nativeModels[0]
	}
	model := domain.Model{Name: "Fixture", NativeID: nativeModel, ProviderID: providerID, Harnesses: []domain.Harness{harness}, MetadataSource: domain.UserDeclared}
	permission, version := domain.PermissionReadOnly, domain.CodexProtocolVersion
	if harness == domain.GrokBuild {
		permission, version = domain.PermissionDefault, domain.GrokProtocolVersion
	} else if harness == domain.OpenCode {
		permission, version = domain.PermissionDefault, domain.OpenCodeProtocolVersion
	} else if harness == domain.ClaudeCode {
		permission, version = domain.PermissionDefault, domain.ClaudeProtocolVersion
	}
	agent := domain.Agent{Name: "Fixture", Harness: harness, ModelID: modelID, Accounts: []domain.WeightedAccount{{ID: accountID, Weight: 1}}, Options: domain.AgentOptions{Permission: permission}}
	configuration, err := domain.ResolveExecutionConfiguration(agentID, 1, agent, 1, model, domain.Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	f.input = domain.ExecutionJobInput{Version: 1, SessionID: domain.NewID(), MachineID: domain.NewID(), ExecutionID: domain.NewID(), InputID: domain.NewID(), ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), Configuration: configuration, ConfigurationDigest: digest, AccountID: accountID, ConnectionID: connectionID, Input: domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Fixture prompt"}, Installation: domain.Installation{Harness: harness, State: domain.InstallationDetected, Version: version, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(harness), State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
	if configure != nil {
		configure(&f.input)
	}
	secrets := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	secrets.values[credentials.Ref{Owner: accountID, ID: connectionID, Purpose: credentials.AccountAPI}] = []byte("temporary-upstream-fixture-key")
	f.service = &Service{Store: s, Identity: security.Identity{ServerID: domain.NewID(), Token: "fixture-owner-token"}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), accountSecrets: secrets}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.execution-authority", f.job, func(tx *store.Tx) (any, error) {
		put := func(kind domain.Kind, id domain.ID, session domain.ID, value any) error {
			_, err := tx.Put(kind, id, 0, session, "", value)
			return err
		}
		for _, row := range []struct {
			kind  domain.Kind
			id    domain.ID
			value any
		}{
			{domain.ProviderKind, providerID, domain.Provider{Name: "Fixture", Endpoint: upstream, Protocol: protocol, Authentication: domain.BearerAuth}},
			{domain.ModelKind, modelID, model}, {domain.AgentKind, agentID, agent},
			{domain.AccountKind, accountID, domain.Account{Alias: "Fixture", ProviderID: providerID, Type: domain.APIAccount, Enabled: true, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: connectionID, Authentication: domain.BearerAuth, ConnectedAt: time.Now().UTC()}}},
			{domain.MachineKind, f.input.MachineID, domain.Machine{Name: "Fixture", OS: "linux", Architecture: "arm64"}},
			{domain.DeviceKind, f.device, domain.Device{Name: "Fixture Worker", Type: domain.WorkerDevice, MachineID: f.input.MachineID}},
		} {
			if err := put(row.kind, row.id, "", row.value); err != nil {
				return nil, err
			}
		}
		workerDigest := sha256.Sum256([]byte(workerToken))
		if err := tx.PutCredential(f.device, workerDigest[:]); err != nil {
			return nil, err
		}
		seen := time.Now().UTC()
		if queued {
			// The real Worker must be able to acquire its own instance lease.
			seen = seen.Add(-2 * time.Minute)
		}
		if err := tx.SetWorkerInstance(f.input.MachineID, f.instance, seen); err != nil {
			return nil, err
		}
		initial := domain.InitialExecution{ID: f.input.ExecutionID, InputID: f.input.InputID, Configuration: f.input.Configuration, ConfigurationDigest: f.input.ConfigurationDigest, InitialAccountID: accountID, ConnectionID: connectionID, AcceptedAt: time.Now().UTC()}
		if err := put(domain.SessionKind, f.input.SessionID, f.input.SessionID, domain.Session{Name: "Fixture", AgentID: agentID, MachineID: f.input.MachineID, Workspace: domain.GeneralChat, Outcome: domain.ExecutionRunning, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchClaimed, ActiveExecutionID: f.input.ExecutionID, InitialExecution: &initial}); err != nil {
			return nil, err
		}
		if err := put(domain.QueueKind, f.input.InputID, f.input.SessionID, domain.QueuedInput{Sequence: 1, ContentRevision: 1, Prompt: f.input.Input.Prompt, Mode: f.input.Input.Mode, Delivery: domain.InputAccepted, ExecutionID: f.input.ExecutionID, NativeRequestID: f.input.TurnRequestID}); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(f.input)
		state, instance, device := domain.JobClaimed, f.instance, f.device
		if queued {
			state, instance, device = domain.JobQueued, "", ""
		}
		return tx.PutJob(f.job, 0, f.input.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: state, MachineID: f.input.MachineID, InstanceID: instance, AssignedDeviceID: device, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(f.service.Handler(nil, true))
	t.Cleanup(func() { f.service.executionAuthority.cancel(); f.http.Close(); f.service.executionAuthority.close() })
	f.client = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.http.URL)
	tokenDigest := sha256.Sum256([]byte(f.token))
	f.register = &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CredentialDigest: tokenDigest[:]}
	return f
}

func (f *authorityFixture) registerGrant(t *testing.T) *pb.RegisterExecutionResponse {
	t.Helper()
	r := connect.NewRequest(f.register)
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	response, err := f.client.RegisterExecution(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func (f *authorityFixture) request(t *testing.T, token, body string) *http.Response {
	t.Helper()
	return f.requestPath(t, token, "/responses", body)
}

func (f *authorityFixture) operationPath() string {
	if f.input.Configuration.Harness == domain.OpenCode {
		return "/chat/completions"
	} else if f.input.Configuration.Harness == domain.ClaudeCode {
		return "/messages"
	}
	return "/responses"
}

func (f *authorityFixture) requestPath(t *testing.T, token, path, body string) *http.Response {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, f.http.URL+apiproxy.Prefix+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestExecutionGrantRPCAndRelayRetainOnlyScopedAuthority(t *testing.T) {
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
			t.Error("upstream request escaped its selected account or operation")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_fixture","object":"response","status":"completed","output":[]}`)
	}))
	defer upstream.Close()
	f := newAuthorityFixture(t, upstream.URL)
	if response := f.registerGrant(t); response.ProxyPath != apiproxy.Prefix || response.Replayed {
		t.Fatal("execution registration did not return the fixed private route")
	}
	if !f.registerGrant(t).Replayed {
		t.Fatal("registration retry replaced its binding")
	}
	for _, token := range []string{f.workerToken, f.service.Identity.Token} {
		response := f.request(t, token, `{"model":"fixture-model"}`)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal("ordinary credential became an inference key")
		}
	}
	response := f.request(t, f.token, `{"model":"fixture-model"}`)
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("resp_fixture")) {
		t.Fatalf("scoped relay failed: %d %v", response.StatusCode, err)
	}
	response = f.request(t, f.token, `{"model":"fixture-model","previous_response_id":"resp_fixture"}`)
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatal("owned response continuity was lost")
	}
	response = f.request(t, f.token, `{"model":"fixture-model","previous_response_id":"resp_foreign"}`)
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusNotFound || requests.Load() != 2 {
		t.Fatal("foreign response reached the provider")
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.cancel-execution", f.job, func(tx *store.Tx) (any, error) { return f.job, tx.RequestJobCancellation(f.job) })
	if err != nil {
		t.Fatal(err)
	}
	response = f.request(t, f.token, `{"model":"fixture-model"}`)
	if response.StatusCode != http.StatusForbidden || requests.Load() != 2 {
		t.Fatal("canceled execution retained inference authority")
	}
	_, err = f.client.RegisterExecution(context.Background(), ownerRequest(f.service.Identity, f.register))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Stop allowed a fresh inference credential", err)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		retained, err := tx.ExecutionGrant(f.register.CredentialDigest)
		if err == nil && retained.JobID != f.job {
			t.Fatal("Stop rewrote historical grant identity")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderDisableBlocksNewGrantButPreservesRegisteredTurnReplay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_after_provider_off","object":"response","status":"completed","output":[]}`)
	}))
	defer upstream.Close()
	f := newAuthorityFixture(t, upstream.URL)
	if response := f.registerGrant(t); response.Replayed {
		t.Fatal("first registered turn unexpectedly replayed")
	}
	if err := setAuthorityProviderEnabled(f, false); err != nil {
		t.Fatal(err)
	}
	if response := f.registerGrant(t); !response.Replayed {
		t.Fatal("exact registered-grant replay was not observational after disable")
	}
	response := f.request(t, f.token, `{"model":"fixture-model"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatal("provider disable revoked the original registered turn", response.StatusCode)
	}

	blocked := newAuthorityFixture(t, upstream.URL)
	if err := setAuthorityProviderEnabled(blocked, false); err != nil {
		t.Fatal(err)
	}
	r := connect.NewRequest(blocked.register)
	r.Header().Set("Authorization", "Bearer "+blocked.workerToken)
	_, err := blocked.client.RegisterExecution(context.Background(), r)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || rpc.ClientError(err).Code != domain.ProviderDisabled {
		t.Fatalf("disabled provider received a fresh execution grant: %v", err)
	}
	err = blocked.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecutionGrant(blocked.register.CredentialDigest)
		return err
	})
	if domain.SafeError(err).Code != domain.Unauthenticated {
		t.Fatalf("failed first grant left durable authority: %v", err)
	}
}

func setAuthorityProviderEnabled(f *authorityFixture, enabled bool) error {
	providerID := f.input.Configuration.ProviderID
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.provider-activation", providerID, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.ProviderKind, providerID)
		if err != nil {
			return nil, err
		}
		provider, err := store.Decode[domain.Provider](record)
		if err != nil {
			return nil, err
		}
		provider.SetEnabled(enabled)
		return tx.Put(domain.ProviderKind, providerID, record.Revision, "", "", provider)
	})
	return err
}

func TestExecutionGrantCannotSurviveEpochReplacementOrAccountRevocation(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.OpenCode, domain.ClaudeCode} {
		t.Run(string(harness), func(t *testing.T) { testExecutionGrantCannotSurviveEpochReplacementOrAccountRevocation(t, harness) })
	}
}

func testExecutionGrantCannotSurviveEpochReplacementOrAccountRevocation(t *testing.T, harness domain.Harness) {
	f := newHarnessAuthorityFixture(t, "http://127.0.0.1:1", harness)
	f.registerGrant(t)
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	newAuthority := newExecutionAuthority(f.service)
	defer newAuthority.close()
	if _, err := newAuthority.Acquire(context.Background(), f.token); err == nil {
		t.Fatal("server replacement inherited a prior process grant")
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revoke-account", f.input.AccountID, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Health = domain.AccountRevoked
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.Context.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("live lease did not observe account revocation")
	}
	if _, err := lease.Key(context.Background()); err == nil {
		t.Fatal("revoked lease retrieved upstream key")
	}
	r := connect.NewRequest(f.register)
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	if _, err := f.client.RegisterExecution(context.Background(), r); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("old registration receipt reauthorized revoked execution")
	}
}

func TestExecutionAccountRevocationJoinsActiveProviderRequest(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.OpenCode, domain.ClaudeCode} {
		t.Run(string(harness), func(t *testing.T) { testExecutionAccountRevocationJoinsActiveProviderRequest(t, harness) })
	}
}

func testExecutionAccountRevocationJoinsActiveProviderRequest(t *testing.T, harness domain.Harness) {
	started, ended := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	defer upstream.Close()
	f := newHarnessAuthorityFixture(t, upstream.URL, harness)
	f.registerGrant(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := http.NewRequest(http.MethodPost, f.http.URL+apiproxy.Prefix+f.operationPath(), strings.NewReader(`{"model":"fixture-model"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+f.token)
		response, err := http.DefaultClient.Do(r)
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy request did not reach fixture")
	}
	client := delidevv1connect.NewAccountServiceClient(http.DefaultClient, f.http.URL)
	response, err := client.DisconnectAccount(context.Background(), ownerRequest(f.service.Identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}}))
	if err != nil || len(response.Msg.CleanupProblemJson) != 0 {
		t.Fatalf("disconnect did not join request cleanup: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("credential removal preceded provider request termination")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked relay handler remained active")
	}
	if _, _, retained := f.service.accountSecrets.(*accountTestSecrets).counts(); retained != 0 {
		t.Fatal("credential cleanup did not follow request release")
	}
}

func TestExecutionGrantRechecksMutableOwnership(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.OpenCode, domain.ClaudeCode} {
		t.Run(string(harness), func(t *testing.T) { testExecutionGrantRechecksMutableOwnership(t, harness) })
	}
}

func testExecutionGrantRechecksMutableOwnership(t *testing.T, harness domain.Harness) {
	for _, change := range []string{"finished-job", "replaced-instance", "expired-heartbeat", "revoked-device", "disabled-machine", "paused-session", "changed-input", "replaced-connection"} {
		t.Run(change, func(t *testing.T) {
			f := newHarnessAuthorityFixture(t, "http://127.0.0.1:1", harness)
			f.registerGrant(t)
			lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.change-authority", change, func(tx *store.Tx) (any, error) {
				if change == "replaced-instance" {
					return nil, tx.SetWorkerInstance(f.input.MachineID, domain.NewID(), time.Now().UTC())
				}
				if change == "expired-heartbeat" {
					return nil, tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().UTC().Add(-time.Minute))
				}
				kind, id := domain.JobKind, f.job
				switch change {
				case "revoked-device":
					kind, id = domain.DeviceKind, f.device
				case "disabled-machine":
					kind, id = domain.MachineKind, f.input.MachineID
				case "paused-session":
					kind, id = domain.SessionKind, f.input.SessionID
				case "changed-input":
					kind, id = domain.QueueKind, f.input.InputID
				case "replaced-connection":
					kind, id = domain.AccountKind, f.input.AccountID
				}
				r, err := tx.Get(kind, id)
				if err != nil {
					return nil, err
				}
				var value any
				switch change {
				case "finished-job":
					v, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					v.State = domain.JobSucceeded
					value = v
				case "revoked-device":
					v, err := store.Decode[domain.Device](r)
					if err != nil {
						return nil, err
					}
					v.Revoked = true
					value = v
				case "disabled-machine":
					v, err := store.Decode[domain.Machine](r)
					if err != nil {
						return nil, err
					}
					v.Disabled = true
					value = v
				case "paused-session":
					v, err := store.Decode[domain.Session](r)
					if err != nil {
						return nil, err
					}
					v.Dispatch = domain.DispatchPaused
					value = v
				case "changed-input":
					v, err := store.Decode[domain.QueuedInput](r)
					if err != nil {
						return nil, err
					}
					v.Prompt = "Changed input"
					value = v
				case "replaced-connection":
					v, err := store.Decode[domain.Account](r)
					if err != nil {
						return nil, err
					}
					v.Connection.ID = domain.NewID()
					value = v
				}
				return tx.Put(kind, id, r.Revision, r.SessionID, r.ProjectID, value)
			})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
			if change == "replaced-instance" || change == "replaced-connection" || change == "expired-heartbeat" {
				if err != nil {
					t.Fatal("metadata mismatch blocked the retained grant", change, err)
				}
				accepted.Release()
				return
			}
			if err == nil {
				accepted.Release()
				t.Fatal("changed ownership retained execution authority")
			}
			select {
			case <-lease.Context.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("changed ownership did not cancel the live lease")
			}
			if _, err := lease.Key(context.Background()); err == nil {
				t.Fatal("revoked lease retrieved a key")
			}
		})
	}
}
