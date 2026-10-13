package grok

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const holdbackSecret = "fixture-chunk-secret/+é"

func holdbackForms() []string {
	forms := []string{}
	for _, form := range []string{"literal", "base64", "raw-base64", "url-base64", "raw-url-base64"} {
		forms = append(forms, reflectedFixtureValue(holdbackSecret, form).(string))
	}
	return forms
}

func holdbackChunk(index int, text string) TextChunk {
	var chunk TextChunk
	chunk.Update.Kind, chunk.Update.Content.Type, chunk.Update.Content.Text = "agent_message_chunk", "text", text
	chunk.Meta.Chunk, chunk.Meta.Event, chunk.Meta.Type = uint64(index+1), "event-"+string(rune('a'+index)), "AgentMessageChunk"
	return chunk
}

func newTestHoldback(published *[]InputObservation) *textHoldback {
	return newTextHoldback(security.NewProtectedJSON([]string{holdbackSecret}), func(o InputObservation) error {
		*published = append(*published, o)
		return nil
	}, func(uint64) {})
}

func publishedText(published []InputObservation) string {
	var text strings.Builder
	for _, o := range published {
		if o.Kind == InputText {
			text.WriteString(o.Chunk.Update.Content.Text)
		}
	}
	return text.String()
}

func TestTextHoldbackNeverPublishesChunksThatReconstructProtectedForms(t *testing.T) {
	for _, form := range holdbackForms() {
		body := "ok " + form + " end"
		for first := 1; first < len(body); first++ {
			for second := first; second < len(body); second++ {
				if !utf8.RuneStart(body[first]) || !utf8.RuneStart(body[second]) {
					continue
				}
				var published []InputObservation
				holdback := newTestHoldback(&published)
				refused := false
				for index, piece := range []string{body[:first], body[first:second], body[second:]} {
					if piece == "" {
						continue
					}
					if err := holdback.text(holdbackChunk(index, piece)); err != nil {
						if domain.SafeError(err).Code != domain.Unsupported || len(holdback.queue) != 0 {
							t.Fatal("refusal kept withheld chunks or used a different code", err)
						}
						refused = true
						break
					}
				}
				if !refused {
					t.Fatalf("split %d/%d of %q was not refused", first, second, form)
				}
				if strings.Contains(publishedText(published), form) || !strings.HasPrefix(body, publishedText(published)) {
					t.Fatalf("published chunks reconstruct or reorder the form: %q", publishedText(published))
				}
			}
		}
	}
}

func TestTextHoldbackKeepsOriginalChunksOrderedAndReleasesCleanEnd(t *testing.T) {
	var published []InputObservation
	holdback := newTestHoldback(&published)
	texts := []string{"Hello ", "fixture-", "world ", "fixture-chunk-sec"}
	originals := make([]TextChunk, len(texts))
	for index, text := range texts {
		originals[index] = holdbackChunk(index, text)
	}
	for _, original := range originals {
		if err := holdback.text(original); err != nil {
			t.Fatal(err)
		}
	}
	if len(published) >= len(originals) || len(holdback.queue) == 0 {
		t.Fatal("expected the chunk ending in a protected prefix to stay private")
	}
	for index, o := range published {
		if o.Kind != InputText || *o.Chunk != originals[index] {
			t.Fatal("a released chunk changed its text, order or native metadata")
		}
	}
	if err := holdback.messageEnd(); err != nil {
		t.Fatal(err)
	}
	if len(published) != len(originals) || len(holdback.queue) != 0 {
		t.Fatal("clean message end did not release the remaining chunks")
	}
	for index, o := range published {
		if *o.Chunk != originals[index] {
			t.Fatal("clean release changed a chunk")
		}
	}
}

