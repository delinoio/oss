package opencode

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// syntheticReadPrefix starts the synthetic text part that the pinned native
// prompt pipeline stores for every file part it expands. Source:
// anomalyco/opencode 545f51d26cc39a907d2867492d498d9607ea5fa4,
// packages/opencode/src/session/prompt.ts (createUserMessage file handling).
const syntheticReadPrefix = "Called the Read tool with the following input:"

// maxSyntheticReadBytes bounds one synthetic Read text part. It carries a
// private path and a short JSON object, never image bytes.
const maxSyntheticReadBytes = 64 << 10

// inputImageOverhead reserves JSON framing, the synthetic Read part and the
// native identity fields stored with each image in one native message.
const inputImageOverhead = 1 << 10

// InputImage is one ordered image attachment of an OpenCode input. Path is the
// private Worker file that native OpenCode reads. It never enters claim
// digests, diagnostics or logs.
type InputImage struct {
	MediaType domain.ImageMediaType
	SHA256    string
	Bytes     uint64
	Path      string
}

// storedInputImage is the path-free identity retained for stored-input
// verification of one image.
type storedInputImage struct {
	mediaType domain.ImageMediaType
	sha256    string
	bytes     uint64
	partID    string
	filename  string
}

// imageExtension maps the closed attachment media types to the opaque
// filename suffix. The user's original filename never reaches OpenCode.
func imageExtension(media domain.ImageMediaType) (string, bool) {
	switch media {
	case domain.ImagePNG:
		return "png", true
	case domain.ImageJPEG:
		return "jpg", true
	case domain.ImageWebP:
		return "webp", true
	}
	return "", false
}

func imageDigestText(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// imagePartID derives the deterministic native part ID of the index-th image
// from the original text part ID by advancing its timestamp/counter field.
func imagePartID(partID string, index int) (string, bool) {
	if !nativeID(partID, "prt") || index < 0 || index >= domain.MaxInputImages {
		return "", false
	}
	value, err := strconv.ParseUint(partID[4:16], 16, 64)
	if err != nil {
		return "", false
	}
	next := (value + uint64(index) + 1) & 0xffffffffffff
	id := fmt.Sprintf("prt_%012x%s", next, partID[16:])
	return id, nativeID(id, "prt")
}

func validInputImages(images []InputImage) bool {
	if len(images) > domain.MaxInputImages {
		return false
	}
	var total uint64
	for _, image := range images {
		if _, ok := imageExtension(image.MediaType); !ok || !imageDigestText(image.SHA256) || image.Bytes == 0 || image.Bytes > domain.MaxInputImageBytes {
			return false
		}
		total += image.Bytes
	}
	return total <= domain.MaxOpenCodeInputImagesBytes
}

// storedImages derives the retained path-free identities for an input.
func storedImages(partID string, images []InputImage) ([]storedInputImage, bool) {
	if !validInputImages(images) {
		return nil, false
	}
	result := make([]storedInputImage, len(images))
	for i, image := range images {
		id, ok := imagePartID(partID, i)
		extension, _ := imageExtension(image.MediaType)
		if !ok {
			return nil, false
		}
		result[i] = storedInputImage{mediaType: image.MediaType, sha256: image.SHA256, bytes: image.Bytes, partID: id, filename: "image-" + strconv.Itoa(i+1) + "." + extension}
	}
	return result, true
}

func base64Length(size uint64) int { return int((size + 2) / 3 * 4) }

// inputFileURL renders the private Worker path as an absolute file URL.
func inputFileURL(path string) (string, bool) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return "", false
	}
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String(), true
}

