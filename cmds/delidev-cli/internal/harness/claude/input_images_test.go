// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func inputImageFixture(t *testing.T) ImageInput {
	t.Helper()
	var buf bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buf, canvas); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return ImageInput{Reference: domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.NewID(), MediaType: domain.ImagePNG, ByteLength: uint64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}, Bytes: buf.Bytes()}
}

func TestClaudeImageInputPreservesOriginalBytesAndOrderedReferences(t *testing.T) {
	first, second := inputImageFixture(t), inputImageFixture(t)
	for _, prompt := range []string{"", "describe"} {
		raw, refs, err := imageInputContent(prompt, []ImageInput{second, first})
		if err != nil || len(refs) != 2 || refs[0] != second.Reference || refs[1] != first.Reference {
			t.Fatal("ordered original references changed", err)
		}
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(raw, &blocks) != nil {
			t.Fatal("invalid native content")
		}
		offset := 0
		if prompt != "" {
			offset = 1
		}
		if len(blocks) != offset+2 {
			t.Fatal("image block lost")
		}
		for i, expected := range []ImageInput{second, first} {
			var source struct {
				Type  string `json:"type"`
				Media string `json:"media_type"`
				Data  string `json:"data"`
			}
			if json.Unmarshal(blocks[offset+i]["source"], &source) != nil || source.Type != "base64" || source.Media != string(expected.Reference.MediaType) || source.Data != base64.StdEncoding.EncodeToString(expected.Bytes) {
				t.Fatal("original image bytes changed")
			}
		}
		binding := &ExecutionBinding{imageInput: true, imagePrompt: prompt, imageRefs: refs}
		if !binding.matchesReplayContent(raw) {
			t.Fatal("closed image replay rejected")
		}
		for _, invalid := range []json.RawMessage{json.RawMessage(`"describe"`), json.RawMessage(`[]`), append(raw[:len(raw)-1:len(raw)-1], []byte(`,{"type":"text","text":"extra"}]`)...)} {
			if binding.matchesReplayContent(invalid) {
				t.Fatal("missing or extra image replay accepted")
			}
		}
	}
	changed := first
	changed.Bytes = []byte("changed")
	if _, _, err := imageInputContent("describe", []ImageInput{changed}); err == nil {
		t.Fatal("changed Worker bytes accepted")
	}
	duplicate := first
	if _, _, err := imageInputContent("describe", []ImageInput{first, duplicate}); err == nil {
		t.Fatal("duplicate reference accepted")
	}
}

func TestClaudeImageContextChecksDimensionsAndInertLocation(t *testing.T) {
	session := domain.NewID()
	expected := imageContext{width: 4064, height: 2324, displayedWidth: 2000, displayedHeight: 1144, media: domain.ImagePNG}
	text := "[Image: source: /private/tmp/native/" + string(session) + "/images/1.png, original 4064x2324, displayed at 2000x1144. Multiply coordinates by 2.03 to map to original image.]"
	if location, ok := imageContextText(text, expected, session, "/private/tmp/native"); !ok || location != "/private/tmp/native/"+string(session)+"/images/1.png" {
		t.Fatal("original native context rejected")
	}
	for _, other := range []string{text + " extra", "[Image: source: /tmp/foreign.png]", "[Image: source: https://fixture.invalid/image.png]"} {
		if _, ok := imageContextText(other, expected, session, "/private/tmp/native"); ok {
			t.Fatal("foreign native context accepted")
		}
	}
	expected.displayedWidth++
	if _, ok := imageContextText(text, expected, session, "/private/tmp/native"); ok {
		t.Fatal("changed native dimensions accepted")
	}
}

func TestClaudeImageHistoryPinsNativePreprocessedBytes(t *testing.T) {
	session, workspace, records, proofs := historyFixture(t)
	first := inputImageFixture(t)
	content, _, err := imageInputContent("describe", []ImageInput{first})
	if err != nil {
		t.Fatal(err)
	}
	var blocks any
	_ = json.Unmarshal(content, &blocks)
	message := records[1]["message"].(map[string]any)
	message["content"] = blocks
	raw, _ := json.Marshal(map[string]any{"type": "user", "uuid": records[1]["uuid"], "session_id": session, "parent_tool_use_id": nil, "message": message})
	proof, err := ObserveMainHistoryMessage(StreamEvent{Kind: NativeMessage, Type: "user", Body: raw}, session)
	if err != nil {
		t.Fatal(err)
	}
	proofs[0] = proof
	if _, err := VerifyMainTranscript(context.Background(), historyJSONL(t, records), session, workspace, proofs); err != nil {
		t.Fatal("original image history rejected", err)
	}
	block := blocks.([]any)[1].(map[string]any)
	block["source"].(map[string]any)["data"] = base64.StdEncoding.EncodeToString([]byte("different bytes"))
	if _, err := VerifyMainTranscript(context.Background(), historyJSONL(t, records), session, workspace, proofs); err == nil {
		t.Fatal("changed native image history acquired continuation authority")
	}
}

