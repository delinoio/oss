// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func nativeReviewResultFixture(t *testing.T, action domain.ID) domain.NativeCodeReviewResult {
	t.Helper()
	thread, turn, provider := domain.NewID(), domain.NewID(), domain.NewID()
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: domain.NewID(), DiffRevision: strings.Repeat("a", 64)}
	result := domain.NativeCodeReviewResult{
		Version: 1, ActionID: action,
		Selection:     domain.NativeCodeReviewSelection{Target: target, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)},
		ThreadID:      domain.NativeIdentity(thread),
		TurnID:        domain.NativeIdentity(turn),
		EnteredItemID: "entered", ExitedItemID: "exited",
		Findings:    []domain.NativeCodeReviewFinding{{Title: "Keep identity", Body: "The finding body.", Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2}},
		Explanation: "Bounded explanation.", Correctness: "patch is incorrect",
		RolloutDigest: strings.Repeat("e", 64), CleanupVerified: true,
		UsageRecords: []domain.ResponseUsageRecord{{Purpose: domain.NativeCodeReviewUsage, SessionID: domain.NewID(), ExecutionID: action, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: provider, ModelID: (domain.ModelIdentity{ProviderID: provider, NativeID: "fixture"}).Key(), Harness: domain.Codex, ThreadID: string(thread), TurnID: string(turn), Sequence: 1, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: domain.UsageCostMissing}}},
	}
	if err := result.Validate(); err != nil {
		t.Fatal("fixture rejected", err)
	}
	return result
}

func nativeReviewJobRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "jobs", string(domain.NewID()))
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeReviewResultJustUnderCeilingIsWrittenAndReturned(t *testing.T) {
	result := nativeReviewResultFixture(t, domain.NewID())
	// Escaped characters count at their encoded size: each bracket is six bytes.
	for len(result.Findings) < 64 {
		result.Findings = append(result.Findings, domain.NativeCodeReviewFinding{Title: "Keep identity", Body: strings.Repeat("<", 2000), Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2})
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) >= domain.MaxWorkerJobOutputBytes || len(encoded) < domain.MaxWorkerJobOutputBytes/2 {
		t.Fatalf("fixture must be a large result under the ceiling, got %d bytes", len(encoded))
	}
	jobRoot := nativeReviewJobRoot(t)
	output, err := finalizeNativeReviewResult(context.Background(), slog.New(slog.DiscardHandler), jobRoot, result)
	if err != nil {
		t.Fatal("result under the ceiling rejected", err)
	}
	retained, err := os.ReadFile(filepath.Join(jobRoot, "review-result.json"))
	if err != nil || !bytes.Equal(retained, output) || !bytes.Equal(output, encoded) {
		t.Fatal("reported bytes were not the exact retained result", err)
	}
	var decoded domain.NativeCodeReviewResult
	if domain.Decode(output, &decoded) != nil || len(decoded.Findings) != 64 || len(decoded.UsageRecords) != 1 {
		t.Fatal("findings or usage were lost")
	}
}

func TestNativeReviewResultOverCeilingIsRejectedBeforeRetention(t *testing.T) {
	result := nativeReviewResultFixture(t, domain.NewID())
	for len(result.Findings) < 64 {
		result.Findings = append(result.Findings, domain.NativeCodeReviewFinding{Title: strings.Repeat("<", 1024), Body: strings.Repeat("<", 8192), Confidence: 0.7, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2})
	}
	jobRoot := nativeReviewJobRoot(t)
	output, err := finalizeNativeReviewResult(context.Background(), slog.New(slog.DiscardHandler), jobRoot, result)
	if err == nil || output != nil || domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("over-limit result was not rejected as resource exhausted: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(jobRoot, "review-result.json")); !os.IsNotExist(statErr) {
		t.Fatal("over-limit result was retained")
	}
}

