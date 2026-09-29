package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const titleFixtureKey = "private-title-inference-fixture-key"

type installedTitleAuthority struct {
	token    atomic.Pointer[string]
	acquires atomic.Int32
	keys     atomic.Int32
	claims   atomic.Int32
	releases atomic.Int32
	scope    apiproxy.Scope
	ctx      context.Context
}

func (a *installedTitleAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	if !apiproxy.ValidToken(token) || ctx.Err() != nil {
		return nil, domain.Fail(domain.Unauthenticated, "Invalid private title fixture authority.", "")
	}
	if prior := a.token.Load(); prior != nil && *prior != token {
		return nil, domain.Fail(domain.Unauthenticated, "The private title fixture authority changed.", "")
	}
	copyToken := token
	a.token.CompareAndSwap(nil, &copyToken)
	a.acquires.Add(1)
	return &apiproxy.Lease{
		Scope: a.scope, Context: a.ctx,
		Key: func(ctx context.Context) ([]byte, error) {
			a.keys.Add(1)
			return []byte(titleFixtureKey), ctx.Err()
		},
		BeforeSubmit: func(ctx context.Context, operation apiproxy.Operation) error {
			if ctx.Err() != nil || operation != apiproxy.ResponseCreate {
				return domain.Fail(domain.PermissionDenied, "Unexpected title fixture operation.", "")
			}
			a.claims.Add(1)
			return nil
		},
		Release: func() { a.releases.Add(1) },
	}, nil
}

type installedTitleWorkerClient struct {
	delidevv1connect.WorkerServiceClient
	t           *testing.T
	credential  Credential
	jobID       domain.ID
	instanceID  domain.ID
	registerErr error
	calls       atomic.Int32
	digest      []byte
}

