package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeRichContent(t *testing.T) {
	t.Run("image", func(t *testing.T) { testManualNativeRichContent(t, false) })
	t.Run("document", func(t *testing.T) { testManualNativeRichContent(t, true) })
}

func testManualNativeRichContent(t *testing.T, document bool) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-image")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", DefaultPermission
	raster := image.NewRGBA(image.Rect(0, 0, 1, 1))
	raster.SetRGBA(0, 0, color.RGBA{R: 200, G: 50, B: 80, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, raster); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.Workspace, "private-image-fixture.png")
	wantKind, wantMedia := ImageBlock, PNGMedia
	if document {
		encoded.Reset()
		encoded.Write(nativePDFFixture())
		path = filepath.Join(cfg.Workspace, "private-document-fixture.pdf")
		wantKind, wantMedia = DocumentBlock, PDFMedia
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native image provider authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		if err != nil || len(raw) > maxStreamFrame {
			t.Error("native image request exceeded its bound")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		switch n {
		case 1:
			nativeFixtureToolResponse(w, n, "Read", "toolu_image_read", map[string]any{"file_path": path})
		case 2:
			result, ok := nativeFixtureToolResults(t, raw)["toolu_image_read"]
			if !ok || result.Error || !bytes.Contains(raw, []byte(fmt.Sprintf(`"type":%q`, wantKind))) {
				t.Error("native image tool did not reach its original provider turn")
			}
			citation := map[string]any{"type": "web_search_result_location", "cited_text": "Scripted citation text.", "title": "Fixture source", "url": "https://fixture.invalid/citation", "encrypted_index": "private-native-citation-index"}
			message := contentMessage("msg_rich_fixture")
			message["model"] = cfg.Model
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []map[string]any{
				{"type": "message_start", "message": message},
				{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "redacted_thinking", "data": "private-native-redacted-data"}},
				{"type": "content_block_stop", "index": 0},
				{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": "", "citations": []any{}}},
				{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "Original native cited answer."}},
				{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "citations_delta", "citation": citation}},
				{"type": "content_block_stop", "index": 1},
				{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 7}},
				{"type": "message_stop"},
			} {
				raw, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
			}
		default:
			t.Error("native image fixture made an additional provider request")
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Image fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	s, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
			t.Error(err)
		}
		for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, path, base64.StdEncoding.EncodeToString(encoded.Bytes()), "private-native-citation-index", "private-native-redacted-data", "Original native cited answer."} {
			if strings.Contains(logs.String(), private) {
				t.Error("native media or authority entered logs")
			}
		}
	}()
	if _, err := s.SendInput(ctx, domain.NewID(), "Inspect the private image fixture.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	var imageSeen, citationSeen, omissionSeen, redactedSeen, idle, succeeded bool
	for !idle {
		observation, err := s.Next(ctx)
		if err != nil {
			t.Fatal("native image observation failed", err)
		}
		published, _ := json.Marshal(observation)
		if bytes.Contains(published, []byte("private-native-citation-index")) || bytes.Contains(published, []byte("private-native-redacted-data")) {
			t.Fatal("opaque native reasoning or citation indices escaped")
		}
		for _, content := range observation.Content {
			if content.Kind == ContentChanged && content.DeltaKind == CitationsDelta && content.Citation != nil {
				citation := content.Citation
				citationSeen = citation.Kind == WebCitation && citation.URL != nil && *citation.URL == "https://fixture.invalid/citation" && citation.Text == "Scripted citation text."
			}
			if content.Kind == ContentCompleted && content.Block != nil {
				switch content.Block.Kind {
				case RedactedThinkingBlock:
					redactedSeen = content.Block.signature == "private-native-redacted-data" && content.Block.Thinking == nil && content.Block.Text == nil
				case TextBlock:
					omissionSeen = content.CitationCompletion == CitationsOmittedByNative && content.Block.Citations != nil && !content.Block.Citations.Null && len(content.Block.Citations.Entries) == 0 && *content.Block.Text == "Original native cited answer."
				}
			}
			blocks := content.Blocks
			if content.Kind == ToolResultObserved && content.ToolResult != nil {
				blocks = content.ToolResult.Blocks
			} else if content.Kind != NativeContextObserved {
				continue
			}
			for _, block := range blocks {
				if block.Kind != wantKind {
					continue
				}
				if imageSeen || block.Media == nil || block.Media.Source.Kind != Base64Media || block.Media.Source.Media != wantMedia || block.Media.Source.Data == nil {
					t.Fatal("native image lost original tool/source identity")
				}
				if document {
					if content.Kind != NativeContextObserved || content.ToolResult != nil || content.MessageID != "" || content.ParentToolID != "" {
						t.Fatal("native PDF context acquired fabricated tool ownership")
					}
				} else if content.ToolResult == nil || content.ToolResult.ID != "toolu_image_read" {
					t.Fatal("native image lost original tool ownership")
				}
				decoded, err := base64.StdEncoding.DecodeString(*block.Media.Source.Data)
				if err != nil || !bytes.Equal(decoded, encoded.Bytes()) {
					t.Fatal("native image content changed")
				}
				published, _ := json.Marshal(observation)
				if bytes.Contains(published, []byte(*block.Media.Source.Data)) {
					t.Fatal("private media escaped without its attachment publication boundary")
				}
				imageSeen = true
			}
		}
		if observation.Kind == InputFinished {
			succeeded = observation.Result.Successful()
		}
		idle = observation.Kind == RunStateObserved && observation.Run.State == RunIdle
	}
	if !imageSeen || !citationSeen || !omissionSeen || !redactedSeen || !succeeded || calls.Load() != 2 {
		t.Fatal("native image run lost content or exact completion")
	}
}

func nativePDFFixture() []byte {
	const content = "BT /F1 12 Tf 10 10 Td (Private native PDF fixture.) Tj ET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
	}
	var raw bytes.Buffer
	raw.WriteString("%PDF-1.4\n")
	offsets := []int{}
	for index, object := range objects {
		offsets = append(offsets, raw.Len())
		fmt.Fprintf(&raw, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := raw.Len()
	fmt.Fprintf(&raw, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&raw, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&raw, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return raw.Bytes()
}