func TestNativeCodeReviewEscapeHeavyResultFinalizesOnlyWithinEncodedLimit(t *testing.T) {
	for _, body := range []string{"<", "x"} {
		t.Run(body, func(t *testing.T) {
			result := nativeReviewResultFixture(t, domain.NewID())
			result.Findings = make([]domain.NativeCodeReviewFinding, 22)
			for i := range result.Findings {
				result.Findings[i] = domain.NativeCodeReviewFinding{Title: "finding", Body: strings.Repeat(body, 8192), Confidence: 0.9, Priority: 1, Path: "file.go", StartLine: 3, EndLine: 4}
			}
			if err := result.ValidateStructure(); err != nil {
				t.Fatalf("structurally valid completed result rejected: %v", err)
			}
			root := nativeReviewJobRoot(t)
			output, err := finalizeNativeReviewResult(context.Background(), slog.New(slog.DiscardHandler), root, result)
			path := filepath.Join(root, nativeReviewResultFile)
			if body == "<" {
				if err == nil || domain.SafeError(err).Code != domain.ResourceExhausted || output != nil {
					t.Fatalf("escaped over-limit result lost typed size error: %v", err)
				}
				if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
					t.Fatal("over-limit output was retained")
				}
				return
			}
			if err != nil || len(output) == 0 || len(output) > domain.MaxWorkerJobOutputBytes {
				t.Fatalf("plain in-limit result was not finalized: %v", err)
			}
			var retained domain.NativeCodeReviewResult
			if domain.Decode(output, &retained) != nil || len(retained.Findings) != 22 || len(retained.UsageRecords) != 1 {
				t.Fatal("finalized review lost findings or original usage")
			}
		})
	}
}

type nativeReviewRecoveryFixture struct {
	root     string
	instance domain.ID
	id       domain.ID
	resource *pb.Resource
	job      domain.Job
	input    domain.NativeCodeReviewInput
	original journal
}

// newNativeReviewRecoveryFixture creates a native review assignment whose
// generic journal is still started, as after a crash between the atomic
// review-result.json write and the finished journal write.
func newNativeReviewRecoveryFixture(t *testing.T) *nativeReviewRecoveryFixture {
	t.Helper()
	checkpoint := newCheckpointFixture(t)
	root := checkpoint.root
	for _, name := range []string{"jobs"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: domain.NewID(), DiffRevision: strings.Repeat("a", 64)}
	input := domain.NativeCodeReviewInput{Version: 1, ActionID: domain.NewID(), SourceJobID: domain.NewID(), SourceRevision: 1, Source: checkpoint.input, Target: target, Actor: domain.Principal{Type: domain.OwnerDevice}}
	if err := input.Validate(); err != nil {
		t.Fatal("invalid native review assignment fixture", err)
	}
	rawInput, _ := json.Marshal(input)
	instance, id := domain.NewID(), domain.NewID()
	job := domain.Job{Type: domain.NativeCodeReviewJob, State: domain.JobClaimed, MachineID: input.Source.MachineID, InstanceID: instance, AssignedDeviceID: domain.NewID(), ParentID: input.SourceJobID, Input: rawInput, AcceptedAt: time.Now().UTC()}
	rawJob, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(id), SessionId: string(input.Source.SessionID), ProjectId: string(domain.NewID()), Revision: 2, DocumentJson: rawJob}
	original := journal{Version: 1, JobID: id, InstanceID: instance, Revision: 2, Digest: executionInputDigest(rawJob), State: journalStarted, ReportID: domain.NewID()}
	if err := writeJSON(filepath.Join(root, "jobs", string(id)+".json"), original); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "jobs", string(id))); err != nil {
		t.Fatal(err)
	}
	return &nativeReviewRecoveryFixture{root: root, instance: instance, id: id, resource: resource, job: job, input: input, original: original}
}

// result returns a valid retained result bound to the fixture assignment.
func (f *nativeReviewRecoveryFixture) result(t *testing.T) domain.NativeCodeReviewResult {
	t.Helper()
	result := nativeReviewResultFixture(t, f.input.ActionID)
	result.Selection.Target = f.input.Target
	source := f.input.Source
	for index := range result.UsageRecords {
		usage := &result.UsageRecords[index]
		usage.ContextRevision, usage.SessionID, usage.ProjectID = f.input.ContextRevision, source.SessionID, domain.ID(f.resource.ProjectId)
		usage.AccountID, usage.ConnectionID = source.AccountID, source.ConnectionID
		usage.ProviderID, usage.SubscriptionService, usage.ModelID = source.Configuration.ProviderID, source.Configuration.SubscriptionService, source.Configuration.ModelID
	}
	if err := result.Validate(); err != nil {
		t.Fatal("recovery result fixture rejected", err)
	}
	return result
}

