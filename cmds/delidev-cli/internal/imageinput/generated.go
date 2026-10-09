// SPDX-License-Identifier: Apache-2.0
package imageinput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// This owner survives a lost publication acknowledgment. Deletion may enumerate
// only the original execution copy, after native owners have joined. A server
// frozen preserve list protects every independently retained original image.
type GenerationOwner struct {
	JobID       domain.ID `json:"job_id"`
	ExecutionID domain.ID `json:"execution_id"`
	InstanceID  domain.ID `json:"instance_id"`
	MachineID   domain.ID `json:"machine_id"`
}
type generatedEntry struct {
	NativeID   string                 `json:"native_id"`
	Attachment domain.ImageAttachment `json:"attachment"`
}
type generatedJournal struct {
	Version uint32           `json:"version"`
	Owner   GenerationOwner  `json:"owner"`
	Entries []generatedEntry `json:"entries"`
}

func (v GenerationOwner) validate() error {
	if v.JobID.Validate() != nil || v.ExecutionID.Validate() != nil || v.InstanceID.Validate() != nil || v.MachineID.Validate() != nil {
		return conflict()
	}
	return nil
}
func (m Manager) generatedPath(v GenerationOwner) (string, error) {
	if v.validate() != nil {
		return "", conflict()
	}
	root := filepath.Join(m.Root, "image-generations")
	if security.PrivateDir(root) != nil {
		return "", conflict()
	}
	return filepath.Join(root, string(v.ExecutionID)+".json"), nil
}
func readGenerated(path string, v GenerationOwner) (generatedJournal, error) {
	journal := generatedJournal{Version: 1, Owner: v, Entries: []generatedEntry{}}
	raw, err := security.ReadPrivate(path, 2<<20)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil || domain.DecodeBounded(raw, &journal, 2<<20) != nil || journal.Version != 1 || journal.Owner != v || journal.Entries == nil || len(journal.Entries) > domain.MaxSessionImageAttachments {
		return journal, conflict()
	}
	seen := map[string]bool{}
	ids := map[domain.ID]bool{}
	for _, entry := range journal.Entries {
		if domain.Text(entry.NativeID, "native image identity", 1024, true) != nil || entry.Attachment.Validate() != nil || entry.Attachment.MachineID != v.MachineID || seen[entry.NativeID] || ids[entry.Attachment.ID] {
			return journal, conflict()
		}
		seen[entry.NativeID] = true
		ids[entry.Attachment.ID] = true
	}
	return journal, nil
}
func (m Manager) SaveGenerated(owner GenerationOwner, native string, raw []byte) (domain.ImageAttachment, error) {
	if domain.Text(native, "native image identity", 1024, true) != nil || domain.ValidateImageContent(raw, domain.ImagePNG) != nil {
		return domain.ImageAttachment{}, domain.InvalidImageInput()
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	path, err := m.generatedPath(owner)
	if err != nil {
		return domain.ImageAttachment{}, err
	}
	journal, err := readGenerated(path, owner)
	if err != nil {
		return domain.ImageAttachment{}, err
	}
	sum := sha256.Sum256(raw)
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: owner.MachineID, MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: hex.EncodeToString(sum[:])}
	found := false
	for _, entry := range journal.Entries {
		if entry.NativeID == native {
			ref.ID = entry.Attachment.ID
			if ref != entry.Attachment {
				return ref, conflict()
			}
			found = true
		}
	}
	root, err := m.open()
	if err != nil {
		return ref, err
	}
	defer root.Close()
	if !found {
		// A random reference collision grants no ownership of an existing input,
		// tombstone or foreign output, even if its bytes and digest happen to match.
		if err = unusedGeneratedReference(root, ref); err != nil {
			return ref, err
		}
		if len(journal.Entries) >= domain.MaxSessionImageAttachments {
			return ref, domain.Fail(domain.ResourceExhausted, "Native image retention reached its bound.", "Retain original outputs for confirmed cleanup.")
		}
		journal.Entries = append(journal.Entries, generatedEntry{NativeID: native, Attachment: ref})
		data, _ := json.Marshal(journal)
		if len(data) > 2<<20 {
			return ref, domain.Fail(domain.ResourceExhausted, "Native image ownership metadata reached its bound.", "Retain original outputs for confirmed cleanup.")
		}
		if err = security.WriteAtomic(path, data); err != nil {
			return ref, conflict()
		}
	}
	if err = journalRef(root, ref); err != nil {
		return ref, err
	}
	if old, err := content(root, ref); err == nil {
		if Digest(old) != ref.SHA256 {
			return ref, conflict()
		}
		return ref, nil
	}
	name := string(ref.ID) + ".data"
	if regular(root, name) != nil {
		return ref, conflict()
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ref, conflict()
	}
	_, write := file.Write(raw)
	sync := file.Sync()
	close := file.Close()
	if write != nil || sync != nil || close != nil {
		return ref, conflict()
	}
	if security.SyncParent(filepath.Join(root.Name(), name)) != nil {
		return ref, conflict()
	}
	return ref, nil
}
func unusedGeneratedReference(root *os.Root, ref domain.ImageAttachment) error {
	for _, suffix := range []string{".json", ".data", ".deleted"} {
		if _, err := root.Lstat(string(ref.ID) + suffix); !errors.Is(err, os.ErrNotExist) {
			return conflict()
		}
	}
	return nil
}

func journalRef(root *os.Root, ref domain.ImageAttachment) error { return journal(root, ref, true) }

// Cleanup includes unpublished and partially written outputs. A changed journal
// or file never acquires removal authority. Independent owners are never retired.
func (m Manager) CleanupGenerated(owner GenerationOwner, preserve []domain.ImageAttachment, observe bool) error {
	storageMu.Lock()
	path, err := m.generatedPath(owner)
	if err != nil {
		storageMu.Unlock()
		return err
	}
	journal, err := readGenerated(path, owner)
	storageMu.Unlock()
	if err != nil {
		return err
	}
	for _, entry := range journal.Entries {
		ref := entry.Attachment
		if slices.Contains(preserve, ref) {
			continue
		}
		if observe {
			if err = m.Removed(owner.MachineID, ref); err != nil {
				return err
			}
			continue
		}
		// Transfer deletes only this originally journaled opaque reference. It also
		// handles an intent synchronized before any image data/journal was written.
		if err = m.DeleteGenerated(owner.MachineID, ref); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) DeleteGenerated(machine domain.ID, ref domain.ImageAttachment) error {
	_, err := m.Transfer(machine, &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: ToProto(ref), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_DELETE})
	if err != nil {
		return err
	}
	return m.Removed(machine, ref)
}
