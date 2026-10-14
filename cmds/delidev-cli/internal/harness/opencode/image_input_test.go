package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// fixturePNG is a deterministic one-pixel red PNG. The same shape is captured
// by the native probe cited in the contract (a 1x1 PNG).
func fixturePNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 1, 1))
	canvas.Set(0, 0, color.RGBA{R: 255, G: shade, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func fixtureInputImage(t *testing.T, shade uint8) (InputImage, []byte) {
	t.Helper()
	raw := fixturePNG(t, shade)
	digest := sha256.Sum256(raw)
	return InputImage{MediaType: domain.ImagePNG, SHA256: hex.EncodeToString(digest[:]), Bytes: uint64(len(raw)), Path: filepath.Join(fixtureNativeRoot(), "private", "worker", "image-inputs", fmt.Sprintf("00000000-0000-4000-8000-%012d.data", shade))}, raw
}

func imageFixtureInput(t *testing.T, images ...InputImage) sessionInput {
	t.Helper()
	stored, ok := storedImages(fixturePartID, images)
	if !ok {
		t.Fatal("fixture images rejected")
	}
	return sessionInput{receipt: InputReceipt{SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, digest: sha256.Sum256([]byte("describe")), images: stored}
}

func nativeTextPart(id, text string, synthetic bool) map[string]any {
	part := map[string]any{"id": id, "sessionID": fixtureSessionID, "messageID": fixtureMessageID, "type": "text", "text": text}
	if synthetic {
		part["synthetic"] = true
	}
	return part
}

func nativeFilePart(t *testing.T, input sessionInput, index int, raw []byte) map[string]any {
	t.Helper()
	image := input.images[index]
	return map[string]any{"id": image.partID, "sessionID": fixtureSessionID, "messageID": fixtureMessageID, "type": "file", "mime": string(image.mediaType), "filename": image.filename, "url": "data:" + string(image.mediaType) + ";base64," + base64.StdEncoding.EncodeToString(raw)}
}

var foreignPartID = "prt_01960dcbf99f" + strings.Repeat("f", 14)

func syntheticPart(n int) map[string]any {
	return nativeTextPart(fmt.Sprintf("prt_%012xzzzzzzzzzzzzzz", 0x1960dcbf000+n), `Called the Read tool with the following input: {"filePath":"/private/worker/image.data"}`, true)
}

func storedMessage(t *testing.T, input sessionInput, parts ...map[string]any) []byte {
	t.Helper()
	value := fixtureInput(input.receipt, fixtureSettings(), "describe")
	list := make([]any, len(parts))
	for i, part := range parts {
		list[i] = part
	}
	value["parts"] = list
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestImageInputEncodingIsDeterministicAndOpaque(t *testing.T) {
	image, _ := fixtureInputImage(t, 0)
	second, _ := fixtureInputImage(t, 1)
	body, err := encodeInput(fixtureSettings(), fixtureMessageID, fixturePartID, "describe", []InputImage{image, second}, false)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Parts []map[string]string `json:"parts"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Parts) != 3 {
		t.Fatalf("unexpected body: %s %v", body, err)
	}
	if decoded.Parts[0]["type"] != "text" || decoded.Parts[0]["id"] != fixturePartID || decoded.Parts[0]["text"] != "describe" {
		t.Fatalf("text part must come first: %v", decoded.Parts[0])
	}
	for i, part := range decoded.Parts[1:] {
		id, _ := imagePartID(fixturePartID, i)
		url := "file://" + filepath.ToSlash([]InputImage{image, second}[i].Path)
		if !strings.HasPrefix(url, "file:///") {
			url = "file:///" + strings.TrimPrefix(url, "file://")
		}
		if part["type"] != "file" || part["id"] != id || part["mime"] != "image/png" || part["filename"] != fmt.Sprintf("image-%d.png", i+1) || part["url"] != url {
			t.Fatalf("unexpected file part %d: %v", i, part)
		}
		if !nativeID(part["id"], "prt") || part["id"] <= fixturePartID {
			t.Fatalf("file part identity must be a later native part ID: %s", part["id"])
		}
	}
	again, err := encodeInput(fixtureSettings(), fixtureMessageID, fixturePartID, "describe", []InputImage{image, second}, false)
	if err != nil || !bytes.Equal(body, again) {
		t.Fatal("encoding is not deterministic")
	}
}

func TestImageInputClaimDigestBindsOrderedDigestsAndIgnoresPaths(t *testing.T) {
	first, _ := fixtureInputImage(t, 0)
	second, _ := fixtureInputImage(t, 1)
	digest, err := InputClaimDigest(fixtureSettings(), fixtureMessageID, fixturePartID, "describe", []InputImage{first, second})
	if err != nil {
		t.Fatal(err)
	}
	moved := []InputImage{first, second}
	moved[0].Path = filepath.Join(fixtureNativeRoot(), "other", "place.data")
	if other, err := InputClaimDigest(fixtureSettings(), fixtureMessageID, fixturePartID, "describe", moved); err != nil || other != digest {
		t.Fatal("claim digest must not depend on the private Worker path")
	}
	for name, mutated := range map[string]struct {
		text   string
		images []InputImage
	}{
		"reordered": {"describe", []InputImage{second, first}},
		"text":      {"changed", []InputImage{first, second}},
		"fewer":     {"describe", []InputImage{first}},
	} {
		if other, err := InputClaimDigest(fixtureSettings(), fixtureMessageID, fixturePartID, mutated.text, mutated.images); err != nil || other == digest {
			t.Fatalf("%s input kept the same claim digest", name)
		}
	}
	// The submitted body must hash to the same digest the Worker derives from
	// the assignment, even though it carries the private path.
	body, err := encodeInput(fixtureSettings(), fixtureMessageID, fixturePartID, "describe", []InputImage{first, second}, true)
	if err != nil || mutationDigest(body) != digest || bytes.Contains(body, []byte("worker")) {
		t.Fatal("claim body leaked a private path or diverged from the digest")
	}
}

func TestTextOnlyInputEncodingAndDigestStayByteIdentical(t *testing.T) {
	settings := fixtureSettings()
	golden := `{"messageID":"` + fixtureMessageID + `","model":{"providerID":"private-provider","modelID":"private-model"},"agent":"build","parts":[{"id":"` + fixturePartID + `","type":"text","text":"hi"}]}`
	for _, claim := range []bool{false, true} {
		body, err := encodeInput(settings, fixtureMessageID, fixturePartID, "hi", nil, claim)
		if err != nil || string(body) != golden {
			t.Fatalf("text-only body changed: %s %v", body, err)
		}
	}
	want, err := TextInputClaimDigest(settings, fixtureMessageID, fixturePartID, "hi")
	if err != nil || want != mutationDigest([]byte(golden)) {
		t.Fatal("text-only digest changed")
	}
	if got, err := InputClaimDigest(settings, fixtureMessageID, fixturePartID, "hi", nil); err != nil || got != want {
		t.Fatal("image-aware digest diverged for text-only input")
	}
}

func TestImageInputRejectsUnboundedOrInvalidAttachments(t *testing.T) {
	image, _ := fixtureInputImage(t, 0)
	bad := map[string][]InputImage{
		"media":    {{MediaType: "image/gif", SHA256: image.SHA256, Bytes: 10, Path: image.Path}},
		"digest":   {{MediaType: domain.ImagePNG, SHA256: strings.ToUpper(image.SHA256), Bytes: 10, Path: image.Path}},
		"empty":    {{MediaType: domain.ImagePNG, SHA256: image.SHA256, Bytes: 0, Path: image.Path}},
		"relative": {{MediaType: domain.ImagePNG, SHA256: image.SHA256, Bytes: 10, Path: "relative.data"}},
		"huge":     {{MediaType: domain.ImagePNG, SHA256: image.SHA256, Bytes: domain.MaxOpenCodeInputImagesBytes + 1, Path: image.Path}},
		"count":    make([]InputImage, domain.MaxInputImages+1),
	}
	for name, images := range bad {
		if _, err := encodeInput(fixtureSettings(), fixtureMessageID, fixturePartID, "describe", images, false); err == nil {
			t.Fatalf("%s attachments were encoded", name)
		}
	}
	if _, err := encodeInput(fixtureSettings(), fixtureMessageID, fixturePartID, "  ", []InputImage{image}, false); err == nil {
		t.Fatal("image-only OpenCode input must fail closed before native send")
	}
	large := strings.Repeat("x", 900<<10)
	if _, err := encodeInput(fixtureSettings(), fixtureMessageID, fixturePartID, large, []InputImage{{MediaType: domain.ImagePNG, SHA256: image.SHA256, Bytes: 200 << 10, Path: image.Path}}, false); err == nil {
		t.Fatal("stored message above the adapter response bound was encoded")
	}
}

func TestStoredImageInputAcceptsExactNativeExpansion(t *testing.T) {
	first, firstRaw := fixtureInputImage(t, 0)
	second, secondRaw := fixtureInputImage(t, 1)
	input := imageFixtureInput(t, first, second)
	original := nativeTextPart(fixturePartID, "describe", false)
	orders := map[string][]map[string]any{
		"native creation order": {original, syntheticPart(1), nativeFilePart(t, input, 0, firstRaw), syntheticPart(2), nativeFilePart(t, input, 1, secondRaw)},
		"identifier order":      {original, nativeFilePart(t, input, 0, firstRaw), nativeFilePart(t, input, 1, secondRaw), syntheticPart(1), syntheticPart(2)},
	}
	for name, parts := range orders {
		recorded, err := validateStoredInput(storedMessage(t, input, parts...), fixtureSettings(), input)
		if err != nil || !recorded {
			t.Fatalf("%s: exact expansion rejected: %v", name, err)
		}
	}
	// Intermediate native rows are unconfirmed, never rejected or recorded.
	for count := 0; count < 5; count++ {
		recorded, err := validateStoredInput(storedMessage(t, input, orders["native creation order"][:count]...), fixtureSettings(), input)
		if (count < 5) && (err != nil || recorded) {
			t.Fatalf("prefix of %d parts misclassified: recorded=%v err=%v", count, recorded, err)
		}
	}
}

func TestStoredImageInputFailsClosedOnEveryMutation(t *testing.T) {
	first, firstRaw := fixtureInputImage(t, 0)
	second, secondRaw := fixtureInputImage(t, 1)
	input := imageFixtureInput(t, first, second)
	build := func() []map[string]any {
		return []map[string]any{nativeTextPart(fixturePartID, "describe", false), syntheticPart(1), nativeFilePart(t, input, 0, firstRaw), syntheticPart(2), nativeFilePart(t, input, 1, secondRaw)}
	}
	cases := map[string]func([]map[string]any) []map[string]any{
		"changed data URL bytes": func(p []map[string]any) []map[string]any {
			p[2]["url"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(append(bytes.Clone(firstRaw), 0))
			return p
		},
		"same-length different bytes": func(p []map[string]any) []map[string]any {
			changed := bytes.Clone(firstRaw)
			changed[len(changed)-1] ^= 0xff
			p[2]["url"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(changed)
			return p
		},
		"swapped attachments": func(p []map[string]any) []map[string]any {
			p[2]["url"], p[4]["url"] = p[4]["url"], p[2]["url"]
			return p
		},
		"reordered file parts": func(p []map[string]any) []map[string]any {
			p[2], p[4] = p[4], p[2]
			return p
		},
		"different mime": func(p []map[string]any) []map[string]any { p[2]["mime"] = "image/jpeg"; return p },
		"different data mime": func(p []map[string]any) []map[string]any {
			p[2]["url"] = strings.Replace(p[2]["url"].(string), "image/png", "image/jpeg", 1)
			return p
		},
		"user filename": func(p []map[string]any) []map[string]any { p[2]["filename"] = "holiday.png"; return p },
		"file URL kept": func(p []map[string]any) []map[string]any { p[2]["url"] = "file:///private/worker/a.data"; return p },
		"extra fourth part": func(p []map[string]any) []map[string]any {
			return append(p, nativeTextPart(foreignPartID, "extra", true))
		},
		"extra plain text": func(p []map[string]any) []map[string]any {
			p[1] = nativeTextPart(syntheticPart(1)["id"].(string), "plain", false)
			return p
		},
		"synthetic wrong prefix": func(p []map[string]any) []map[string]any { p[1]["text"] = "Called some other tool"; return p },
		"synthetic not flagged":  func(p []map[string]any) []map[string]any { delete(p[1], "synthetic"); return p },
		"changed original text":  func(p []map[string]any) []map[string]any { p[0]["text"] = "changed"; return p },
		"original not first":     func(p []map[string]any) []map[string]any { p[0], p[1] = p[1], p[0]; return p },
		"unknown file id": func(p []map[string]any) []map[string]any {
			p[2]["id"] = "prt_01960dcbe1faXXXXXXXXXXXXXX"
			return p
		},
		"duplicate file part": func(p []map[string]any) []map[string]any { p[4] = nativeFilePart(t, input, 0, firstRaw); return p },
		"third synthetic":     func(p []map[string]any) []map[string]any { p[4] = syntheticPart(3); return p },
		"ignored synthetic":   func(p []map[string]any) []map[string]any { p[1]["ignored"] = true; return p },
		"foreign session":     func(p []map[string]any) []map[string]any { p[2]["sessionID"] = "ses_foreign"; return p },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			recorded, err := validateStoredInput(storedMessage(t, input, mutate(build())...), fixtureSettings(), input)
			if err == nil {
				t.Fatalf("mutation accepted (recorded=%v)", recorded)
			}
			if code := domain.SafeError(err).Code; code != domain.Unsupported {
				t.Fatalf("unexpected error code %v", code)
			}
		})
	}
}

func TestStoredTextOnlyInputStillRejectsExpansion(t *testing.T) {
	input := sessionInput{receipt: InputReceipt{SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, digest: sha256.Sum256([]byte("describe"))}
	if _, err := validateStoredInput(storedMessage(t, input, nativeTextPart(fixturePartID, "describe", false), syntheticPart(1)), fixtureSettings(), input); err == nil {
		t.Fatal("a text-only input accepted unexpected native expansion")
	}
}

func TestCheckpointInputPartsAcceptOnlyTheClosedExpansion(t *testing.T) {
	part := func(id string, kind PartKind) HistoryPart { return HistoryPart{ID: id, Kind: kind} }
	original := part(fixturePartID, TextPartKind)
	if !validCheckpointInputParts([]HistoryPart{original}, fixturePartID) {
		t.Fatal("text-only input rejected")
	}
	expanded := []HistoryPart{original, part("prt_a", TextPartKind), part("prt_b", FilePartKind)}
	if !validCheckpointInputParts(expanded, fixturePartID) {
		t.Fatal("one-image expansion rejected")
	}
	for name, parts := range map[string][]HistoryPart{
		"two parts":      {original, part("prt_a", FilePartKind)},
		"two files":      {original, part("prt_a", FilePartKind), part("prt_b", FilePartKind)},
		"tool":           {original, part("prt_a", TextPartKind), part("prt_b", ToolPartKind)},
		"wrong original": {part("prt_x", TextPartKind), part("prt_a", TextPartKind), part("prt_b", FilePartKind)},
		"empty":          {},
	} {
		if validCheckpointInputParts(parts, fixturePartID) {
			t.Fatalf("%s accepted", name)
		}
	}
	// Replacement, Fork and compaction profiles keep rejecting image history.
	if checkpointReplacementProfile(nativeCheckpoint{History: HistoryObservation{Messages: []HistoryMessage{{Parts: expanded}}}}) == nil {
		t.Fatal("replacement profile admitted image-bearing history")
	}
	if forkSourceProfile(nativeCheckpoint{}) == nil {
		t.Fatal("fork profile precondition changed")
	}
}

func newImageObserver(t *testing.T, input sessionInput) *observerFixture {
	t.Helper()
	creation := &sessionCreation{request: domain.NewID(), settings: fixtureSettings(), identity: sessionIdentity{id: fixtureSessionID, project: "global", slug: "private-fixture", created: 1234}}
	input.receipt.RequestID = domain.NewID()
	api := &sessionAPI{creation: creation, input: &input, events: &eventStream{ctx: context.Background()}, gate: make(chan struct{}, 1), cwd: fixtureWorkspacePath()}
	o, err := api.observeInput(context.Background(), fixtureObserverRoot())
	if err != nil {
		t.Fatal(err)
	}
	stored := fixtureInput(input.receipt, creation.settings, "describe")
	return &observerFixture{t: t, o: o, user: stored["info"].(map[string]any), input: stored["parts"].([]any)[0].(map[string]any), a: fixtureAssistant()}
}

func TestObserverRequiresCompleteExpansionBeforeInputIsStored(t *testing.T) {
	first, firstRaw := fixtureInputImage(t, 0)
	input := imageFixtureInput(t, first)
	f := newImageObserver(t, input)
	f.message(f.user)
	f.part(f.input)
	if progress := f.o.snapshot(); !progress.UserSeen || progress.InputPartSeen {
		t.Fatal("the original text part alone made an image-bearing input stored")
	}
	f.part(syntheticPart(1))
	if f.o.snapshot().InputPartSeen {
		t.Fatal("synthetic part completed the expansion")
	}
	f.part(nativeFilePart(t, input, 0, firstRaw))
	if progress := f.o.snapshot(); !progress.InputPartSeen || progress.NeedsRecovery {
		t.Fatalf("complete expansion was not accepted: %+v", progress)
	}
}

func TestObserverRejectsUnexpectedInputExpansion(t *testing.T) {
	first, firstRaw := fixtureInputImage(t, 0)
	second, secondRaw := fixtureInputImage(t, 1)
	input := imageFixtureInput(t, first, second)
	changed := bytes.Clone(firstRaw)
	changed[len(changed)-1] ^= 0xff
	file := func(index int, raw []byte) map[string]any { return nativeFilePart(t, input, index, raw) }
	for name, parts := range map[string][]map[string]any{
		"file before synthetic":   {file(0, firstRaw)},
		"digest mismatch":         {syntheticPart(1), file(0, changed)},
		"reordered attachments":   {syntheticPart(1), file(1, secondRaw)},
		"too many synthetic":      {syntheticPart(1), syntheticPart(2), syntheticPart(3)},
		"foreign extra text":      {nativeTextPart(foreignPartID, "extra", false)},
		"synthetic wrong prefix":  {nativeTextPart(syntheticPart(1)["id"].(string), "other", true)},
		"changed original":        {nativeTextPart(fixturePartID, "changed", false)},
		"unknown file identifier": {syntheticPart(1), func() map[string]any { v := file(0, firstRaw); v["id"] = "prt_01960dcbe1faXXXXXXXXXXXXXX"; return v }()},
	} {
		t.Run(name, func(t *testing.T) {
			f := newImageObserver(t, input)
			f.message(f.user)
			f.part(f.input)
			var failure error
			for _, part := range parts {
				if _, err := f.o.observe(context.Background(), f.event(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": part, "time": 1240})); err != nil {
					failure = err
					break
				}
			}
			if failure == nil {
				t.Fatal("unexpected expansion was accepted")
			}
			if !f.o.snapshot().NeedsRecovery {
				t.Fatal("unexpected expansion did not latch recovery")
			}
		})
	}
}

func TestImageInputDiagnosticsContainOnlyCountsTypesAndDigests(t *testing.T) {
	first, _ := fixtureInputImage(t, 0)
	input := imageFixtureInput(t, first)
	fields := fmt.Sprint(input.imageDiagnostics())
	if !strings.Contains(fields, first.SHA256) || !strings.Contains(fields, "image/png") || strings.Contains(fields, "worker") || strings.Contains(fields, "image-1") {
		t.Fatalf("diagnostics are not limited to counts, media types and digests: %s", fields)
	}
}