func (f *nativeReviewRecoveryFixture) resultAtSize(t *testing.T, target int) domain.NativeCodeReviewResult {
	t.Helper()
	result := f.result(t)
	result.Findings = make([]domain.NativeCodeReviewFinding, 64)
	for i := range result.Findings {
		result.Findings[i] = domain.NativeCodeReviewFinding{Title: strings.Repeat("t", 1024), Body: strings.Repeat("b", 8192), Confidence: 0.5, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2}
	}
	result.Explanation = strings.Repeat("x", 8192)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	remaining := target - len(raw)
	if remaining < 0 {
		t.Fatalf("plain result already exceeds target: %d", len(raw))
	}
	for i := range result.Findings {
		body := []byte(result.Findings[i].Body)
		position := 0
		for remaining >= 5 && position < len(body) {
			body[position] = '<'
			remaining -= 5
			position++
		}
		for remaining > 0 && position < len(body) {
			body[position] = '"'
			remaining--
			position++
		}
		result.Findings[i].Body = string(body)
		if remaining == 0 {
			break
		}
	}
	raw, err = json.Marshal(result)
	if err != nil || remaining != 0 || len(raw) != target || result.Validate() != nil {
		t.Fatalf("could not build valid %d-byte result: actual=%d remaining=%d err=%v", target, len(raw), remaining, err)
	}
	return result
}

func TestNativeCodeReviewFinishedJournalReadsFullBoundedOutput(t *testing.T) {
	for _, target := range []int{domain.MaxWorkerJobOutputBytes - 1, domain.MaxWorkerJobOutputBytes} {
		for _, state := range []journalState{journalFinished, journalReported} {
			t.Run(string(state)+"/"+strconv.Itoa(target), func(t *testing.T) {
				f := newNativeReviewRecoveryFixture(t)
				output, err := json.Marshal(f.resultAtSize(t, target))
				if err != nil {
					t.Fatal(err)
				}
				original := f.original
				original.State, original.Output = state, output
				path := filepath.Join(f.root, "jobs", string(f.id)+".json")
				if err := writeJSON(path, original); err != nil {
					t.Fatal(err)
				}
				stored, err := os.ReadFile(path)
				if err != nil || len(stored) <= domain.MaxWorkerJobOutputBytes || len(stored) >= 2<<20 {
					t.Fatalf("journal envelope did not fit finite private bound: %d bytes, %v", len(stored), err)
				}
				got := f.run(t)
				if got.State != state || got.ReportID != original.ReportID || got.Problem != nil || !bytes.Equal(got.Output, output) {
					t.Fatalf("bounded completion was not replayed: state=%s report=%s problem=%v output_bytes=%d", got.State, got.ReportID, got.Problem, len(got.Output))
				}
			})
		}
	}
}

func TestNativeCodeReviewRecoveredFullResultRemainsReadable(t *testing.T) {
	for _, target := range []int{domain.MaxWorkerJobOutputBytes - 1, domain.MaxWorkerJobOutputBytes} {
		t.Run(strconv.Itoa(target), func(t *testing.T) {
			f := newNativeReviewRecoveryFixture(t)
			output, err := json.Marshal(f.resultAtSize(t, target))
			if err != nil {
				t.Fatal(err)
			}
			f.retain(t, output)
			recovered := f.run(t)
			if recovered.State != journalFinished || recovered.Problem != nil || recovered.ReportID != f.original.ReportID || !bytes.Equal(recovered.Output, output) {
				t.Fatalf("retained result was not recovered: state=%s report=%s problem=%v", recovered.State, recovered.ReportID, recovered.Problem)
			}
			replay := f.run(t)
			if replay.State != journalFinished || replay.ReportID != recovered.ReportID || !bytes.Equal(replay.Output, output) {
				t.Fatal("finished near-limit journal was not readable on retry")
			}
		})
	}
}

type nativeReviewFinishedReplayClient struct {
	delidevv1connect.WorkerServiceClient
	observed *pb.PublishNativeCodeReviewRequest
	reported *pb.ReportWorkRequest
}

func (c *nativeReviewFinishedReplayClient) PublishNativeCodeReview(_ context.Context, request *connect.Request[pb.PublishNativeCodeReviewRequest]) (*connect.Response[pb.PublishNativeCodeReviewResponse], error) {
	c.observed = request.Msg
	return connect.NewResponse(&pb.PublishNativeCodeReviewResponse{Revision: 2}), nil
}

func (c *nativeReviewFinishedReplayClient) ReportWork(_ context.Context, request *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	if c.observed == nil {
		return nil, fmt.Errorf("terminal report preceded retained observation")
	}
	c.reported = request.Msg
	return connect.NewResponse(&pb.ReportWorkResponse{}), nil
}

