// SPDX-License-Identifier: Apache-2.0
package imageinput

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedImagesRetainOriginalOrderBytesAndIndependentCleanup(t *testing.T) {
	m := Manager{Root: t.TempDir()}
	owner := GenerationOwner{JobID: domain.NewID(), ExecutionID: domain.NewID(), InstanceID: domain.NewID(), MachineID: domain.NewID()}
	raw := imageFixture(t, domain.ImagePNG)
	a, err := m.SaveGenerated(owner, "call-one", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.SaveGenerated(owner, "call-two", raw)
	if err != nil || a.ID == b.ID {
		t.Fatal("ordered distinct native calls lost", err)
	}
	replay, err := (Manager{Root: m.Root}).SaveGenerated(owner, "call-one", raw)
	if err != nil || replay != a {
		t.Fatal("restart adopted fresh generation", err)
	}
	read := transfer(a, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_READ)
	read.Limit = domain.MaxImageChunkBytes
	got, err := m.Transfer(owner.MachineID, read)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("read changed bytes", err)
	}
	path, _ := m.generatedPath(owner)
	journal, err := readGenerated(path, owner)
	if err != nil || journal.Entries[0].Attachment != a || journal.Entries[1].Attachment != b {
		t.Fatal("wire order lost")
	}
	if err = m.CleanupGenerated(owner, []domain.ImageAttachment{a}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Transfer(owner.MachineID, read); err != nil {
		t.Fatal("independent owner removed", err)
	}
	if m.Removed(owner.MachineID, b) != nil {
		t.Fatal("unpublished original not removed")
	}
	if err = m.CleanupGenerated(owner, []domain.ImageAttachment{a}, true); err != nil {
		t.Fatal("cleanup replay changed", err)
	}
	if err = m.DeleteGenerated(owner.MachineID, a); err != nil {
		t.Fatal("last independent owner cleanup", err)
	}
	if _, err = m.SaveGenerated(owner, "call-one", raw); err == nil {
		t.Fatal("delayed completed image recreated deleted output")
	}
}
func TestGeneratedImageChangedOwnerAndUnpublishedPartialIntent(t *testing.T) {
	m := Manager{Root: t.TempDir()}
	owner := GenerationOwner{JobID: domain.NewID(), ExecutionID: domain.NewID(), InstanceID: domain.NewID(), MachineID: domain.NewID()}
	raw := imageFixture(t, domain.ImagePNG)
	ref, err := m.SaveGenerated(owner, "owned", raw)
	if err != nil {
		t.Fatal(err)
	}
	wrong := owner
	wrong.InstanceID = domain.NewID()
	if m.CleanupGenerated(wrong, nil, false) == nil {
		t.Fatal("replacement adopted original output")
	}
	if _, err = m.Resolve(owner.MachineID, []domain.ImageAttachment{ref}); err != nil {
		t.Fatal("rejected cleanup removed original")
	}
	// Intent persisted before native image data: durable cleanup must still observe
	// a tombstone and must never reach a user-selected native savedPath.
	path, _ := m.generatedPath(owner)
	journal, _ := readGenerated(path, owner)
	pending := imageRef(raw, domain.ImagePNG)
	pending.MachineID = owner.MachineID
	journal.Entries = append(journal.Entries, generatedEntry{NativeID: "unpublished", Attachment: pending})
	data, _ := json.Marshal(journal)
	if os.WriteFile(path, data, 0600) != nil {
		t.Fatal("fixture")
	}
	foreign := filepath.Join(m.Root, "original-user.png")
	os.WriteFile(foreign, raw, 0600)
	if err = m.CleanupGenerated(owner, nil, false); err != nil {
		t.Fatal(err)
	}
	if m.Removed(owner.MachineID, pending) != nil {
		t.Fatal("partial intent lost")
	}
	if got, _ := os.ReadFile(foreign); !bytes.Equal(got, raw) {
		t.Fatal("original user image removed")
	}
}

func TestNewGeneratedReferenceNeverAdoptsExistingStorage(t *testing.T) {
	m := Manager{Root: t.TempDir()}
	root, err := m.open()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, suffix := range []string{".json", ".data", ".deleted"} {
		ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.NewID(), MediaType: domain.ImagePNG, ByteLength: 1, SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
		if err = unusedGeneratedReference(root, ref); err != nil {
			t.Fatal(err)
		}
		name := string(ref.ID) + suffix
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte("original foreign storage")); err != nil {
			t.Fatal(err)
		}
		file.Close()
		if unusedGeneratedReference(root, ref) == nil {
			t.Fatal("existing storage adopted", suffix)
		}
		raw, err := root.ReadFile(name)
		if err != nil || string(raw) != "original foreign storage" {
			t.Fatal("foreign storage changed", err)
		}
	}
}