func (c *installedTitleWorkerClient) RegisterExecution(_ context.Context, request *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	c.calls.Add(1)
	if request == nil || request.Msg == nil || request.Msg.Mutation == nil || request.Header().Get("Authorization") != "Bearer "+c.credential.Token {
		c.t.Error("title execution registration lost its worker authentication or mutation identity")
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	mutation := request.Msg.Mutation
	if domain.ID(mutation.RequestId).Validate() != nil || domain.ID(mutation.Id) != c.jobID || mutation.ExpectedRevision != 9 || domain.ID(request.Msg.MachineId) != c.credential.MachineID || domain.ID(request.Msg.InstanceId) != c.instanceID || len(request.Msg.CredentialDigest) != sha256.Size {
		c.t.Error("title execution registration did not bind its exact assignment and credential digest")
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	c.digest = append([]byte(nil), request.Msg.CredentialDigest...)
	if c.registerErr != nil {
		return nil, c.registerErr
	}
	return connect.NewResponse(&pb.RegisterExecutionResponse{ProxyPath: apiproxy.Prefix}), nil
}

func TestTitleSendClaimPrecedesNativeProfileVerification(t *testing.T) {
	jobID, machineID, instanceID := domain.NewID(), domain.NewID(), domain.NewID()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{Token: "worker-token", DeviceID: domain.NewID(), MachineID: machineID}
	input := domain.AuxiliaryTitleInput{
		Version: 1, SessionID: domain.NewID(), OperationID: domain.NewID(), NameGeneration: 1,
		OriginalJobID: domain.NewID(), OriginalExecutionID: domain.NewID(), MachineID: machineID,
		OriginalDeviceID: credential.DeviceID, OriginalInstanceID: instanceID, AgentID: domain.NewID(),
		Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion,
		Executable: executable, AccountID: domain.NewID(),
		ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ProviderProtocol: domain.OpenAIResponses,
		ModelID: domain.NewID(), NativeModel: "fixture-model", Prompt: "private first message",
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: machineID, InstanceID: instanceID, AssignedDeviceID: credential.DeviceID, ParentID: input.OriginalJobID, Input: inputJSON}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	registrationErr := connect.NewError(connect.CodeUnavailable, nil)
	workerClient := &installedTitleWorkerClient{t: t, credential: credential, jobID: jobID, instanceID: instanceID, registerErr: registrationErr}
	config := Config{Root: t.TempDir(), execution: &PublicationConfig{
		Credential: credential, Instance: instanceID,
		Assignment: &pb.Resource{Id: string(jobID), Revision: 9, Kind: pb.EntityKind_ENTITY_KIND_JOB, DocumentJson: jobJSON},
		Client:     workerClient,
	}}
	_, err = executeSessionTitle(context.Background(), config, jobID, job)
	if workerClient.calls.Load() != 1 || domain.SafeError(err).Code != domain.ServerUnavailable {
		t.Fatalf("title registration did not fail before native profile verification: calls=%d error=%v", workerClient.calls.Load(), err)
	}
}

// TestOptInInstalledCodexTitleInference exercises one real pinned Codex title
// inference through a private loopback-only scripted provider. Ordinary tests
// never launch an installed user harness or send inference.
func TestOptInInstalledCodexTitleInference(t *testing.T) {
	executable := os.Getenv("DELIDEV_CODEX_TITLE_EXECUTABLE")
	if executable == "" {
		t.Skip("set DELIDEV_CODEX_TITLE_EXECUTABLE to opt in to installed Codex title inference")
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatal(err)
	}

	const prompt = "README に Rust のテストを追加する"
	const title = "README に Rust テストを追加"
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer "+titleFixtureKey || r.Header.Get("X-Api-Key") != "" {
			t.Error("installed Codex title inference escaped the selected server-owned provider credential")
			http.Error(w, "unsupported fixture request", http.StatusForbidden)
			return
		}
		var request map[string]json.RawMessage
		if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request) != nil {
			t.Error("Codex title request was not bounded JSON")
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		var model, instructions, input, toolChoice string
		var stream, parallel bool
		var maxOutput int
		var tools []json.RawMessage
		var reasoning struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(request["model"], &model) != nil || json.Unmarshal(request["instructions"], &instructions) != nil || json.Unmarshal(request["input"], &input) != nil || json.Unmarshal(request["stream"], &stream) != nil || json.Unmarshal(request["tools"], &tools) != nil || json.Unmarshal(request["tool_choice"], &toolChoice) != nil || json.Unmarshal(request["parallel_tool_calls"], &parallel) != nil || json.Unmarshal(request["max_output_tokens"], &maxOutput) != nil || json.Unmarshal(request["reasoning"], &reasoning) != nil {
			t.Error("Codex title request omitted its frozen prompt, model, effort or no-tools policy")
		}
		if model != "fixture-title-model" || instructions != domain.AutomaticTitleInstructions || input != prompt || !stream || len(tools) != 0 || toolChoice != "none" || parallel || maxOutput != 128 || reasoning.Effort != "high" {
			t.Errorf("title request did not match the bounded single-message profile: model=%q input=%q tools=%d choice=%q output=%d effort=%q", model, input, len(tools), toolChoice, maxOutput, reasoning.Effort)
		}
		for _, forbidden := range []string{"previous_response_id", "conversation", "models", "route", "fallbacks", "plugins", "mcp_servers", "instructions_from_project"} {
			if _, ok := request[forbidden]; ok {
				t.Errorf("title request included unrelated authority or conversation field %q", forbidden)
			}
		}
		if len(request) != 9 {
			t.Errorf("title relay forwarded unexpected provider fields: %v", request)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		responseID := "resp_private_title_fixture"
		item := map[string]any{"type": "message", "id": "msg_private_title_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": title}}}
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 31, "output_tokens": 7, "total_tokens": 38}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = w.Write(append(append([]byte("data: "), raw...), '\n', '\n'))
		}
	}))
	defer upstream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	providerID, modelID := domain.NewID(), domain.NewID()
	accountID, connectionID := domain.NewID(), domain.NewID()
	sessionID, executionID, projectID := domain.NewID(), domain.NewID(), domain.NewID()
	parentJobID, jobID, machineID, instanceID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	operationID := domain.NewID()
	provider := domain.Provider{Name: "Private installed title fixture", Endpoint: upstream.URL + "/v1", Protocol: domain.OpenAIResponses, Authentication: domain.BearerAuth}
	authority := &installedTitleAuthority{scope: apiproxy.Scope{
		ExecutionID: executionID, SessionID: sessionID, AccountID: accountID, ConnectionID: connectionID,
		ProviderID: providerID, ModelID: modelID, NativeModel: "fixture-title-model", Purpose: domain.SessionTitleUsage,
		TitlePrompt: prompt, Effort: "high", Provider: provider, Operations: []apiproxy.Operation{apiproxy.ResponseCreate},
	}, ctx: ctx}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	defer relay.Close()

	workerToken, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: relay.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: machineID, PairingID: domain.NewID(), Token: workerToken}
	workerClient := &installedTitleWorkerClient{t: t, credential: credential, jobID: jobID, instanceID: instanceID}
	input := domain.AuxiliaryTitleInput{
		Version: 1, SessionID: sessionID, OperationID: operationID, NameGeneration: 4,
		OriginalJobID: parentJobID, OriginalExecutionID: executionID, MachineID: machineID,
		OriginalDeviceID: credential.DeviceID, OriginalInstanceID: instanceID, ProjectID: projectID,
		AgentID: domain.NewID(), Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion,
		Executable: absolute, AccountID: accountID, ConnectionID: connectionID, ProviderID: providerID,
		ProviderProtocol: domain.OpenAIResponses, ModelID: modelID, NativeModel: "fixture-title-model", Effort: "high", Prompt: prompt,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: machineID, InstanceID: instanceID, AssignedDeviceID: credential.DeviceID, ParentID: parentJobID, Input: inputJSON, AcceptedAt: time.Now().UTC()}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	assignment := &pb.Resource{Id: string(jobID), Revision: 9, Kind: pb.EntityKind_ENTITY_KIND_JOB, DocumentJson: jobJSON}
	root := t.TempDir()
	config := Config{Root: root, Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})), execution: &PublicationConfig{Credential: credential, Instance: instanceID, Assignment: assignment, Client: workerClient}}

	output, err := executeSessionTitle(ctx, config, jobID, job)
	if err != nil {
		t.Fatalf("installed Codex title inference failed: %v (provider requests=%d authority acquisitions=%d claims=%d)", err, providerCalls.Load(), authority.acquires.Load(), authority.claims.Load())
	}
	var result domain.AuxiliaryTitleResult
	if domain.Decode(output, &result) != nil || result.Validate(input) != nil || result.Title != title || !result.CleanupVerified {
		t.Fatalf("installed Codex title output or cleanup evidence was invalid: %+v", result)
	}
	if result.UsageRecord == nil || result.UsageRecord.Purpose != domain.SessionTitleUsage || result.UsageRecord.SessionID != sessionID || result.UsageRecord.ExecutionID != executionID || result.UsageRecord.AccountID != accountID || result.UsageRecord.ConnectionID != connectionID || result.UsageRecord.ProviderID != providerID || result.UsageRecord.ModelID != modelID || result.UsageRecord.ProjectID != projectID || result.UsageRecord.Usage.Counts == nil || result.UsageRecord.Usage.Counts.Input == nil || *result.UsageRecord.Usage.Counts.Input != 31 {
		t.Fatalf("title response usage was not attributed to the original execution: %+v", result.UsageRecord)
	}
	if providerCalls.Load() != 1 || authority.acquires.Load() != 1 || authority.keys.Load() != 1 || authority.claims.Load() != 1 || authority.releases.Load() != 1 || workerClient.calls.Load() != 1 {
		t.Fatalf("title inference unexpectedly retried or omitted its durable send claim: requests=%d acquire=%d keys=%d claims=%d release=%d registrations=%d", providerCalls.Load(), authority.acquires.Load(), authority.keys.Load(), authority.claims.Load(), authority.releases.Load(), workerClient.calls.Load())
	}
	token := authority.token.Load()
	if token == nil {
		t.Fatal("the private title execution credential never reached its relay")
	}
	digest := sha256.Sum256([]byte(*token))
	if !bytes.Equal(workerClient.digest, digest[:]) {
		t.Fatal("registered execution credential digest did not bind the actual private inference token")
	}
	for _, directory := range []string{"probes", "title-runtimes"} {
		entries, readErr := os.ReadDir(filepath.Join(root, directory))
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("private installed Codex %s were not removed: entries=%d err=%v", directory, len(entries), readErr)
		}
	}
	processEntries, err := os.ReadDir(filepath.Join(root, "processes", string(jobID)))
	if err == nil && len(processEntries) != 0 {
		t.Fatalf("owned Codex process records remained after verified cleanup: %v", processEntries)
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestMatchesTitleOptionRequiresExactFrozenSelection(t *testing.T) {
	value := "high"
	empty := ""
	if !matchesTitleOption(&value, "high") || matchesTitleOption(&value, "medium") || !matchesTitleOption(nil, "") || !matchesTitleOption(&empty, "") || matchesTitleOption(&value, "") || matchesTitleOption(nil, "high") {
		t.Fatal("effective title settings accepted a value outside the frozen configuration")
	}
}
