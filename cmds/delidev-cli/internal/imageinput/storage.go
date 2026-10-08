// SPDX-License-Identifier: Apache-2.0
package imageinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// One lock serializes local mutations and prevents overlapping identical replay
// from appending a chunk twice. The private journal survives Worker restart.
var storageMu sync.Mutex

type Manager struct{ Root string }

func MediaType(value pb.ImageMediaType) domain.ImageMediaType {
	switch value {
	case pb.ImageMediaType_IMAGE_MEDIA_TYPE_PNG:
		return domain.ImagePNG
	case pb.ImageMediaType_IMAGE_MEDIA_TYPE_JPEG:
		return domain.ImageJPEG
	case pb.ImageMediaType_IMAGE_MEDIA_TYPE_WEBP:
		return domain.ImageWebP
	}
	return ""
}
func FromProto(value *pb.ImageAttachment) (domain.ImageAttachment, error) {
	if value == nil {
		return domain.ImageAttachment{}, domain.InvalidImageInput()
	}
	ref := domain.ImageAttachment{ID: domain.ID(value.Id), MachineID: domain.ID(value.MachineId), MediaType: MediaType(value.MediaType), ByteLength: value.ByteLength, SHA256: value.Sha256}
	return ref, ref.Validate()
}
func ToProto(value domain.ImageAttachment) *pb.ImageAttachment {
	media := pb.ImageMediaType_IMAGE_MEDIA_TYPE_UNSPECIFIED
	switch value.MediaType {
	case domain.ImagePNG:
		media = pb.ImageMediaType_IMAGE_MEDIA_TYPE_PNG
	case domain.ImageJPEG:
		media = pb.ImageMediaType_IMAGE_MEDIA_TYPE_JPEG
	case domain.ImageWebP:
		media = pb.ImageMediaType_IMAGE_MEDIA_TYPE_WEBP
	}
	return &pb.ImageAttachment{Id: string(value.ID), MachineId: string(value.MachineID), MediaType: media, ByteLength: value.ByteLength, Sha256: value.SHA256}
}
func Digest(raw []byte) string { value := sha256.Sum256(raw); return hex.EncodeToString(value[:]) }
func conflict() error {
	return domain.Fail(domain.Conflict, "Image transfer ownership or bytes changed.", "Keep the original image operation and inspect its upload status.")
}
func (m Manager) open() (*os.Root, error) {
	path := filepath.Join(m.Root, "image-inputs")
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, domain.InvalidImageInput()
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, domain.InvalidImageInput()
	}
	return os.OpenRoot(path)
}
func regular(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return conflict()
	}
	return nil
}
func journal(root *os.Root, ref domain.ImageAttachment, create bool) error {
	id := string(ref.ID)
	if _, err := root.Lstat(id + ".deleted"); err == nil {
		return conflict()
	} else if !errors.Is(err, os.ErrNotExist) {
		return conflict()
	}
	if err := regular(root, id+".json"); err != nil {
		return err
	}
	info, statErr := root.Stat(id + ".json")
	if statErr == nil && info.Size() > 2048 {
		return conflict()
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return conflict()
	}
	raw, err := root.ReadFile(id + ".json")
	if errors.Is(err, os.ErrNotExist) && create {
		file, err := root.OpenFile(id+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return conflict()
		}
		raw, _ = json.Marshal(ref)
		_, write := file.Write(raw)
		sync := file.Sync()
		close := file.Close()
		if write != nil || sync != nil || close != nil {
			return conflict()
		}
		return security.SyncParent(filepath.Join(root.Name(), id+".json"))
	}
	var original domain.ImageAttachment
	if err != nil || len(raw) > 2048 || domain.Decode(raw, &original) != nil || original != ref {
		return conflict()
	}
	return nil
}
func content(root *os.Root, ref domain.ImageAttachment) ([]byte, error) {
	if err := journal(root, ref, false); err != nil {
		return nil, err
	}
	name := string(ref.ID) + ".data"
	if err := regular(root, name); err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, conflict()
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, domain.MaxInputImageBytes+1))
	if err != nil || ref.ValidateContent(raw) != nil {
		return nil, domain.InvalidImageInput()
	}
	return raw, nil
}
func (m Manager) Transfer(machine domain.ID, value *pb.AttachmentTransfer) ([]byte, error) {
	ref, err := FromProto(value.GetAttachment())
	if err != nil || ref.MachineID != machine || domain.ID(value.GetId()).Validate() != nil {
		return nil, domain.InvalidImageInput()
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	root, err := m.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	id := string(ref.ID)
	switch value.Operation {
	case pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_WRITE:
		if len(value.Data) == 0 || len(value.Data) > domain.MaxImageChunkBytes || value.Sha256 != Digest(value.Data) || value.Offset > ref.ByteLength || uint64(len(value.Data)) > ref.ByteLength-value.Offset {
			return nil, domain.InvalidImageInput()
		}
		if err := journal(root, ref, true); err != nil {
			return nil, err
		}
		if err := regular(root, id+".data"); err != nil {
			return nil, err
		}
		file, err := root.OpenFile(id+".data", os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			return nil, conflict()
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Size() < 0 || uint64(info.Size()) > ref.ByteLength {
			return nil, conflict()
		}
		if value.Offset < uint64(info.Size()) {
			if value.Offset+uint64(len(value.Data)) > uint64(info.Size()) {
				return nil, conflict()
			}
			old := make([]byte, len(value.Data))
			if _, err = file.ReadAt(old, int64(value.Offset)); err != nil || !bytes.Equal(old, value.Data) {
				return nil, conflict()
			}
			return nil, nil
		}
		if value.Offset != uint64(info.Size()) {
			return nil, conflict()
		}
		if _, err = file.WriteAt(value.Data, int64(value.Offset)); err != nil {
			return nil, conflict()
		}
		if err = file.Sync(); err != nil {
			return nil, conflict()
		}
		return nil, security.SyncParent(filepath.Join(root.Name(), id+".data"))
	case pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_FINISH:
		_, err := content(root, ref)
		return nil, err
	case pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_READ:
		if value.Limit == 0 || value.Limit > domain.MaxImageChunkBytes || value.Offset > ref.ByteLength {
			return nil, domain.InvalidImageInput()
		}
		raw, err := content(root, ref)
		if err != nil {
			return nil, err
		}
		end := min(ref.ByteLength, value.Offset+uint64(value.Limit))
		return bytes.Clone(raw[value.Offset:end]), nil
	case pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_DELETE:
		// The tombstone prevents an old delayed Write from recreating deleted bytes.
		if err := regular(root, id+".deleted"); err != nil {
			return nil, err
		}
		if info, err := root.Stat(id + ".deleted"); err == nil && info.Size() > 2048 {
			return nil, conflict()
		}
		if old, err := root.ReadFile(id + ".deleted"); err == nil {
			var prior domain.ImageAttachment
			if domain.Decode(old, &prior) != nil || prior != ref {
				return nil, conflict()
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if _, err := root.Lstat(id + ".json"); err == nil {
				if err = journal(root, ref, false); err != nil {
					return nil, err
				}
			}
			raw, _ := json.Marshal(ref)
			file, err := root.OpenFile(id+".deleted", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return nil, conflict()
			}
			_, write := file.Write(raw)
			sync := file.Sync()
			close := file.Close()
			if write != nil || sync != nil || close != nil {
				return nil, conflict()
			}
		} else {
			return nil, conflict()
		}
		if err := security.SyncParent(filepath.Join(root.Name(), id+".deleted")); err != nil {
			return nil, conflict()
		}
		for _, name := range []string{id + ".data", id + ".json"} {
			if err := regular(root, name); err != nil {
				return nil, err
			}
			if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, conflict()
			}
			if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
				return nil, conflict()
			}
		}
		return nil, security.SyncParent(filepath.Join(root.Name(), id+".deleted"))
	default:
		return nil, domain.InvalidImageInput()
	}
}

// Resolve never interprets a caller path. Only verified original private bytes
// produce an adapter path. Native input remains an image primitive.
func (m Manager) Resolve(machine domain.ID, refs []domain.ImageAttachment) ([]string, error) {
	if err := domain.ValidateImageAttachments(refs); err != nil {
		return nil, err
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	root, err := m.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	paths := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.MachineID != machine {
			return nil, conflict()
		}
		if _, err := content(root, ref); err != nil {
			return nil, err
		}
		paths = append(paths, filepath.Join(m.Root, "image-inputs", string(ref.ID)+".data"))
	}
	return paths, nil
}

// Lookup verifies a retained native path against its original private journal.
// The caller must also compare the resulting complete input digest with its
// immutable checkpoint; this function alone does not authorize native input.
func (m Manager) Lookup(machine domain.ID, path string) (domain.ImageAttachment, error) {
	base := filepath.Base(path)
	if len(base) <= 5 || filepath.Ext(base) != ".data" {
		return domain.ImageAttachment{}, conflict()
	}
	id := domain.ID(base[:len(base)-5])
	if id.Validate() != nil || path != filepath.Join(m.Root, "image-inputs", string(id)+".data") {
		return domain.ImageAttachment{}, conflict()
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	root, err := m.open()
	if err != nil {
		return domain.ImageAttachment{}, err
	}
	defer root.Close()
	if err := regular(root, string(id)+".json"); err != nil {
		return domain.ImageAttachment{}, err
	}
	info, err := root.Stat(string(id) + ".json")
	if err != nil || info.Size() > 2048 {
		return domain.ImageAttachment{}, conflict()
	}
	raw, err := root.ReadFile(string(id) + ".json")
	var ref domain.ImageAttachment
	if err != nil || domain.Decode(raw, &ref) != nil || ref.ID != id || ref.MachineID != machine {
		return ref, conflict()
	}
	if _, err = content(root, ref); err != nil {
		return ref, err
	}
	return ref, nil
}

// Removed observes an original immutable deletion receipt. It never removes a
// reappeared file, creates a tombstone, or adopts another private generation.
func (m Manager) Removed(machine domain.ID, ref domain.ImageAttachment) error {
	if ref.Validate() != nil || ref.MachineID != machine {
		return domain.InvalidImageInput()
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	root, err := m.open()
	if err != nil {
		return err
	}
	defer root.Close()
	id := string(ref.ID)
	if err := regular(root, id+".deleted"); err != nil {
		return err
	}
	info, err := root.Stat(id + ".deleted")
	if err != nil || info.Size() > 2048 {
		return conflict()
	}
	raw, err := root.ReadFile(id + ".deleted")
	var receipt domain.ImageAttachment
	if err != nil || domain.Decode(raw, &receipt) != nil || receipt != ref {
		return conflict()
	}
	for _, name := range []string{id + ".data", id + ".json"} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return conflict()
		}
	}
	return nil
}
