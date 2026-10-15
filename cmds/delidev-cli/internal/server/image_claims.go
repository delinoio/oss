// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
)

func requestImageAttachments(values []*pb.ImageAttachment) ([]domain.ImageAttachment, error) {
	result := make([]domain.ImageAttachment, 0, len(values))
	for _, value := range values {
		ref, err := imageinput.FromProto(value)
		if err != nil {
			return nil, err
		}
		result = append(result, ref)
	}
	return result, domain.ValidateImageAttachments(result)
}
func checkImageRoute(tx *store.Tx, agentID, machineID domain.ID, prompt string, refs []domain.ImageAttachment) error {
	if len(refs) == 0 {
		return nil
	}
	if err := domain.ValidateImageAttachments(refs); err != nil {
		return err
	}
	_, machine, err := activeMachine(tx, machineID)
	if err != nil {
		return err
	}
	row, err := tx.Get(domain.AgentKind, agentID)
	if err != nil {
		return err
	}
	agent, err := store.Decode[domain.Agent](row)
	if err != nil {
		return err
	}
	if !agent.Harness.ImageInputs() || !domain.ImageInputWorkerSupported(agent.Harness, machine.WorkerCapabilities) || !domain.ImagePromptAllowed(agent.Harness, prompt) {
		return domain.UnsupportedImageInput()
	}
	if err := domain.ValidateImageAttachmentsForHarness(agent.Harness, refs); err != nil {
		return err
	}
	for _, route := range agent.SourceRoutes() {
		if route.Model == nil || !slices.Contains(route.Model.InputModalities, "image") {
			return domain.UnsupportedImageInput()
		}
	}

	for _, ref := range refs {
		if ref.MachineID != machineID {
			return domain.InvalidImageInput()
		}
	}
	return nil
}

// This runs inside the same original session/input acceptance transaction.
// Receipt replay returns current records without re-entering this claim.
func claimInputImages(tx *store.Tx, actor domain.Principal, operation, sessionID, inputID, machineID domain.ID, refs []domain.ImageAttachment, creating bool) error {
	// Retained text-only queues keep their ordinary session admission rules.
	// Worker registration is required only when claiming image ownership.
	if len(refs) == 0 {
		return nil
	}
	machineRow, _, err := activeMachine(tx, machineID)
	if err != nil {
		return err
	}
	device, err := tx.InstallationWorkerDevice(machineID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		row, value, err := ownedImageUpload(tx, ref.ID, actor)
		if err != nil {
			return err
		}
		if value.Quarantined || value.WorkerDeviceID != device || value.MachineRevision != machineRow.Revision || value.State != domain.ImageReady || value.Attachment != ref || value.Attachment.MachineID != machineID || value.OperationID != operation || value.SessionID != "" && value.SessionID != sessionID || creating && value.SessionID != "" || !creating && value.SessionID != sessionID || value.UploadedBytes != ref.ByteLength || len(value.Owners) != 0 || value.InputID != "" {
			return domain.InvalidImageInput()
		}
		value.State = domain.ImageClaimed
		value.InputID = inputID
		value.SessionID = sessionID
		value.Owners = []domain.ID{sessionID}
		if _, err = putImageUpload(tx, row, value); err != nil {
			return err
		}
	}
	return nil
}
func rejectDocumentAttachments(typed []*pb.ImageAttachment, document []domain.ImageAttachment) error {
	if len(document) != 0 {
		return domain.Fail(domain.InvalidArgument, "Image attachments require the typed request field.", "Keep image references outside the session selection document.")
	}
	return nil
}

func checkSessionImageRoute(tx *store.Tx, session domain.Session, prompt string, refs []domain.ImageAttachment) error {
	if len(refs) == 0 {
		return nil
	}
	var snapshot *domain.InitialExecution
	if session.InitialExecution != nil {
		snapshot = session.InitialExecution
	} else if session.Fork != nil {
		snapshot = &session.Fork.Snapshot
	}
	if snapshot == nil {
		return checkImageRoute(tx, session.AgentID, session.MachineID, prompt, refs)
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return err
	}
	if !snapshot.Configuration.Harness.ImageInputs() || !snapshot.Configuration.ImageInputDeclared || snapshot.Configuration.SidechatPolicy != "" || !domain.ImageInputWorkerSupported(snapshot.Configuration.Harness, machine.WorkerCapabilities) || !domain.ImagePromptAllowed(snapshot.Configuration.Harness, prompt) {
		return domain.UnsupportedImageInput()
	}
	if err := domain.ValidateImageAttachmentsForHarness(snapshot.Configuration.Harness, refs); err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.MachineID != session.MachineID {
			return domain.InvalidImageInput()
		}
	}
	return domain.ValidateImageAttachments(refs)
}

// checkQueuedImageEdit applies the text-dependent image rules to an edit of an
// undelivered input. The immutable references were admitted with the original
// input, so the edit never reruns catalog or Worker eligibility; it only keeps
// the new text valid for the frozen session harness (a claimed session
// snapshot, else the Agent harness the first execution will freeze).
func checkQueuedImageEdit(tx *store.Tx, session domain.Session, prompt string, refs []domain.ImageAttachment) error {
	if len(refs) == 0 {
		return nil
	}
	var harness domain.Harness
	switch {
	case session.InitialExecution != nil:
		harness = session.InitialExecution.Configuration.Harness
	case session.Fork != nil:
		harness = session.Fork.Snapshot.Configuration.Harness
	default:
		row, err := tx.Get(domain.AgentKind, session.AgentID)
		if err != nil {
			return err
		}
		agent, err := store.Decode[domain.Agent](row)
		if err != nil {
			return err
		}
		harness = agent.Harness
	}
	if !domain.ImagePromptAllowed(harness, prompt) {
		return domain.UnsupportedImageInput()
	}
	return nil
}

// queuedOpenCodeImageWorkerMissing reports whether a queued Worker job carries a
// frozen OpenCode image declaration that the claiming Runner Device cannot
// honor. The declaration was admitted while a Worker holding the OpenCode image
// pair was connected, but a Worker that predates the pair can reconnect on the
// same machine before the job is claimed and rejects every such assignment, even
// a text-only one. The caller leaves the job queued instead of claiming it. A
// payload that does not decode is left to the claim checks that own it.
func queuedOpenCodeImageWorkerMissing(machine domain.Machine, job domain.Job) bool {
	if slices.Contains(machine.WorkerCapabilities, domain.OpenCodeImageInputsV1) && slices.Contains(machine.WorkerCapabilities, domain.ClaudeImageInputsV1) {
		return false
	}
	declares := func(configurations ...domain.ExecutionConfiguration) bool {
		for _, c := range configurations {
			if (c.Harness == domain.OpenCode || c.Harness == domain.ClaudeCode) && c.ImageInputDeclared && !domain.ImageInputWorkerSupported(c.Harness, machine.WorkerCapabilities) {
				return true
			}
		}
		return false
	}
	switch job.Type {
	case domain.ExecuteSessionJob:
		var input domain.ExecutionJobInput
		return domain.Decode(job.Input, &input) == nil && declares(input.Configuration)
	case domain.CompactSessionJob:
		var input domain.SessionCompactionInput
		return domain.DecodeCompactionInput(job.Input, &input) == nil && declares(input.Restore.Configuration, input.Assignment.Configuration)
	case domain.ForkSessionJob:
		var input domain.ForkJobInput
		return domain.Decode(job.Input, &input) == nil && declares(input.SourceAssignment.Configuration, input.Snapshot.Configuration)
	}
	return false
}
