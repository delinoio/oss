// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func imageFixture(t *testing.T, media ImageMediaType) ([]byte, ImageAttachment) {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	var err error
	if media == ImagePNG {
		err = png.Encode(&buf, value)
	} else {
		err = jpeg.Encode(&buf, value, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	hash := sha256.Sum256(raw)
	return raw, ImageAttachment{ID: NewID(), MachineID: NewID(), MediaType: media, ByteLength: uint64(len(raw)), SHA256: hex.EncodeToString(hash[:])}
}
func TestImageInputContentAndOriginalDigest(t *testing.T) {
	for _, media := range []ImageMediaType{ImagePNG, ImageJPEG} {
		t.Run(string(media), func(t *testing.T) {
			raw, ref := imageFixture(t, media)
			if err := ref.ValidateContent(raw); err != nil {
				t.Fatal(err)
			}
			changed := bytes.Clone(raw)
			changed[len(changed)/2] ^= 1
			if ref.ValidateContent(changed) == nil {
				t.Fatal("modified bytes accepted")
			}
			ref.MediaType = ImageWebP
			if ref.ValidateContent(raw) == nil {
				t.Fatal("declared type accepted without content match")
			}
		})
	}
}
func TestImageInputLimitsAndImageOnly(t *testing.T) {
	_, ref := imageFixture(t, ImagePNG)
	input := SessionInput{Mode: ExecuteMode, Attachments: []ImageAttachment{ref}}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	if (SessionInput{Mode: ExecuteMode}).Validate() == nil {
		t.Fatal("empty input accepted")
	}
	if (SessionInput{Mode: ExecuteMode, Attachments: []ImageAttachment{ref, ref}}).Validate() == nil {
		t.Fatal("duplicate attachment accepted")
	}
	refs := make([]ImageAttachment, MaxInputImages)
	for i := range refs {
		refs[i] = ref
		refs[i].ID = NewID()
		refs[i].ByteLength = MaxInputImagesBytes / MaxInputImages
	}
	if ValidateImageAttachments(refs) != nil {
		t.Fatal("exact count/total rejected")
	}
	refs[0].ByteLength++
	if ValidateImageAttachments(refs) == nil {
		t.Fatal("total limit bypassed")
	}
	refs = refs[:1]
	refs[0].ByteLength = MaxInputImageBytes
	if ValidateImageAttachments(refs) != nil {
		t.Fatal("exact individual limit rejected")
	}
	refs[0].ByteLength++
	if ValidateImageAttachments(refs) == nil {
		t.Fatal("individual limit bypassed")
	}
	refs = make([]ImageAttachment, 9)
	for i := range refs {
		refs[i] = ref
		refs[i].ID = NewID()
	}
	if ValidateImageAttachments(refs) == nil {
		t.Fatal("ninth image accepted")
	}
}
func TestImageInputDigestBindsOrderedReferences(t *testing.T) {
	_, a := imageFixture(t, ImagePNG)
	_, b := imageFixture(t, ImageJPEG)
	first := SessionInput{Prompt: "original", Mode: ExecuteMode, Attachments: []ImageAttachment{a, b}}
	second := first
	second.Attachments = []ImageAttachment{b, a}
	if first.InputDigest() == second.InputDigest() {
		t.Fatal("order not bound")
	}
	if (SessionInput{Prompt: "original"}).InputDigest() != sha256.Sum256([]byte("original")) {
		t.Fatal("text identity changed")
	}
}
func TestImageInputRejectsCorruptAndAnimatedContainers(t *testing.T) {
	for _, media := range []ImageMediaType{ImagePNG, ImageJPEG, ImageWebP} {
		if ValidateImageContent([]byte("invalid"), media) == nil {
			t.Fatal("corrupt image accepted")
		}
	}
	raw, _ := imageFixture(t, ImagePNG)
	animation := append(bytes.Clone(raw[:8]), []byte{0, 0, 0, 0, 'a', 'c', 'T', 'L', 0, 0, 0, 0}...)
	animation = append(animation, raw[8:]...)
	if ValidateImageContent(animation, ImagePNG) == nil {
		t.Fatal("animation accepted")
	}
}

func TestWebPActualContentAndDecodeAllocationBound(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvAAAAAAcQ/Y/+ByKi/wEA")
	if err != nil {
		t.Fatal(err)
	}
	if ValidateImageContent(raw, ImageWebP) != nil {
		t.Fatal("valid still WebP rejected")
	}
	animation := append(bytes.Clone(raw), []byte{'A', 'N', 'I', 'M', 0, 0, 0, 0}...)
	binary.LittleEndian.PutUint32(animation[4:8], uint32(len(animation)-8))
	if ValidateImageContent(animation, ImageWebP) == nil {
		t.Fatal("animated WebP accepted")
	}
	pngBytes, _ := imageFixture(t, ImagePNG)
	binary.BigEndian.PutUint32(pngBytes[16:20], 10000)
	binary.BigEndian.PutUint32(pngBytes[20:24], 10000)
	binary.BigEndian.PutUint32(pngBytes[29:33], crc32.ChecksumIEEE(pngBytes[12:29]))
	if ValidateImageContent(pngBytes, ImagePNG) == nil {
		t.Fatal("unsafe allocation dimensions accepted")
	}
}

func TestImageModelDeclarationKeepsOmittedTextConfigurationCompatible(t *testing.T) {
	route := RoundRobin
	agent := Agent{Name: "Fixture", Harness: Codex, ModelID: NewID(), Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}, Routing: &route, Options: AgentOptions{Permission: PermissionReadOnly}}
	model := Model{Name: "Fixture", NativeID: "fixture", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}
	id := NewID()
	text, err := resolveInlineFixture(id, 1, agent, 1, model, RoundRobin, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := text.Digest()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(text)
	if bytes.Contains(raw, []byte("image_input_declared")) {
		t.Fatal("text-only omitted profile changed")
	}
	model.InputModalities = []string{"text", "image"}
	images, err := resolveInlineFixture(id, 1, agent, 1, model, RoundRobin, nil)
	if err != nil || !images.ImageInputDeclared || images.Validate() != nil {
		t.Fatal(images, err)
	}
	model.InputModalities = []string{"text"}
	if !images.ImageInputDeclared {
		t.Fatal("current model rewrote retained declaration")
	}
	after, err := text.Digest()
	if err != nil || before != after {
		t.Fatal("text digest changed", err)
	}
}
