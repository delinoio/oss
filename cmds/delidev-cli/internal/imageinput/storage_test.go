// SPDX-License-Identifier: Apache-2.0
package imageinput

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func imageFixture(t *testing.T, media domain.ImageMediaType) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var err error
	switch media {
	case domain.ImagePNG:
		err = png.Encode(&out, img)
	case domain.ImageJPEG:
		err = jpeg.Encode(&out, img, nil)
	case domain.ImageWebP:
		raw, e := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvAAAAAAcQ/Y/+ByKi/wEA")
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func imageRef(raw []byte, media domain.ImageMediaType) domain.ImageAttachment {
	return domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.NewID(), MediaType: media, ByteLength: uint64(len(raw)), SHA256: Digest(raw)}
}
func transfer(ref domain.ImageAttachment, op pb.AttachmentTransferOperation) *pb.AttachmentTransfer {
	return &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: ToProto(ref), Operation: op}
}
func write(t *testing.T, m Manager, ref domain.ImageAttachment, offset uint64, raw []byte) error {
	t.Helper()
	v := transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_WRITE)
	v.Offset = offset
	v.Data = raw
	v.Sha256 = Digest(raw)
	_, err := m.Transfer(ref.MachineID, v)
	return err
}
func TestOriginalImageBytesSurviveRestartAndReplay(t *testing.T) {
	for _, media := range []domain.ImageMediaType{domain.ImagePNG, domain.ImageJPEG, domain.ImageWebP} {
		t.Run(string(media), func(t *testing.T) {
			raw := imageFixture(t, media)
			ref := imageRef(raw, media)
			m := Manager{Root: t.TempDir()}
			half := len(raw) / 2
			if err := write(t, m, ref, 0, raw[:half]); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Transfer(ref.MachineID, transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_FINISH)); err == nil {
				t.Fatal("truncated image finished")
			}
			m = Manager{Root: m.Root}
			if err := write(t, m, ref, 0, raw[:half]); err != nil {
				t.Fatal("identical retry", err)
			}
			changed := bytes.Clone(raw[:half])
			changed[0] ^= 1
			if err := write(t, m, ref, 0, changed); err == nil {
				t.Fatal("changed retry accepted")
			}
			if err := write(t, m, ref, uint64(half+1), raw[half:]); err == nil {
				t.Fatal("gap accepted")
			}
			if err := write(t, m, ref, uint64(half), raw[half:]); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Transfer(ref.MachineID, transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_FINISH)); err != nil {
				t.Fatal(err)
			}
			paths, err := m.Resolve(ref.MachineID, []domain.ImageAttachment{ref})
			if err != nil || len(paths) != 1 {
				t.Fatal(paths, err)
			}
			if got, err := m.Lookup(ref.MachineID, paths[0]); err != nil || got != ref {
				t.Fatal(got, err)
			}
			v := transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_READ)
			v.Offset = uint64(half)
			v.Limit = domain.MaxImageChunkBytes
			got, err := m.Transfer(ref.MachineID, v)
			if err != nil || !bytes.Equal(got, raw[half:]) {
				t.Fatal(err)
			}
			if _, err := m.Resolve(domain.NewID(), []domain.ImageAttachment{ref}); err == nil {
				t.Fatal("foreign Runner resolved")
			}
			if _, err := m.Lookup(ref.MachineID, filepath.Join(t.TempDir(), filepath.Base(paths[0]))); err == nil {
				t.Fatal("foreign path resolved")
			}
			d := transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_DELETE)
			if _, err := m.Transfer(ref.MachineID, d); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Transfer(ref.MachineID, d); err != nil {
				t.Fatal("delete replay", err)
			}
			if err := write(t, m, ref, 0, raw); err == nil {
				t.Fatal("delayed write recreated deleted image")
			}
			if err := m.Removed(ref.MachineID, ref); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths[0], raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.Removed(ref.MachineID, ref); err == nil {
				t.Fatal("replacement passed original removal proof")
			}
			if got, err := os.ReadFile(paths[0]); err != nil || !bytes.Equal(got, raw) {
				t.Fatal("proof changed replacement", err)
			}
			if err := os.Remove(paths[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
				t.Fatal("bytes remain", err)
			}
		})
	}
}
func TestFinishRejectsContentTypeDigestAndSymlinkChanges(t *testing.T) {
	raw := imageFixture(t, domain.ImagePNG)
	for _, mutate := range []func(*domain.ImageAttachment){func(r *domain.ImageAttachment) { r.MediaType = domain.ImageJPEG }, func(r *domain.ImageAttachment) { r.SHA256 = Digest([]byte("different")) }} {
		ref := imageRef(raw, domain.ImagePNG)
		mutate(&ref)
		m := Manager{Root: t.TempDir()}
		if err := write(t, m, ref, 0, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Transfer(ref.MachineID, transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_FINISH)); err == nil {
			t.Fatal("invalid content finished")
		}
	}
	ref := imageRef(raw, domain.ImagePNG)
	m := Manager{Root: t.TempDir()}
	if err := write(t, m, ref, 0, raw); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Root, "image-inputs", string(ref.ID)+".data")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(outside, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := m.Resolve(ref.MachineID, []domain.ImageAttachment{ref}); err == nil {
		t.Fatal("symlink resolved")
	}
	if _, err := m.Transfer(ref.MachineID, transfer(ref, pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_DELETE)); err == nil {
		t.Fatal("symlink deleted")
	}
	if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("outside changed", err)
	}
}
