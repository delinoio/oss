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

func TestImageInputHarnessProfile(t *testing.T) {
	for harness, want := range map[Harness]bool{Codex: true, OpenCode: true, ClaudeCode: false, GrokBuild: false} {
		if harness.ImageInputs() != want {
			t.Fatalf("%s image profile = %v", harness, !want)
		}
	}
	_, ref := imageFixture(t, ImagePNG)
	if err := ValidateImageAttachmentsForHarness(OpenCode, []ImageAttachment{ref}); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []Harness{ClaudeCode, GrokBuild} {
		if SafeError(ValidateImageAttachmentsForHarness(harness, []ImageAttachment{ref})).Code != Unsupported {
			t.Fatalf("%s accepted images", harness)
		}
	}
	if err := ValidateImageAttachmentsForHarness(ClaudeCode, nil); err != nil {
		t.Fatal("text-only input is not an image route", err)
	}
}

func TestOpenCodeImageAttachmentsShareOneCombinedByteBound(t *testing.T) {
	_, ref := imageFixture(t, ImagePNG)
	half := ref
	half.ByteLength = MaxOpenCodeInputImagesBytes / 2
	other := half
	other.ID = NewID()
	if err := ValidateImageAttachmentsForHarness(OpenCode, []ImageAttachment{half, other}); err != nil {
		t.Fatal("images at the bound rejected", err)
	}
	over := half
	over.ID = NewID()
	over.ByteLength++
	if SafeError(ValidateImageAttachmentsForHarness(OpenCode, []ImageAttachment{half, other, over})).Code != InvalidArgument {
		t.Fatal("combined images beyond the native response bound accepted")
	}
	// Codex keeps the generic 40 MiB bound.
	if err := ValidateImageAttachmentsForHarness(Codex, []ImageAttachment{half, other, over}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionConfigurationFreezesOpenCodeImageDeclaration(t *testing.T) {
	provider := NewID()
	for _, test := range []struct {
		harness    Harness
		modalities []string
		declared   bool
	}{
		{OpenCode, []string{"text", "image"}, true},
		{OpenCode, []string{"text"}, false},
		{Codex, []string{"text", "image"}, true},
		{ClaudeCode, []string{"text", "image"}, false},
		{GrokBuild, []string{"text", "image"}, false},
	} {
		identity := ModelIdentity{ProviderID: provider, NativeID: "fixture-model"}
		agent := Agent{Name: "Fixture", Harness: test.harness, ModelID: identity.Key(), Model: &InlineModel{ModelIdentity: identity, InputModalities: test.modalities, MetadataSource: UserDeclared}, Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}, Options: AgentOptions{Permission: PermissionReadOnly}}
		model := Model{Name: "Fixture", NativeID: identity.NativeID, ProviderID: provider, Harnesses: []Harness{test.harness}, InputModalities: test.modalities, MetadataSource: UserDeclared}
		config, err := ResolveExecutionConfiguration(NewID(), 1, agent, 1, model, Priority, nil)
		if err != nil {
			t.Fatal(test.harness, err)
		}
		if config.ImageInputDeclared != test.declared {
			t.Fatalf("%s %v declared=%v", test.harness, test.modalities, config.ImageInputDeclared)
		}
		raw, err := json.Marshal(config)
		if err != nil || bytes.Contains(raw, []byte("image_input_declared")) != test.declared {
			t.Fatalf("%s declaration serialization changed: %s", test.harness, raw)
		}
	}
	config := ExecutionConfiguration{Harness: ClaudeCode, ImageInputDeclared: true}
	if SafeError(config.Validate()).Code == "" {
		t.Fatal("retained non-image harness declaration accepted")
	}
}

func TestOpenCodeImageCapabilityRefinesTheBaseImageCapability(t *testing.T) {
	base, refined := []WorkerCapability{ImageInputsV1}, []WorkerCapability{ImageInputsV1, OpenCodeImageInputsV1}
	for name, test := range map[string]struct {
		harness      Harness
		capabilities []WorkerCapability
		supported    bool
	}{
		"codex base":               {Codex, base, true},
		"codex refined":            {Codex, refined, true},
		"opencode base":            {OpenCode, base, false},
		"opencode refined":         {OpenCode, refined, true},
		"opencode refinement only": {OpenCode, []WorkerCapability{OpenCodeImageInputsV1}, false},
		"opencode none":            {OpenCode, nil, false},
		"claude refined":           {ClaudeCode, refined, true},
	} {
		if ImageInputWorkerSupported(test.harness, test.capabilities) != test.supported {
			t.Errorf("%s: supported != %v", name, test.supported)
		}
	}
	if !OpenCodeImageInputsPaired(base) || !OpenCodeImageInputsPaired(refined) || !OpenCodeImageInputsPaired(nil) {
		t.Fatal("a paired capability set was rejected")
	}
	if OpenCodeImageInputsPaired([]WorkerCapability{OpenCodeImageInputsV1}) {
		t.Fatal("the OpenCode image capability without the base capability was paired")
	}
	if err := (Machine{Name: "Original Worker", OS: "linux", Architecture: "amd64", WorkerCapabilities: refined}).Validate(); err != nil {
		t.Fatalf("a machine holding both image capabilities failed validation: %v", err)
	}
	if err := (Machine{Name: "Original Worker", OS: "linux", Architecture: "amd64", WorkerCapabilities: []WorkerCapability{OpenCodeImageInputsV1}}).Validate(); err == nil {
		t.Fatal("a machine holding the OpenCode image capability without the base capability validated")
	}
}
