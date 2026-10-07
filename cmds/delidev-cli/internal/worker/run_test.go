package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestJournalReusesCompletionAndPreservesInterruptedExecution(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"jobs", "empty-hooks"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(t.TempDir(), "checkout")
	cmd := exec.Command("git", "init", "--quiet", repo)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(domain.RepositoryInspectionInput{Path: repo})
	instance := domain.NewID()
	job := domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()}
	raw, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(domain.NewID()), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, DocumentJson: raw}
	first, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || first.Problem != nil {
		t.Fatalf("execution: %v %v", err, first.Problem)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	replay, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || string(replay.Output) != string(first.Output) || replay.ReportID != first.ReportID {
		t.Fatalf("completion was reexecuted: %v", err)
	}
	first.State = journalStarted
	first.Output = nil
	if err := writeJSON(filepath.Join(root, "jobs", resource.Id+".json"), first); err != nil {
		t.Fatal(err)
	}
	interrupted, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || interrupted.Problem == nil || interrupted.Problem.Code != domain.RecoveryRequired {
		t.Fatalf("interrupted execution replayed: %v %+v", err, interrupted)
	}
	resource.Revision++
	if _, err := runJob(context.Background(), Config{Root: root}, instance, resource, job); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("changed assignment reused journal: %v", err)
	}
}

func TestAuxiliaryTitleCapabilityRequiresServerEcho(t *testing.T) {
	if auxiliaryTitleCapability(nil) {
		t.Fatal("missing AttachWorker response enabled the auxiliary stream")
	}
	resource := func(capabilities []domain.WorkerCapability) *pb.Resource {
		t.Helper()
		body, err := json.Marshal(domain.Machine{Name: "Worker", OS: "darwin", Architecture: "arm64", WorkerCapabilities: capabilities})
		if err != nil {
			t.Fatal(err)
		}
		return &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, SchemaVersion: 1, DocumentJson: body}
	}
	if auxiliaryTitleCapability(resource(nil)) || !auxiliaryTitleCapability(resource([]domain.WorkerCapability{domain.AutomaticTitlesCodexV1})) {
		t.Fatal("auxiliary title lane did not follow the server's typed capability echo")
	}
	if auxiliaryTitleCapability(&pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, SchemaVersion: 1, DocumentJson: []byte("invalid")}) {
		t.Fatal("malformed machine response enabled the auxiliary stream")
	}
}

func TestTitleProfileProbeCleanupFailureStopsWorker(t *testing.T) {
	recovery := domain.Fail(domain.RecoveryRequired, "The title probe process could not be reconciled.", "Preserve its private journal and stop the Worker.")
	if got := fatalTitleProfileProbeError(recovery); got != recovery {
		t.Fatalf("uncertain title probe cleanup was downgraded: %v", got)
	}
	unsupported := domain.Fail(domain.Unsupported, "The configured title profile is unsupported.", "Continue without automatic title capability.")
	if got := fatalTitleProfileProbeError(unsupported); got != nil {
		t.Fatalf("ordinary unsupported profile stopped the Worker: %v", got)
	}
}

func TestCodexTitleExecutableUsesVerifiedConfiguredInstallation(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "custom-codex")
	resource := func(installation domain.Installation) *pb.Resource {
		t.Helper()
		body, err := json.Marshal(domain.Machine{
			Name: "Worker", OS: "darwin", Architecture: "arm64",
			Installations: []domain.Installation{installation},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, SchemaVersion: 1, DocumentJson: body}
	}
	verified := domain.Installation{
		Harness: domain.Codex, State: domain.InstallationDetected, ResolvedPath: executable,
		Version: domain.CodexProtocolVersion, ProtocolVerified: true,
		Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified},
	}
	if got := codexTitleExecutable(resource(verified)); got != executable {
		t.Fatalf("configured Codex executable = %q, want %q", got, executable)
	}
	for name, mutate := range map[string]func(*domain.Installation){
		"missing executable":   func(i *domain.Installation) { i.ResolvedPath = "" },
		"relative executable":  func(i *domain.Installation) { i.ResolvedPath = "codex" },
		"unsafe version":       func(i *domain.Installation) { i.Version = "invalid/version" },
		"unverified protocol":  func(i *domain.Installation) { i.ProtocolVerified = false },
		"wrong protocol":       func(i *domain.Installation) { i.Protocol.Protocol = domain.OpenCodeHTTP },
		"unsupported protocol": func(i *domain.Installation) { i.Protocol.State = domain.ProtocolUnsupported },
	} {
		t.Run(name, func(t *testing.T) {
			installation := verified
			installation.Protocol = &domain.ProtocolObservation{Protocol: verified.Protocol.Protocol, State: verified.Protocol.State}
			mutate(&installation)
			if got := codexTitleExecutable(resource(installation)); got != "" {
				t.Fatalf("unsupported configured installation selected executable %q", got)
			}
		})
	}
}

func TestInspectionMetadataCapabilityRequiresServerEcho(t *testing.T) {
	machine := domain.Machine{Name: "fixture", OS: runtime.GOOS, Architecture: runtime.GOARCH, Version: rpc.Version}
	raw, _ := json.Marshal(machine)
	resource := &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, SchemaVersion: 1, DocumentJson: raw}
	if machineCapability(resource, domain.RepositoryInspectionMetadataV1) {
		t.Fatal("legacy machine fabricated metadata acceptance")
	}
	machine.WorkerCapabilities = []domain.WorkerCapability{domain.RepositoryInspectionMetadataV1, domain.AutomaticTitlesCodexV1, domain.SessionForwardingV1, domain.SessionTerminalsV1}
	raw, _ = json.Marshal(machine)
	resource.DocumentJson = raw
	if !machineCapability(resource, domain.RepositoryInspectionMetadataV1) {
		t.Fatal("independent metadata capability not recognized")
	}
	machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.RepositoryInspectionMetadataV1)
	raw, _ = json.Marshal(machine)
	resource.DocumentJson = raw
	if machineCapability(resource, domain.RepositoryInspectionMetadataV1) {
		t.Fatal("duplicate capability accepted")
	}
}
