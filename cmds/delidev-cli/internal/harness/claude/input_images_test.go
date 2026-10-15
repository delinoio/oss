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
	"testing"

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
	if !imageContextText(text, expected, session, "/private/tmp/native") {
		t.Fatal("original native context rejected")
	}
	for _, other := range []string{text + " extra", "[Image: source: /tmp/foreign.png]", "[Image: source: https://fixture.invalid/image.png]"} {
		if imageContextText(other, expected, session, "/private/tmp/native") {
			t.Fatal("foreign native context accepted")
		}
	}
	expected.displayedWidth++
	if imageContextText(text, expected, session, "/private/tmp/native") {
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