func TestNativeCodeReviewFinishedJournalReplaysObservationAndReport(t *testing.T) {
	f := newNativeReviewRecoveryFixture(t)
	output, err := json.Marshal(f.resultAtSize(t, domain.MaxWorkerJobOutputBytes))
	if err != nil {
		t.Fatal(err)
	}
	completed := f.original
	completed.State, completed.Output = journalFinished, output
	path := filepath.Join(f.root, "jobs", string(f.id)+".json")
	if err := writeJSON(path, completed); err != nil {
		t.Fatal(err)
	}
	credential := Credential{MachineID: f.job.MachineID, ServerID: domain.NewID(), DeviceID: domain.NewID()}
	observation := &pb.PublishNativeCodeReviewRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: f.resource.Id, ExpectedRevision: f.resource.Revision}, MachineId: string(credential.MachineID), InstanceId: string(f.instance), ItemId: "original-entered-item"}
	wire, err := proto.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	entry := nativeReviewPublication{Version: 1, AssignmentDigest: executionInputDigest(f.resource.DocumentJson), ServerID: credential.ServerID, DeviceID: credential.DeviceID, Request: wire}
	if err := writeJSON(filepath.Join(f.root, "jobs", string(f.id), "review-observation-000001.json"), entry); err != nil {
		t.Fatal(err)
	}
	client := &nativeReviewFinishedReplayClient{}
	config := Config{Root: f.root, Logger: slog.New(slog.DiscardHandler)}
	if err := runAndReportJob(context.Background(), config, client, credential, f.instance, assignment{context: context.Background(), cancel: func() {}}, f.resource, f.job); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(client.observed, observation) || client.reported == nil || !bytes.Equal(client.reported.OutputJson, output) || client.reported.Mutation.RequestId != string(completed.ReportID) {
		t.Fatal("replay changed the retained observation, output, or report identity")
	}
	reported := f.run(t)
	if reported.State != journalReported || reported.ReportID != completed.ReportID || !bytes.Equal(reported.Output, output) {
		t.Fatal("reported near-limit review journal was not retained")
	}
}

func TestNativeCodeReviewFinishedJournalRejectsInvalidBoundsAndIdentity(t *testing.T) {
	for _, kind := range []string{"oversized output", "foreign action", "malformed output", "oversized journal"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeReviewRecoveryFixture(t)
			result := f.resultAtSize(t, domain.MaxWorkerJobOutputBytes)
			if kind == "oversized output" {
				for i := range result.Findings {
					if strings.Contains(result.Findings[i].Body, "b") {
						result.Findings[i].Body = strings.Replace(result.Findings[i].Body, "b", "\"", 1)
						break
					}
				}
			}
			if kind == "foreign action" {
				result.ActionID = domain.NewID()
				for i := range result.UsageRecords {
					result.UsageRecords[i].ExecutionID = result.ActionID
				}
				if err := result.Validate(); err != nil {
					t.Fatalf("foreign result fixture must remain structurally valid: %v", err)
				}
			}
			output, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "oversized output" && len(output) != domain.MaxWorkerJobOutputBytes+1 {
				t.Fatal("invalid output fixture did not exceed the ceiling by one")
			}
			if kind == "malformed output" {
				output = []byte(`{"incomplete":`)
			}
			finished := f.original
			finished.State, finished.Output = journalFinished, output
			path := filepath.Join(f.root, "jobs", string(f.id)+".json")
			if kind == "malformed output" {
				// A malformed RawMessage cannot be marshaled through writeJSON.
				encoded, err := json.Marshal(f.original)
				if err != nil {
					t.Fatal(err)
				}
				encoded = append(encoded[:len(encoded)-1], []byte(`,"state":"finished","output":{"incomplete":}`)...)
				if err := security.WriteAtomic(path, encoded); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := writeJSON(path, finished); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "oversized journal" {
				stored, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				stored = append(stored, bytes.Repeat([]byte(" "), (2<<20)-len(stored)+1)...)
				if err := security.WriteAtomic(path, stored); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := runJob(context.Background(), Config{Root: f.root}, f.instance, f.resource, f.job); err == nil {
				t.Fatal("unverifiable finished review journal was accepted")
			}
		})
	}
}