func TestTextHoldbackKeepsNonTextOrderAndDiscardsWithoutPublishing(t *testing.T) {
	var published []InputObservation
	holdback := newTestHoldback(&published)
	if err := holdback.text(holdbackChunk(0, "Start fixture-")); err != nil {
		t.Fatal(err)
	}
	title := "private title"
	if err := holdback.ordered(InputObservation{Kind: InputTitle, Title: title}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 || len(holdback.queue) != 2 {
		t.Fatal("a title overtook withheld text")
	}
	holdback.discard()
	if len(published) != 0 || len(holdback.queue) != 0 {
		t.Fatal("discard published or retained a holdback")
	}
	// The guard restarts: a fresh message cannot continue the discarded stream.
	if err := holdback.text(holdbackChunk(1, "chunk-secret/+é")); err != nil {
		t.Fatal("discarded holdback leaked matching state", err)
	}
	if err := holdback.ordered(InputObservation{Kind: InputTitle, Title: title}); err != nil || len(published) != 2 || len(holdback.queue) != 0 {
		t.Fatal("ordered publication changed once the queue drained", err)
	}
}

func TestTextHoldbackPublishFailureDropsRemainingChunks(t *testing.T) {
	failure := errors.New("publication failed")
	holdback := newTextHoldback(security.NewProtectedJSON([]string{holdbackSecret}), func(InputObservation) error { return failure }, func(uint64) {})
	if err := holdback.text(holdbackChunk(0, "safe text")); !errors.Is(err, failure) || len(holdback.queue) != 0 {
		t.Fatal("failed publication retained withheld chunks", err)
	}
}

func TestTextHoldbackWithoutProtectedValuesPublishesImmediately(t *testing.T) {
	var published []InputObservation
	holdback := newTextHoldback(security.NewProtectedJSON(nil), func(o InputObservation) error {
		published = append(published, o)
		return nil
	}, func(uint64) {})
	if err := holdback.text(holdbackChunk(0, "fixture-chunk-")); err != nil || len(published) != 1 || len(holdback.queue) != 0 {
		t.Fatal("unconfigured protection delayed a chunk", err)
	}
}

func runHoldbackFixture(t *testing.T, mode string) ([]InputObservation, error) {
	t.Helper()
	config, logs := fixtureAPIConfig(t, mode)
	config.Model = turnFixtureModel
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	api, err := openAPI(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err = api.Create(ctx, domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var observations []InputObservation
	_, err = api.RunText(ctx, domain.NewID(), "Ordinary input.", func(context.Context, InputClaim) error { return nil }, func(_ context.Context, o InputObservation) error {
		observations = append(observations, o)
		return nil
	})
	if strings.Contains(logs.String(), config.Token) {
		t.Fatal("protected data entered diagnostics")
	}
	return observations, err
}

func TestNativeTextChunksSplitAcrossBoundariesAreRefusedBeforePublication(t *testing.T) {
	for _, form := range []string{"literal", "base64", "raw-base64", "url-base64", "raw-url-base64"} {
		full := "before " + reflectedFixtureValue(apiFixtureToken, form).(string) + " after"
		for _, split := range []int{len("before ") + 1, len("before ") + len(full[len("before "):])/2, len(full) - len(" after") - 1} {
			t.Run(form+"-"+full[:split], func(t *testing.T) {
				mode := "input-split-" + string(rune('0'+split/10)) + string(rune('0'+split%10)) + "-" + form
				observations, err := runHoldbackFixture(t, mode)
				if err == nil {
					t.Fatal("reconstructing chunks were accepted")
				}
				var published strings.Builder
				for _, o := range observations {
					if o.Kind == InputText {
						published.WriteString(o.Chunk.Update.Content.Text)
					}
					if o.Kind == InputCompleted {
						t.Fatal("refused input completed")
					}
				}
				if strings.Contains(published.String(), reflectedFixtureValue(apiFixtureToken, form).(string)) {
					t.Fatal("published chunks reconstruct the protected execution token")
				}
			})
		}
	}
}

func TestNativeTextChunksEndingInProtectedPrefixKeepExactOriginalsOnCleanCompletion(t *testing.T) {
	for _, mode := range []string{"input-hold-continue", "input-hold-end"} {
		t.Run(mode, func(t *testing.T) {
			observations, err := runHoldbackFixture(t, mode)
			if err != nil {
				t.Fatal(err)
			}
			texts := fixtureSplitTexts(mode, apiFixtureToken)
			var chunks []TextChunk
			textIndex, responseIndex, completedIndex := -1, -1, -1
			for index, o := range observations {
				switch o.Kind {
				case InputText:
					chunks = append(chunks, *o.Chunk)
					textIndex = index
				case InputResponse:
					responseIndex = index
				case InputCompleted:
					completedIndex = index
				}
			}
			if len(chunks) != len(texts) || textIndex > responseIndex || responseIndex > completedIndex {
				t.Fatal("withheld chunk was lost or reordered after the response", len(chunks))
			}
			for index, chunk := range chunks {
				if chunk.Update.Content.Text != texts[index] || chunk.Meta.Chunk != uint64(index+1) || chunk.Meta.Prompt == "" {
					t.Fatal("released chunk changed text or chunk/event metadata")
				}
			}
		})
	}
}
