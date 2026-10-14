// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func managedTitleWorkerFixture(t *testing.T) (Config, domain.ID, domain.Job, domain.AuxiliaryTitleInput, *installedTitleWorkerClient) {
	t.Helper()
	jobID, machineID, instanceID := domain.NewID(), domain.NewID(), domain.NewID()
	credential := Credential{Token: "worker-token", DeviceID: domain.NewID(), MachineID: machineID}
	input := domain.AuxiliaryTitleInput{
		Version: 3, SessionID: domain.NewID(), OperationID: domain.NewID(), NameGeneration: 1,
		OriginalJobID: domain.NewID(), OriginalExecutionID: domain.NewID(), MachineID: machineID,
		OriginalDeviceID: credential.DeviceID, OriginalInstanceID: instanceID, AgentID: domain.NewID(),
		Harness: domain.Codex, NativeVersion: "0.159.2", AccountID: domain.NewID(), ConnectionID: domain.NewID(),
		ModelID: (domain.ModelIdentity{SubscriptionService: domain.SubscriptionChatGPT, NativeID: "fixture-model"}).Key(), NativeModel: "fixture-model", Prompt: "private first message",
		Startup: &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: "/native/codex", ExecutableSHA256: strings.Repeat("a", 64)},
		Managed: &domain.ManagedTitleAuthority{SubscriptionService: domain.SubscriptionChatGPT, Generation: domain.NewID(), IdentityCommitment: strings.Repeat("c", 64)},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: machineID, InstanceID: instanceID, AssignedDeviceID: credential.DeviceID, ParentID: input.OriginalJobID, Input: inputJSON}
	jobJSON, _ := json.Marshal(job)
	client := &installedTitleWorkerClient{t: t, credential: credential, jobID: jobID, instanceID: instanceID}
	config := Config{Root: t.TempDir(), execution: &PublicationConfig{
		Credential: credential, Instance: instanceID,
		Assignment: &pb.Resource{Id: string(jobID), Revision: 9, Kind: pb.EntityKind_ENTITY_KIND_JOB, DocumentJson: jobJSON},
		Client:     client,
	}}
	return config, jobID, job, input, client
}

// The managed title never registers an API relay credential, and a missing
// original startup proof refuses before any runtime, lease or native input.
func TestManagedTitleRefusesBeforeRuntimeWithoutOriginalStartupProof(t *testing.T) {
	config, jobID, job, _, client := managedTitleWorkerFixture(t)
	output, err := executeSessionTitle(context.Background(), config, jobID, job)
	if err == nil || output != nil {
		t.Fatal("a managed title without its original executable proof ran")
	}
	if client.calls.Load() != 0 {
		t.Fatal("a managed title used API relay registration")
	}
	if _, statErr := os.Stat(filepath.Join(config.Root, "title-runtimes")); !os.IsNotExist(statErr) {
		t.Fatal("a refused managed title created a private runtime")
	}
}

func TestManagedTitleRejectsForeignMachineAssignment(t *testing.T) {
	config, jobID, job, input, client := managedTitleWorkerFixture(t)
	input.MachineID = domain.NewID()
	job.Input, _ = json.Marshal(input)
	if _, err := executeSessionTitle(context.Background(), config, jobID, job); err == nil || client.calls.Load() != 0 {
		t.Fatal("a foreign managed assignment was accepted")
	}
}