func TestClaudeImageScaleMatchesNativeJavaScriptRounding(t *testing.T) {
	for _, test := range []struct {
		width, displayed int
		scale            string
	}{{4064, 2000, "2.03"}, {4250, 2000, "2.13"}, {5350, 2000, "2.67"}} {
		if actual := nativeImageScale(test.width, test.displayed); actual != test.scale {
			t.Fatalf("native scale %d/%d = %s, want %s", test.width, test.displayed, actual, test.scale)
		}
	}
}

// imageRetentionFixture builds a settled session whose first input carries one
// expected image and whose native JSONL optionally contains the hidden
// image-context record for that input.
func imageRetentionFixture(t *testing.T, hidden bool) (*APISession, string) {
	t.Helper()
	if hidden {
		return multiImageRetentionFixture(t, 1)
	}
	return multiImageRetentionFixture(t)
}

// multiImageRetentionFixture is like imageRetentionFixture, but the first input
// expects one 2x2 PNG per given cache ordinal and the hidden record carries one
// text block per ordinal in that order. No ordinals means no hidden record.
func multiImageRetentionFixture(t *testing.T, ordinals ...int) (*APISession, string) {
	t.Helper()
	hidden := len(ordinals) > 0
	s, _ := continuationFixture(t)
	path := filepath.Join(s.config.Home, "projects", "delidev", string(s.config.SessionID)+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var contextID string
	if hidden {
		contextID = uuid.NewString()
		lines := bytes.Split(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\n'})
		records := make([]map[string]any, 0, len(lines)+1)
		for _, line := range lines {
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				t.Fatal("invalid fixture history")
			}
			records = append(records, record)
		}
		records = slices.Insert(records, 2, imageContextRecord(s, contextID, string(s.current.input), ordinals...))
		records[3]["parentUuid"] = contextID
		if err := os.WriteFile(path, historyJSONL(t, records), 0600); err != nil {
			t.Fatal(err)
		}
	}
	expected := make([]imageContext, max(len(ordinals), 1))
	for i := range expected {
		expected[i] = imageContext{width: 2, height: 2, displayedWidth: 2, displayedHeight: 2, media: domain.ImagePNG}
	}
	s.history.images = map[domain.ID][]imageContext{s.current.input: expected}
	return s, contextID
}

func imageContextRecord(s *APISession, id, parent string, ordinals ...int) map[string]any {
	content := make([]any, 0, len(ordinals))
	for _, ordinal := range ordinals {
		location := filepath.Join(s.config.Home, "image-tmp", string(s.config.SessionID), "images", strconv.Itoa(ordinal)+".png")
		content = append(content, map[string]any{"type": "text", "text": "[Image: source: " + location + "]"})
	}
	body := map[string]any{"role": "user", "content": content}
	return map[string]any{"type": "user", "uuid": id, "parentUuid": parent, "sessionId": s.config.SessionID, "cwd": s.config.Workspace, "version": SupportedVersion, "isSidechain": false, "isMeta": true, "message": body}
}

func TestClaudeImageRetentionRequiresEveryExpectedContextRecord(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		s, id := imageRetentionFixture(t, true)
		if err := s.retainImageContexts(context.Background()); err != nil {
			t.Fatal("complete image history rejected", err)
		}
		if len(s.history.images) != 0 || !slices.ContainsFunc(s.history.messages, func(m HistoryMessageProof) bool { return m.NativeID == id }) {
			t.Fatal("valid context record was not consumed and retained")
		}
		if observed, err := s.readRetainedTranscript(context.Background()); err != nil || observed.MatchedMessages != 3 || observed.AdditionalMessages != 0 {
			t.Fatal("complete image history lost retained-history authority", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		s, _ := imageRetentionFixture(t, false)
		var logs bytes.Buffer
		s.config.Process.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		if err := s.retainImageContexts(context.Background()); err == nil {
			t.Fatal("missing image-context record accepted")
		}
		if len(s.history.images) != 1 || !strings.Contains(logs.String(), "claude_image_context_invalid") || !strings.Contains(logs.String(), "stage=7") || strings.Contains(logs.String(), s.config.Home) {
			t.Fatal("missing record was not logged as a numeric stage only", logs.String())
		}
		if _, err := s.readRetainedTranscript(context.Background()); err == nil {
			t.Fatal("missing image-context record acquired retained-history authority")
		}
		if _, err := s.CloseForContinuation(context.Background()); err == nil {
			t.Fatal("missing image-context record acquired continuation authority")
		}
	})
	t.Run("partial", func(t *testing.T) {
		s, _ := imageRetentionFixture(t, true)
		s.history.images[domain.NewID()] = []imageContext{{width: 2, height: 2, displayedWidth: 2, displayedHeight: 2, media: domain.ImagePNG}}
		if err := s.retainImageContexts(context.Background()); err == nil {
			t.Fatal("partial image-context history accepted")
		}
		if len(s.history.images) != 1 {
			t.Fatal("unmatched expected image input was consumed")
		}
	})
	t.Run("text only", func(t *testing.T) {
		s, _ := continuationFixture(t)
		if err := s.retainImageContexts(context.Background()); err != nil {
			t.Fatal("text-only history changed", err)
		}
	})
}

func TestClaudeImageRetentionRequiresDistinctContextLocations(t *testing.T) {
	t.Run("distinct accepted", func(t *testing.T) {
		s, id := multiImageRetentionFixture(t, 1, 2)
		if err := s.retainImageContexts(context.Background()); err != nil {
			t.Fatal("distinct image locations rejected", err)
		}
		if len(s.history.images) != 0 || !slices.ContainsFunc(s.history.messages, func(m HistoryMessageProof) bool { return m.NativeID == id }) {
			t.Fatal("distinct context record was not consumed and retained")
		}
		if observed, err := s.readRetainedTranscript(context.Background()); err != nil || observed.MatchedMessages != 3 || observed.AdditionalMessages != 0 {
			t.Fatal("distinct image history lost retained-history authority", err)
		}
	})
	t.Run("non contiguous distinct accepted", func(t *testing.T) {
		s, _ := multiImageRetentionFixture(t, 3, 1)
		if err := s.retainImageContexts(context.Background()); err != nil {
			t.Fatal("ordered distinct locations without monotonicity rejected", err)
		}
	})
	t.Run("repeated rejected before retention", func(t *testing.T) {
		s, id := multiImageRetentionFixture(t, 1, 1)
		var logs bytes.Buffer
		s.config.Process.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		messages := slices.Clone(s.history.messages)
		err := s.retainImageContexts(context.Background())
		if err == nil {
			t.Fatal("repeated image-context location accepted")
		}
		if len(s.history.images) != 1 || !slices.Equal(messages, s.history.messages) || slices.ContainsFunc(s.history.messages, func(m HistoryMessageProof) bool { return m.NativeID == id }) {
			t.Fatal("repeated location inserted a proof or cleared the expectation")
		}
		if !strings.Contains(logs.String(), "claude_image_context_invalid") || !strings.Contains(logs.String(), "stage=8") || strings.Contains(logs.String(), s.config.Home) || strings.Contains(err.Error(), s.config.Home) {
			t.Fatal("repeated location was not reported as a numeric stage only", logs.String())
		}
		if _, err := s.readRetainedTranscript(context.Background()); err == nil {
			t.Fatal("repeated image-context location acquired retained-history authority")
		}
		if _, err := s.CloseForContinuation(context.Background()); err == nil {
			t.Fatal("repeated image-context location acquired continuation authority")
		}
	})
	t.Run("swapped dimensions still refused", func(t *testing.T) {
		s, _ := multiImageRetentionFixture(t, 1, 2)
		s.history.images[s.current.input][1].width = 4
		if err := s.retainImageContexts(context.Background()); err == nil {
			t.Fatal("mismatched block dimensions accepted")
		}
		if len(s.history.images) != 1 {
			t.Fatal("mismatched block consumed the expectation")
		}
	})
}
