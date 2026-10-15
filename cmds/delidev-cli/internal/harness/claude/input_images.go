// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"math/big"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// ImageInput carries original validated Worker bytes privately. Neither bytes nor
// native paths enter public observations or retained checkpoint documents.
type ImageInput struct {
	Reference domain.ImageAttachment `json:"-"`
	Bytes     []byte                 `json:"-"`
}

const maxImageInputFrame = 64 << 20

func imageInputContent(prompt string, images []ImageInput) (json.RawMessage, []domain.ImageAttachment, error) {
	refs := make([]domain.ImageAttachment, len(images))
	blocks := make([]any, 0, len(images)+1)
	if prompt != "" {
		blocks = append(blocks, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{"text", prompt})
	}
	for i, image := range images {
		refs[i] = image.Reference
		if err := image.Reference.ValidateContent(image.Bytes); err != nil {
			return nil, nil, err
		}
		blocks = append(blocks, struct {
			Type   string `json:"type"`
			Source struct {
				Type  string                `json:"type"`
				Media domain.ImageMediaType `json:"media_type"`
				Data  string                `json:"data"`
			} `json:"source"`
		}{"image", struct {
			Type  string                `json:"type"`
			Media domain.ImageMediaType `json:"media_type"`
			Data  string                `json:"data"`
		}{"base64", image.Reference.MediaType, base64.StdEncoding.EncodeToString(image.Bytes)}})
	}
	if len(images) == 0 || domain.ValidateImageAttachments(refs) != nil || domain.Text(prompt, "native input", 256<<10, false) != nil {
		return nil, nil, domain.InvalidImageInput()
	}
	raw, err := json.Marshal(blocks)
	return raw, refs, err
}