func TestNativeCodeReviewJournalAllowanceDoesNotChangeOtherJobs(t *testing.T) {
	f := newNativeReviewRecoveryFixture(t)
	job := f.job
	job.Type = domain.InspectRepositoryJob
	job.Input = json.RawMessage(`{"path":"/fixture"}`)
	job.ParentID = ""
	rawJob, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	resource := &pb.Resource{Id: f.resource.Id, SessionId: f.resource.SessionId, ProjectId: f.resource.ProjectId, Revision: f.resource.Revision, DocumentJson: rawJob}
	finished := f.original
	finished.Digest = executionInputDigest(rawJob)
	finished.State, finished.Output = journalFinished, json.RawMessage(`"ok"`)
	path := filepath.Join(f.root, "jobs", string(f.id)+".json")
	if err := writeJSON(path, finished); err != nil {
		t.Fatal(err)
	}
	if _, err := runJob(context.Background(), Config{Root: f.root}, f.instance, resource, job); err != nil {
		t.Fatalf("small non-review journal rejected: %v", err)
	}
	finished.Output, err = json.Marshal(strings.Repeat("x", 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, finished); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || len(stored) <= 1<<20 || len(stored) >= 2<<20 {
		t.Fatalf("non-review fixture did not isolate decoder bound: %d bytes, %v", len(stored), err)
	}
	if _, err := runJob(context.Background(), Config{Root: f.root}, f.instance, resource, job); err == nil {
		t.Fatal("non-review job adopted native review journal allowance")
	}
}

func (f *nativeReviewRecoveryFixture) retain(t *testing.T, raw []byte) {
	t.Helper()
	if err := security.WriteAtomic(filepath.Join(f.root, "jobs", string(f.id), "review-result.json"), raw); err != nil {
		t.Fatal(err)
	}
}

func (f *nativeReviewRecoveryFixture) run(t *testing.T) journal {
	t.Helper()
	// The Config carries no execution connection. A replayed native review
	// would reach execute, fail before any send and settle a no-send proof;
	// recovery must therefore be visible as the exact retained bytes.
	result, err := runJob(context.Background(), Config{Root: f.root}, f.instance, f.resource, f.job)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeReviewRecoverySettlesRetainedResultWithoutReexecution(t *testing.T) {
	f := newNativeReviewRecoveryFixture(t)
	raw, _ := json.Marshal(f.result(t))
	f.retain(t, raw)
	recovered := f.run(t)
	if recovered.Problem != nil || !bytes.Equal(recovered.Output, raw) || recovered.State != journalFinished || recovered.ReportID != f.original.ReportID {
		t.Fatalf("retained result did not settle: %+v", recovered)
	}
	var proof domain.NativeCodeReviewRejectedProof
	if domain.Decode(recovered.Output, &proof) == nil && proof.Validate() == nil {
		t.Fatal("recovery re-entered execution and produced a no-send proof")
	}
	// The terminal report identity is reused: a reconnect returns the same
	// finished journal instead of recovering or executing again.
	replay := f.run(t)
	if replay.Problem != nil || !bytes.Equal(replay.Output, raw) || replay.ReportID != f.original.ReportID {
		t.Fatalf("finished recovery changed on replay: %+v", replay)
	}
	stored, err := os.ReadFile(filepath.Join(f.root, "jobs", string(f.id)+".json"))
	var persisted journal
	if err != nil || json.Unmarshal(stored, &persisted) != nil || persisted.State != journalFinished || !bytes.Equal(persisted.Output, raw) || persisted.Problem != nil {
		t.Fatal("recovered completion was not persisted", err)
	}
}

func TestNativeReviewRecoveryKeepsRecoveryRequiredForUnverifiableEvidence(t *testing.T) {
	foreign := func(mutate func(*domain.NativeCodeReviewResult)) func(*testing.T, *nativeReviewRecoveryFixture) []byte {
		return func(t *testing.T, f *nativeReviewRecoveryFixture) []byte {
			result := f.result(t)
			mutate(&result)
			raw, _ := json.Marshal(result)
			return raw
		}
	}
	for name, build := range map[string]func(*testing.T, *nativeReviewRecoveryFixture) []byte{
		"missing":   nil,
		"malformed": func(*testing.T, *nativeReviewRecoveryFixture) []byte { return []byte(`{"version":1,`) },
		"oversized": func(*testing.T, *nativeReviewRecoveryFixture) []byte {
			return bytes.Repeat([]byte(" "), domain.MaxWorkerJobOutputBytes+1)
		},
		"non-canonical": func(t *testing.T, f *nativeReviewRecoveryFixture) []byte {
			raw, _ := json.MarshalIndent(f.result(t), "", " ")
			return raw
		},
		"action mismatch":      foreign(func(r *domain.NativeCodeReviewResult) { r.ActionID = domain.NewID() }),
		"cleanup unverified":   foreign(func(r *domain.NativeCodeReviewResult) { r.CleanupVerified = false }),
		"target mismatch":      foreign(func(r *domain.NativeCodeReviewResult) { r.Selection.Target.RepositoryID = domain.NewID() }),
		"usage account":        foreign(func(r *domain.NativeCodeReviewResult) { r.UsageRecords[0].AccountID = domain.NewID() }),
		"usage session":        foreign(func(r *domain.NativeCodeReviewResult) { r.UsageRecords[0].SessionID = domain.NewID() }),
		"usage project":        foreign(func(r *domain.NativeCodeReviewResult) { r.UsageRecords[0].ProjectID = domain.NewID() }),
		"usage connection":     foreign(func(r *domain.NativeCodeReviewResult) { r.UsageRecords[0].ConnectionID = domain.NewID() }),
		"usage sequence":       foreign(func(r *domain.NativeCodeReviewResult) { r.UsageRecords[0].Sequence = 2 }),
		"usage thread binding": foreign(func(r *domain.NativeCodeReviewResult) { r.UsageRecords[0].ThreadID = string(domain.NewID()) }),
	} {
		t.Run(name, func(t *testing.T) {
			f := newNativeReviewRecoveryFixture(t)
			if build != nil {
				f.retain(t, build(t, f))
			}
			recovered := f.run(t)
			if recovered.Problem == nil || recovered.Problem.Code != domain.RecoveryRequired || len(recovered.Output) != 0 || recovered.ReportID != f.original.ReportID {
				t.Fatalf("unverifiable evidence fabricated success: %+v", recovered)
			}
		})
	}
}

func TestNativeReviewRecoveryLeavesNonReviewAndFinishedJournalsUnchanged(t *testing.T) {
	t.Run("started non-review job", func(t *testing.T) {
		f := newNativeReviewRecoveryFixture(t)
		raw, _ := json.Marshal(f.result(t))
		f.retain(t, raw)
		job := f.job
		job.Type = domain.InspectRepositoryJob
		job.Input = json.RawMessage(`{"path":"/fixture"}`)
		job.ParentID = ""
		rawJob, _ := json.Marshal(job)
		resource := &pb.Resource{Id: f.resource.Id, SessionId: f.resource.SessionId, ProjectId: f.resource.ProjectId, Revision: f.resource.Revision, DocumentJson: rawJob}
		stale := f.original
		stale.Digest = executionInputDigest(rawJob)
		if err := writeJSON(filepath.Join(f.root, "jobs", string(f.id)+".json"), stale); err != nil {
			t.Fatal(err)
		}
		result, err := runJob(context.Background(), Config{Root: f.root}, f.instance, resource, job)
		if err != nil || result.Problem == nil || result.Problem.Code != domain.RecoveryRequired || len(result.Output) != 0 {
			t.Fatalf("non-review journal consulted a review result: %v %+v", err, result)
		}
	})
	t.Run("finished review journal", func(t *testing.T) {
		f := newNativeReviewRecoveryFixture(t)
		proof, err := nativeReviewNoSendProof(f.input.ActionID)
		if err != nil {
			t.Fatal(err)
		}
		finished := f.original
		finished.State, finished.Output = journalFinished, proof
		if err := writeJSON(filepath.Join(f.root, "jobs", string(f.id)+".json"), finished); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(f.result(t))
		f.retain(t, raw)
		replay := f.run(t)
		if replay.Problem != nil || !bytes.Equal(replay.Output, proof) || replay.State != journalFinished {
			t.Fatalf("finished journal was rewritten from a retained result: %+v", replay)
		}
	})
}

func (f *nativeReviewRecoveryFixture) retainStale(t *testing.T, raw []byte) {
	t.Helper()
	if err := security.WriteAtomic(filepath.Join(f.root, "jobs", string(f.id), nativeReviewStaleProofFile), raw); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReviewRecoverySettlesRetainedStaleProofWithoutReexecution(t *testing.T) {
	f := newNativeReviewRecoveryFixture(t)
	// The proof is persisted by the same settlement the live Worker uses.
	jobRoot := filepath.Join(f.root, "jobs", string(f.id))
	drift := domain.Fail(domain.Conflict, "The original review target changed.", "Refresh the selected target.")
	raw, returned := settleNativeReviewOutcome(context.Background(), slog.New(slog.DiscardHandler), jobRoot, f.input.ActionID, nil, drift, true, true, true)
	if returned != nil || len(raw) == 0 {
		t.Fatalf("stale settlement was not produced: %v", returned)
	}
	recovered := f.run(t)
	if recovered.Problem != nil || !bytes.Equal(recovered.Output, raw) || recovered.State != journalFinished || recovered.ReportID != f.original.ReportID {
		t.Fatalf("retained stale proof did not settle: %+v", recovered)
	}
	var stale domain.NativeCodeReviewStaleProof
	if domain.Decode(recovered.Output, &stale) != nil || stale.Validate() != nil || stale.ActionID != f.input.ActionID {
		t.Fatal("recovery did not return the retained stale proof")
	}
	replay := f.run(t)
	if replay.Problem != nil || !bytes.Equal(replay.Output, raw) || replay.ReportID != f.original.ReportID {
		t.Fatalf("finished stale recovery changed on replay: %+v", replay)
	}
}

func TestNativeReviewRecoveryKeepsRecoveryRequiredForUnverifiableStaleProof(t *testing.T) {
	stale := func(f *nativeReviewRecoveryFixture, mutate func(*domain.NativeCodeReviewStaleProof)) []byte {
		proof := domain.NativeCodeReviewStaleProof{Version: 1, ActionID: f.input.ActionID, Stale: true, CleanupVerified: true}
		mutate(&proof)
		raw, _ := json.Marshal(proof)
		return raw
	}
	for name, build := range map[string]func(*nativeReviewRecoveryFixture) []byte{
		"malformed": func(*nativeReviewRecoveryFixture) []byte { return []byte(`{"version":1,`) },
		"non-canonical": func(f *nativeReviewRecoveryFixture) []byte {
			raw, _ := json.MarshalIndent(stale(f, func(*domain.NativeCodeReviewStaleProof) {}), "", " ")
			return raw
		},
		"action mismatch": func(f *nativeReviewRecoveryFixture) []byte {
			return stale(f, func(p *domain.NativeCodeReviewStaleProof) { p.ActionID = domain.NewID() })
		},
		"cleanup unverified": func(f *nativeReviewRecoveryFixture) []byte {
			return stale(f, func(p *domain.NativeCodeReviewStaleProof) { p.CleanupVerified = false })
		},
		"not stale": func(f *nativeReviewRecoveryFixture) []byte {
			return stale(f, func(p *domain.NativeCodeReviewStaleProof) { p.Stale = false })
		},
		"no-send proof": func(f *nativeReviewRecoveryFixture) []byte {
			raw, _ := nativeReviewNoSendProof(f.input.ActionID)
			return raw
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNativeReviewRecoveryFixture(t)
			f.retainStale(t, build(f))
			recovered := f.run(t)
			if recovered.Problem == nil || recovered.Problem.Code != domain.RecoveryRequired || len(recovered.Output) != 0 {
				t.Fatalf("unverifiable stale evidence fabricated success: %+v", recovered)
			}
		})
	}
}

func TestSettleNativeReviewOutcomeRetainsStaleProofBeforeReturningIt(t *testing.T) {
	action := domain.NewID()
	drift := domain.Fail(domain.Conflict, "The original review target changed.", "Refresh the selected target.")
	root := filepath.Join(t.TempDir(), "job")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	output, returned := settleNativeReviewOutcome(context.Background(), slog.New(slog.DiscardHandler), root, action, nil, drift, true, true, true)
	retained, err := os.ReadFile(filepath.Join(root, nativeReviewStaleProofFile))
	if err != nil || returned != nil || !bytes.Equal(retained, output) {
		t.Fatalf("stale proof was returned without durable retention: %v %v", err, returned)
	}
	// An unretainable proof must not be returned; the uncertain problem stands.
	missing := filepath.Join(t.TempDir(), "absent", "job")
	output, returned = settleNativeReviewOutcome(context.Background(), slog.New(slog.DiscardHandler), missing, action, nil, drift, true, true, true)
	if len(output) != 0 || returned == nil {
		t.Fatalf("unretained stale proof escaped: %q %v", output, returned)
	}
}
