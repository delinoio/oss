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
func checkImageRoute(tx *store.Tx, agentID, machineID domain.ID, refs []domain.ImageAttachment) error {
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
	if agent.Harness != domain.Codex || !slices.Contains(machine.WorkerCapabilities, domain.ImageInputsV1) {
		return domain.UnsupportedImageInput()
	}
	for _, modelID := range agent.ModelIDs() {
		row, err := tx.Get(domain.ModelKind, modelID)
		if err != nil {
			return err
		}
		model, err := store.Decode[domain.Model](row)
		if err != nil {
			return err
		}
		if !slices.Contains(model.InputModalities, "image") {
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

func checkSessionImageRoute(tx *store.Tx, session domain.Session, refs []domain.ImageAttachment) error {
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
		return checkImageRoute(tx, session.AgentID, session.MachineID, refs)
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return err
	}
	if snapshot.Configuration.Harness != domain.Codex || !snapshot.Configuration.ImageInputDeclared || snapshot.Configuration.SidechatPolicy != "" || !slices.Contains(machine.WorkerCapabilities, domain.ImageInputsV1) {
		return domain.UnsupportedImageInput()
	}
	for _, ref := range refs {
		if ref.MachineID != session.MachineID {
			return domain.InvalidImageInput()
		}
	}
	return domain.ValidateImageAttachments(refs)
}