type inputFilePart struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Mime     string `json:"mime"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// encodeInput builds the prompt_async body. With claim set, each file URL is
// replaced by the image content digest so the body digest never depends on a
// private Worker path. Text-only bodies are byte-identical in both modes and
// to the historical text-only encoding.
func encodeInput(settings SessionSettings, messageID, partID, text string, images []InputImage, claim bool) ([]byte, error) {
	model := inputModel{settings.Provider, settings.Model}
	if len(images) == 0 {
		return encodeTextInput(settings, messageID, partID, text)
	}
	stored, ok := storedImages(partID, images)
	if !ok || strings.TrimSpace(text) == "" {
		return nil, sessionInvalid()
	}
	parts := make([]any, 0, len(images)+1)
	parts = append(parts, inputPart{partID, "text", text})
	encodedImages := 0
	for i, image := range images {
		location := "sha256:" + image.SHA256
		if !claim {
			var ok bool
			if location, ok = inputFileURL(image.Path); !ok {
				return nil, sessionInvalid()
			}
		}
		parts = append(parts, inputFilePart{ID: stored[i].partID, Type: "file", Mime: string(image.MediaType), Filename: stored[i].filename, URL: location})
		encodedImages += base64Length(image.Bytes) + len("data:;base64,") + len(image.MediaType) + inputImageOverhead
	}
	body, err := json.Marshal(struct {
		MessageID string       `json:"messageID"`
		Model     inputModel   `json:"model"`
		Agent     PrimaryAgent `json:"agent"`
		Parts     []any        `json:"parts"`
	}{messageID, model, settings.Agent, parts})
	// The native server stores every image as a base64 data URL in the same
	// message. Keep the complete stored message and each event inside the
	// adapter's response bound so it can always be verified.
	if err != nil || len(body)+encodedImages > maxHTTPBody {
		return nil, sessionInvalid()
	}
	return body, nil
}

// InputClaimDigest lets the owning Worker compare the native mutation against
// its immutable assignment. Text-only inputs return the historical digest.
func InputClaimDigest(settings SessionSettings, messageID, partID, text string, images []InputImage) (string, error) {
	if !validSessionSettings(settings) || !nativeID(messageID, "msg") || !nativeID(partID, "prt") || domain.Text(text, "input", 256<<10, len(images) > 0) != nil {
		return "", sessionInvalid()
	}
	if len(images) == 0 {
		return TextInputClaimDigest(settings, messageID, partID, text)
	}
	body, err := encodeInput(settings, messageID, partID, text, images, true)
	if err != nil {
		return "", err
	}
	return mutationDigest(body), nil
}

type storedInputPart uint8

const (
	originalInputPart storedInputPart = iota + 1
	syntheticInputPart
	imageInputPart
)

// classifyInputPart recognizes the closed set of parts the native server may
// store for one accepted input. index is the image ordinal for file parts.
// It validates only the part itself; counts and order are checked by callers.
func classifyInputPart(input *sessionInput, part NativePart) (storedInputPart, int, error) {
	if part.SessionID != input.receipt.SessionID || part.MessageID != input.receipt.MessageID {
		return 0, 0, observerProblem()
	}
	switch part.Kind {
	case TextPartKind:
		text := part.Text
		if text == nil || text.Ignored != nil || text.Metadata != nil {
			return 0, 0, observerProblem()
		}
		if part.ID == input.receipt.PartID {
			if text.Timing != nil || text.Synthetic != nil || sha256.Sum256([]byte(text.Text)) != input.digest {
				return 0, 0, observerProblem()
			}
			return originalInputPart, 0, nil
		}
		if len(input.images) == 0 || text.Synthetic == nil || !*text.Synthetic || len(text.Text) > maxSyntheticReadBytes || !strings.HasPrefix(text.Text, syntheticReadPrefix) {
			return 0, 0, observerProblem()
		}
		return syntheticInputPart, 0, nil
	case FilePartKind:
		for i, image := range input.images {
			if part.ID != image.partID {
				continue
			}
			file := part.File
			// The pinned input shape has no `source`; an extra native annotation
			// means the expansion changed, so it fails closed.
			if file == nil || file.MIME != string(image.mediaType) || file.Filename != nil && *file.Filename != image.filename || file.Source != nil {
				return 0, 0, observerProblem()
			}
			if !verifyImageDataURL(file.URL, image) {
				return 0, 0, observerProblem()
			}
			return imageInputPart, i, nil
		}
	}
	return 0, 0, observerProblem()
}

// verifyImageDataURL requires the native data URL to carry exactly the original
// attachment bytes: same media type, length and SHA-256.
func verifyImageDataURL(location string, image storedInputImage) bool {
	prefix := "data:" + string(image.mediaType) + ";base64,"
	if !strings.HasPrefix(location, prefix) || uint64(base64Length(image.bytes)) != uint64(len(location)-len(prefix)) {
		return false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(location[len(prefix):])
	if err != nil || uint64(len(raw)) != image.bytes {
		return false
	}
	digest := sha256.Sum256(raw)
	return bytes.Equal([]byte(hex.EncodeToString(digest[:])), []byte(image.sha256))
}

// validInputIdentity binds the checkpoint input identity to the attachments:
// a text-only input keeps the historical text digest, and an image-bearing
// input must carry a different, nonzero digest of its text and references.
func validInputIdentity(text string, images []InputImage, identity [sha256.Size]byte) bool {
	textDigest := sha256.Sum256([]byte(text))
	if len(images) == 0 {
		return identity == textDigest
	}
	return identity != textDigest && identity != [sha256.Size]byte{}
}

// inputIdentity is the digest retained as the checkpoint input identity. Every
// text-only input keeps its historical text digest, which also identifies the
// stored original text part; an image-bearing input binds its ordered images.
func (i *sessionInput) inputIdentity() [sha256.Size]byte {
	if len(i.images) == 0 {
		return i.digest
	}
	return i.identity
}

// partTotal is the exact part count of the complete stored input.
func (i *sessionInput) partTotal() int { return 1 + 2*len(i.images) }

// verifyInputParts validates the stored parts of one input in storage order.
// The original text part comes first, image file parts must follow attachment
// order, and each image has exactly one synthetic Read part. The native server
// orders parts by identifier, so synthetic parts may interleave with files.
// complete is false for a valid strict prefix of the expansion, which is a real
// intermediate native state; anything else fails closed.
func verifyInputParts(input *sessionInput, parts []NativePart) (complete bool, err error) {
	if len(parts) > input.partTotal() {
		return false, sessionProblem()
	}
	seen := map[string]bool{}
	synthetic, files := 0, 0
	for position, part := range parts {
		if seen[part.ID] {
			return false, sessionProblem()
		}
		seen[part.ID] = true
		kind, index, err := classifyInputPart(input, part)
		if err != nil {
			return false, sessionProblem()
		}
		switch kind {
		case originalInputPart:
			if position != 0 {
				return false, sessionProblem()
			}
		case syntheticInputPart:
			synthetic++
			if position == 0 || synthetic > len(input.images) {
				return false, sessionProblem()
			}
		case imageInputPart:
			if position == 0 || index != files {
				return false, sessionProblem()
			}
			files++
		}
	}
	return len(parts) == input.partTotal(), nil
}

// imageDiagnostics lists content-free fields for structured logs.
func (i *sessionInput) imageDiagnostics() []any {
	media := make([]string, len(i.images))
	digests := make([]string, len(i.images))
	for index, image := range i.images {
		media[index], digests[index] = string(image.mediaType), image.sha256
	}
	return []any{"image_count", len(i.images), "image_media_types", media, "image_sha256", digests}
}

// validateStoredImageInput verifies an image-bearing stored input. Image bytes
// are compared only by exact length and SHA-256; neither bytes nor private
// paths enter an error or log.
func validateStoredImageInput(raw []json.RawMessage, input sessionInput) (bool, error) {
	// A valid original row with an empty array is a real intermediate state,
	// exactly as for text-only input: it is neither storage nor rejection.
	if len(raw) == 0 {
		return false, nil
	}
	parts := make([]NativePart, len(raw))
	for i, item := range raw {
		part, err := decodeNativePart(item)
		if err != nil {
			return false, sessionProblem()
		}
		parts[i] = part
	}
	return verifyInputParts(&input, parts)
}

// imageInputPart checks one arriving part of the original image-bearing user
// message. Parts arrive in native creation order: the original text first, then
// each image's synthetic Read part and its file part in attachment order.
func (o *inputObserver) imageInputPart(message *observedMessage, value NativePart, old *observedPart) error {
	kind, index, err := classifyInputPart(&o.input, value)
	if err != nil || old != nil || len(message.parts) >= o.input.partTotal() {
		return observerProblem()
	}
	synthetic, files := 0, 0
	for _, id := range message.parts {
		if seen := o.parts[id]; seen != nil && seen.value.Kind == FilePartKind {
			files++
		} else if seen != nil && id != o.input.receipt.PartID {
			synthetic++
		}
	}
	switch kind {
	case originalInputPart:
		if len(message.parts) != 0 {
			return observerProblem()
		}
	case syntheticInputPart:
		if len(message.parts) == 0 || synthetic >= len(o.input.images) {
			return observerProblem()
		}
	case imageInputPart:
		// The native server stores each image's synthetic Read part first.
		if len(message.parts) == 0 || index != files || synthetic <= files {
			return observerProblem()
		}
		if o.logger != nil {
			o.logger.InfoContext(o.ctx, "opencode_image_input_part_verified", "owner_id", o.owner, "image_index", index, "image_media_type", string(o.input.images[index].mediaType), "image_sha256", o.input.images[index].sha256)
		}
	}
	return nil
}

// validCheckpointInputParts accepts the retained original input message: its
// single original text part, optionally followed by one synthetic text part and
// one file part per image. A checkpoint records kinds and digests only, so the
// exact bytes were verified against the live attachment before retention.
// Replacement, Fork and compaction profiles still reject the file parts.
func validCheckpointInputParts(parts []HistoryPart, partID string) bool {
	if len(parts) == 0 || len(parts) > 1+2*domain.MaxInputImages || len(parts)%2 == 0 || parts[0].ID != partID || parts[0].Kind != TextPartKind {
		return false
	}
	images, text, files := (len(parts)-1)/2, 0, 0
	for _, part := range parts[1:] {
		switch part.Kind {
		case TextPartKind:
			text++
		case FilePartKind:
			files++
		}
	}
	return text == images && files == images
}
