// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"slices"
	"strings"
)

const MaxInputImages = 8
const MaxInputImageBytes = 10 << 20
const MaxInputImagesBytes = 40 << 20
const MaxImageChunkBytes = 256 << 10

// Decode bounds protect the Worker against small compressed allocation bombs.
const MaxInputImagePixels = 40_000_000
const ImageInputsV1 WorkerCapability = "image-inputs-v1"

type ImageMediaType string

const (
	ImagePNG  ImageMediaType = "image/png"
	ImageJPEG ImageMediaType = "image/jpeg"
	ImageWebP ImageMediaType = "image/webp"
)

type ImageAttachment struct {
	ID         ID             `json:"id"`
	MachineID  ID             `json:"machine_id"`
	MediaType  ImageMediaType `json:"media_type"`
	ByteLength uint64         `json:"byte_length"`
	SHA256     string         `json:"sha256"`
}

func InvalidImageInput() *Error {
	return Fail(InvalidArgument, "Invalid image attachment.", "Use a still PNG, JPEG or WebP image within the attachment limits.")
}
func UnsupportedImageInput() *Error {
	return Fail(Unsupported, "The selected native route does not support images.", "Keep the draft and select an explicitly supported Agent Worker and Runner Device, or send text only.")
}
func (a ImageAttachment) Validate() error {
	if a.ID.Validate() != nil || a.MachineID.Validate() != nil || !slices.Contains([]ImageMediaType{ImagePNG, ImageJPEG, ImageWebP}, a.MediaType) || a.ByteLength == 0 || a.ByteLength > MaxInputImageBytes || !deletionHash(a.SHA256) {
		return InvalidImageInput()
	}
	return nil
}
func ValidateImageAttachments(values []ImageAttachment) error {
	if len(values) > MaxInputImages {
		return InvalidImageInput()
	}
	seen := make(map[ID]bool)
	var total uint64
	for _, value := range values {
		if value.Validate() != nil || seen[value.ID] {
			return InvalidImageInput()
		}
		seen[value.ID] = true
		total += value.ByteLength
	}
	if total > MaxInputImagesBytes {
		return InvalidImageInput()
	}
	return nil
}

// ValidateImageContent checks the original container and fully decodes pixels.
// Animation chunks are rejected before decode; accepted bytes are never changed.
func ValidateImageContent(raw []byte, media ImageMediaType) error {
	if len(raw) == 0 || len(raw) > MaxInputImageBytes {
		return InvalidImageInput()
	}
	if media == ImagePNG {
		if len(raw) < 8 || !bytes.Equal(raw[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
			return InvalidImageInput()
		}
		for offset := 8; offset < len(raw); {
			if offset+12 > len(raw) {
				return InvalidImageInput()
			}
			n := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
			if n > len(raw)-offset-12 {
				return InvalidImageInput()
			}
			kind := string(raw[offset+4 : offset+8])
			if kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
				return InvalidImageInput()
			}
			offset += n + 12
			if kind == "IEND" && offset != len(raw) {
				return InvalidImageInput()
			}
		}
	}
	if media == ImageWebP {
		if len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(raw[4:8]))+8 != uint64(len(raw)) {
			return InvalidImageInput()
		}
		for offset := 12; offset < len(raw); {
			if offset+8 > len(raw) {
				return InvalidImageInput()
			}
			n := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
			if n > len(raw)-offset-8 {
				return InvalidImageInput()
			}
			kind := string(raw[offset : offset+4])
			if kind == "ANIM" || kind == "ANMF" || kind == "VP8X" && (n != 10 || raw[offset+8]&2 != 0) {
				return InvalidImageInput()
			}
			offset += 8 + n + (n & 1)
			if offset > len(raw) {
				return InvalidImageInput()
			}
		}
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > MaxInputImagePixels || "image/"+format != string(media) {
		return InvalidImageInput()
	}
	if _, _, err = image.Decode(bytes.NewReader(raw)); err != nil {
		return InvalidImageInput()
	}
	return nil
}
func (a ImageAttachment) ValidateContent(raw []byte) error {
	digest := sha256.Sum256(raw)
	if a.Validate() != nil || uint64(len(raw)) != a.ByteLength || hex.EncodeToString(digest[:]) != a.SHA256 {
		return InvalidImageInput()
	}
	return ValidateImageContent(raw, a.MediaType)
}

// InputDigest keeps original text-only checkpoint identities byte-compatible.
// Image-bearing identities bind each immutable ordered reference as well.
func (i SessionInput) InputDigest() [sha256.Size]byte {
	if len(i.Attachments) == 0 {
		return sha256.Sum256([]byte(i.Prompt))
	}
	raw, _ := json.Marshal(struct {
		Prompt      string            `json:"prompt"`
		Attachments []ImageAttachment `json:"attachments"`
	}{i.Prompt, i.Attachments})
	return sha256.Sum256(raw)
}
func (i SessionInput) HasContent() bool {
	return strings.TrimSpace(i.Prompt) != "" || len(i.Attachments) > 0
}
