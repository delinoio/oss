// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSessionImageDeletionJoinsOriginalOwnersAndRequiresReplayAbsence(t *testing.T) {
	config, work, _, originalLocal := deletionWorkerFixture(t, domain.Local)
	var bytesPNG bytes.Buffer
	if err := png.Encode(&bytesPNG, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	raw := bytesPNG.Bytes()
	digest := sha256.Sum256(raw)
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: work.MachineID, MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: hex.EncodeToString(digest[:])}
	manager := imageinput.Manager{Root: config.Root}
	for _, request := range []*pb.AttachmentTransfer{
		{Id: string(domain.NewID()), Attachment: imageinput.ToProto(ref), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_WRITE, Data: raw, Sha256: ref.SHA256},
		{Id: string(domain.NewID()), Attachment: imageinput.ToProto(ref), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_FINISH},
	} {
		if _, err := manager.Transfer(work.MachineID, request); err != nil {
			t.Fatal(err)
		}
	}
	work.Images = []domain.ImageAttachment{ref}
	lock, err := security.TryLock(filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
		t.Fatal("Image cleanup bypassed the original native owner")
	}
	if _, err := manager.Resolve(work.MachineID, []domain.ImageAttachment{ref}); err != nil {
		t.Fatal("Pending native cleanup deleted an image", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	if err := manager.Removed(work.MachineID, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(originalLocal, "tracked.txt")); err != nil {
		t.Fatal("Original Local file was removed", err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err != nil {
		t.Fatal("Exact completed cleanup did not replay", err)
	}
	replacement := filepath.Join(config.Root, "image-inputs", string(ref.ID)+".data")
	if err := os.WriteFile(replacement, []byte("unattributed replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
		t.Fatal("A completed proof accepted reappeared bytes")
	}
	if raw, err := os.ReadFile(replacement); err != nil || string(raw) != "unattributed replacement" {
		t.Fatal("Replay removed replacement bytes", err)
	}
}

func TestSessionImageOnlyDeletionOwnsNoWorkspace(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	// An image-only plan may precede all native preparation. It grants no
	// workspace removal authority, including an unattributed existing folder.
	work.Copies = nil
	work.PreparationDigests = nil
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: work.MachineID, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: hex.EncodeToString(make([]byte, 32))}
	work.Images = []domain.ImageAttachment{ref}
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err != nil {
		t.Fatal("Image-only proof acquired workspace authority on replay", err)
	}
	if _, err := os.Stat(filepath.Join(config.Root, "workspaces", string(work.SessionID))); err != nil {
		t.Fatal("Image-only plan adopted a workspace", err)
	}
}