func (s *Stream) sendImageInput(ctx context.Context, id, session domain.ID, content json.RawMessage) error {
	if id.Validate() != nil || session.Validate() != nil || len(content) == 0 || len(content) > maxImageInputFrame || s.frameLimit != maxImageInputFrame {
		return domain.InvalidImageInput()
	}
	raw, err := json.Marshal(struct {
		Type    string    `json:"type"`
		UUID    domain.ID `json:"uuid"`
		Session domain.ID `json:"session_id"`
		Parent  *string   `json:"parent_tool_use_id"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}{"user", id, session, nil, struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}{"user", content}})
	if err != nil || len(raw) > maxImageInputFrame {
		return domain.InvalidImageInput()
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	s.mu.Lock()
	err = s.claimLocked(id)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.write(ctx, raw)
}

// SendImageInput binds both the immutable product input and the exact ordered
// native block payload before transmission. Replay checks the native image
// profile; retained history independently checks the exact replay payload.
func (s *APISession) SendImageInput(ctx context.Context, input domain.ID, prompt string, images []ImageInput, intent InputIntent) (AppliedSettings, error) {
	content, refs, err := imageInputContent(prompt, images)
	if err != nil {
		return AppliedSettings{}, err
	}
	return s.sendInput(ctx, input, prompt, intent, content, (domain.SessionInput{Prompt: prompt, Attachments: refs}).InputDigest(), inputImageContexts(images))
}

// Claude preprocesses image bytes before its replay (including native resizing).
// The original stdin write binds original bytes; this closed native replay binds
// its resulting ordered image blocks. Native JSONL must match this replay exactly.
func (b *ExecutionBinding) matchesImageReplay(raw json.RawMessage) bool {
	var blocks []json.RawMessage
	if domain.DecodeBounded(raw, &blocks, maxImageInputFrame) != nil {
		return false
	}
	offset := 0
	if b.imagePrompt != "" {
		if len(blocks) == 0 {
			return false
		}
		var text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if decodeNativeObject(blocks[0], &text) != nil || text.Type != "text" || text.Text != b.imagePrompt {
			return false
		}
		offset = 1
	}
	if len(blocks) != offset+len(b.imageRefs) {
		return false
	}
	for i, ref := range b.imageRefs {
		var block struct {
			Type   string          `json:"type"`
			Source json.RawMessage `json:"source"`
		}
		var source struct {
			Type  string                `json:"type"`
			Media domain.ImageMediaType `json:"media_type"`
			Data  string                `json:"data"`
		}
		if decodeNativeObject(blocks[offset+i], &block) != nil || block.Type != "image" || decodeNativeObject(block.Source, &source) != nil || source.Type != "base64" || source.Media != ref.MediaType || len(source.Data) > base64.StdEncoding.EncodedLen(domain.MaxInputImageBytes) {
			return false
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(source.Data)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != source.Data || domain.ValidateImageContent(decoded, source.Media) != nil {
			return false
		}
	}
	return true
}

type imageContext struct {
	width, height, displayedWidth, displayedHeight int
	media                                          domain.ImageMediaType
}

func inputImageContexts(images []ImageInput) []imageContext {
	contexts := make([]imageContext, len(images))
	for i, v := range images {
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(v.Bytes))
		contexts[i] = imageContext{width: cfg.Width, height: cfg.Height, media: v.Reference.MediaType}
	}
	return contexts
}

func (h *sessionHistory) retainImageReplay(input domain.ID, raw json.RawMessage) error {
	contexts := h.images[input]
	if len(contexts) == 0 {
		return nil
	}
	var envelope struct {
		Message struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return historyUncertain()
	}
	offset := len(envelope.Message.Content) - len(contexts)
	if offset < 0 {
		return historyUncertain()
	}
	for i := range contexts {
		var block struct {
			Source struct {
				Data string `json:"data"`
			} `json:"source"`
		}
		if json.Unmarshal(envelope.Message.Content[offset+i], &block) != nil {
			return historyUncertain()
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(block.Source.Data)
		if err != nil {
			return historyUncertain()
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
		if err != nil {
			return historyUncertain()
		}
		contexts[i].displayedWidth, contexts[i].displayedHeight = cfg.Width, cfg.Height
	}
	return nil
}

// Native image context is a hidden user record, not another user input. Its
// original dimensions come from immutable Worker bytes; displayed dimensions
// come from the validated native replay. The cache location is inert metadata:
// it never grants a file read, publication, deletion or workspace capability.
func imageContextText(text string, expected imageContext, session domain.ID, roots ...string) (string, bool) {
	if expected.width <= 0 || expected.height <= 0 || expected.displayedWidth <= 0 || expected.displayedHeight <= 0 {
		return "", false
	}
	prefix := "[Image: source: "
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, "]") {
		return "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, prefix), "]")
	if expected.width != expected.displayedWidth || expected.height != expected.displayedHeight {
		suffix := fmt.Sprintf(", original %dx%d, displayed at %dx%d. Multiply coordinates by %s to map to original image.", expected.width, expected.height, expected.displayedWidth, expected.displayedHeight, nativeImageScale(expected.width, expected.displayedWidth))
		if !strings.HasSuffix(body, suffix) {
			return "", false
		}
		body = strings.TrimSuffix(body, suffix)
	}
	if !filepath.IsAbs(body) || filepath.Clean(body) != body || domain.Text(body, "native image context location", 4096, true) != nil {
		return "", false
	}
	if len(roots) != 1 {
		return "", false
	}
	relative, err := filepath.Rel(roots[0], body)
	if err != nil || !filepath.IsLocal(relative) {
		return "", false
	}
	parent := filepath.Dir(body)
	if filepath.Base(parent) != "images" || filepath.Base(filepath.Dir(parent)) != string(session) {
		return "", false
	}
	ext := filepath.Ext(body)
	allowed := map[domain.ImageMediaType]string{domain.ImagePNG: ".png", domain.ImageJPEG: ".jpg", domain.ImageWebP: ".webp"}
	if ext != allowed[expected.media] {
		return "", false
	}
	ordinal, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(body), ext))
	if err != nil || ordinal <= 0 || strconv.Itoa(ordinal)+ext != filepath.Base(body) {
		return "", false
	}
	return body, true
}

func (s *APISession) retainImageContexts(ctx context.Context) error {
	h := s.history
	if len(h.images) == 0 {
		return nil
	}
	scope, err := openHistoryFiles(ctx, historyHome(s.config))
	if err != nil {
		return err
	}
	defer scope.Close()
	raw, err := scope.read(ctx, filepath.Join("projects", "delidev", string(s.config.SessionID)+".jsonl"), maxHistoryTranscript)
	if err != nil {
		return err
	}
	if !protectedTranscriptSafe(raw, security.NewProtectedJSON(s.config.ProtectedValues())) {
		return imageContextFailure(s, 1)
	}
	known := map[string]bool{}
	for _, v := range h.messages {
		known[v.NativeID] = true
	}
	parents := map[string]string{}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var node struct {
			UUID   string `json:"uuid"`
			Parent string `json:"parentUuid"`
		}
		_ = json.Unmarshal(line, &node)
		if node.UUID != "" {
			parents[node.UUID] = node.Parent
		}
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if domain.DecodeBounded(line, &fields, maxImageInputFrame) != nil {
			return imageContextFailure(s, 2)
		}
		var kind, id, parent string
		var meta bool
		_ = json.Unmarshal(fields["type"], &kind)
		_ = json.Unmarshal(fields["uuid"], &id)
		_ = json.Unmarshal(fields["parentUuid"], &parent)
		_ = json.Unmarshal(fields["isMeta"], &meta)
		source := domain.ID(parent)
		expected, ok := h.images[source]
		for steps := 0; !ok && source != "" && steps < len(parents); steps++ {
			source = domain.ID(parents[string(source)])
			expected, ok = h.images[source]
		}

		if !ok || known[id] || kind != "user" || !meta {
			continue
		}
		var message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if decodeNativeObject(fields["message"], &message) != nil || message.Role != "user" || len(message.Content) != len(expected) || !nativeUUID(id) {
			return imageContextFailure(s, 3)
		}
		// Equal media types and dimensions do not prove image identity, so a
		// record that repeats one cache location must not clear the pending
		// expectation. Block order stays the expected input order.
		locations := make(map[string]struct{}, len(message.Content))
		for i, block := range message.Content {
			if block.Type != "text" {
				return imageContextFailure(s, 4)
			}
			location, valid := imageContextText(block.Text, expected[i], s.config.SessionID, filepath.Join(historyHome(s.config), "image-tmp"))
			if !valid {
				return imageContextFailure(s, 4)
			}
			if _, repeated := locations[location]; repeated {
				return imageContextFailure(s, imageContextStageDuplicate)
			}
			locations[location] = struct{}{}
		}
		role, digest, err := historyPayloadDigest(fields["message"], false)
		if err != nil {
			return err
		}
		position := slices.IndexFunc(h.messages, func(proof HistoryMessageProof) bool { return proof.NativeID == parent })
		if position < 0 {
			position = slices.IndexFunc(h.messages, func(proof HistoryMessageProof) bool { return proof.NativeID == string(source) })
		}
		if position < 0 {
			return imageContextFailure(s, 5)
		}
		if s.current.seen[id] || len(s.current.seen) >= domain.MaxExecutionEvents {
			return imageContextFailure(s, 6)
		}
		s.current.seen[id] = true
		h.messages = slices.Insert(h.messages, position+1, HistoryMessageProof{NativeID: id, Role: role, PayloadSHA256: fmt.Sprintf("%x", digest)})
		delete(h.images, source)
		known[id] = true
	}
	// Every expected image input must have produced its hidden native context
	// record. A missing record adds no unexpected message, so the later
	// additional-message comparison cannot detect it.
	if len(h.images) != 0 {
		return imageContextFailure(s, imageContextStageMissing)
	}
	return nil
}

// imageContextStageMissing is the numeric claude_image_context_invalid stage
// for an expected image input that has no hidden native context record.
const imageContextStageMissing = 7

// imageContextStageDuplicate is the numeric claude_image_context_invalid stage
// for a hidden context record whose blocks repeat one native-cache location.
const imageContextStageDuplicate = 8

func imageContextFailure(s *APISession, stage int) error {
	if s.config.Process.Logger != nil {
		s.config.Process.Logger.Warn("claude_image_context_invalid", "stage", stage)
	}
	return historyUncertain()
}

// Match JavaScript Number.toFixed(2), including positive halfway values. Go's
// default formatter rounds ties to even, which rejects valid native dimensions
// such as 4250/2000 (the CLI emits 2.13). Keep the exact binary float fraction.
func nativeImageScale(width, displayed int) string {
	ratio := new(big.Rat).SetFloat64(float64(width) / float64(displayed))
	ratio.Mul(ratio, big.NewRat(100, 1))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(ratio.Num(), ratio.Denom(), remainder)
	if remainder.Lsh(remainder, 1).Cmp(ratio.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	value := whole.Int64()
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}
