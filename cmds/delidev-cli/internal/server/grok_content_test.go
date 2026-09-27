package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func grokServerText(f *publicationFixture) domain.ExecutionEvent {
	e := f.event(domain.ExecutionGrokTextObserved, 3)
	e.GrokText = &domain.GrokTextUpdate{ID: domain.NewID(), ResponseOrdinal: 1, Text: "Original 한글 <script>inert</script>", Metadata: domain.GrokTextMetadata{EventID: string(f.thread) + "-10", ChunkID: "1", ContextTokens: "18446744073709551615", TimestampMS: "1", StreamStartMS: "0", TurnStartMS: "0"}}
	return e
}
func grokServerResponse(f *publicationFixture, sequence uint64, ordinal uint32) domain.ExecutionEvent {
	e := f.event(domain.ExecutionGrokUsageObserved, sequence)
	e.ObservationID = domain.NewID()
	e.GrokUsage = &domain.GrokResponseUsage{Ordinal: ordinal, Counts: domain.GrokResponseCounts{Input: "11", Output: "5", CachedRead: "0", CacheCreation: "0", Reasoning: "0"}}
	return e
}
func grokServerAccepted(t *testing.T) *publicationFixture {
	t.Helper()
	f := newGrokPublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	return f
}
func TestGrokServerRetainsTextResponseAndReceiptAtomically(t *testing.T) {
	f := grokServerAccepted(t)
	ctx := context.Background()
	text := grokServerText(f)
	first := text.GrokText.Metadata
	f.publish(t, text)
	text.Sequence++
	text.GrokText.Metadata.EventID = string(f.thread) + "-13"
	text.GrokText.Metadata.ChunkID = "4"
	text.GrokText.Metadata.ContextTokens = "0"
	text.GrokText.Text = " Next."
	f.publish(t, text)
	response := grokServerResponse(f, 5, 1)
	request := f.publish(t, response)
	before, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if r, err := f.call(request); err != nil || !r.Msg.Replayed {
		t.Fatal("original response receipt lost", err)
	}
	after, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("receipt replay changed state")
	}
	r, err := f.service.Store.Get(ctx, domain.MessageKind, text.GrokText.ID)
	m, decodeErr := store.Decode[domain.ExecutionMessage](r)
	if err != nil || decodeErr != nil || m.State != domain.MessageComplete || m.NativeID != first.EventID || m.GrokText == nil || len(m.GrokText.Chunks) != 2 || m.GrokText.Chunks[0] != first || m.GrokText.Chunks[1] != text.GrokText.Metadata || m.Text != "Original 한글 <script>inert</script> Next." || m.LastSequence != 5 {
		t.Fatal("original text changed", err, decodeErr, m)
	}
	u, err := f.service.Store.Get(ctx, domain.UsageKind, response.ObservationID)
	usage, decodeErr := store.Decode[domain.GrokUsageRecord](u)
	if err != nil || decodeErr != nil || usage.Usage != *response.GrokUsage || usage.AccountID != f.input.AccountID || usage.ModelID != f.input.Configuration.ModelID || usage.ThreadID != string(f.thread) || usage.TurnID != string(f.turn) {
		t.Fatal("usage provenance changed", err, decodeErr)
	}
	// Equal counts without text are an independent second original response.
	f.publish(t, grokServerResponse(f, 6, 2))
	r, _ = f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	session, _ := store.Decode[domain.Session](r)
	if session.Execution.GrokContent.Responses != 2 || session.Execution.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified {
		t.Fatal("response changed root completion")
	}
}
func TestGrokServerRejectsContentDriftWithoutPartialMutation(t *testing.T) {
	for _, mutation := range []string{"ordinal", "event", "chunk", "message", "foreign-thread", "foreign-prompt", "mixed", "reused-message", "reused-usage", "response-order"} {
		t.Run(mutation, func(t *testing.T) {
			f := grokServerAccepted(t)
			ctx := context.Background()
			e := grokServerText(f)
			f.publish(t, e)
			id := e.GrokText.ID
			e.Sequence = 4
			e.GrokText.Metadata.EventID = string(f.thread) + "-11"
			e.GrokText.Metadata.ChunkID = "2"
			switch mutation {
			case "ordinal":
				e.GrokText.ResponseOrdinal = 2
			case "event":
				e.GrokText.Metadata.EventID = string(f.thread) + "-10"
			case "chunk":
				e.GrokText.Metadata.ChunkID = "1"
			case "message":
				e.GrokText.ID = domain.NewID()
			case "foreign-thread":
				e.NativeThreadID = string(domain.NewID())
			case "foreign-prompt":
				e.NativeTurnID = "526452fa-1956-42dd-b5f4-60e2b23dfe92"
			case "mixed":
				e.Notice = domain.NativeWarning
			case "reused-message":
				f.publish(t, grokServerResponse(f, 4, 1))
				e.Sequence = 5
				e.GrokText.ResponseOrdinal = 2
			case "reused-usage":
				e = grokServerResponse(f, 4, 1)
				e.ObservationID = id
			case "response-order":
				e = grokServerResponse(f, 4, 2)
			}
			before, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			messageBefore, _ := f.service.Store.Get(ctx, domain.MessageKind, id)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid content accepted")
			}
			after, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			messageAfter, _ := f.service.Store.Get(ctx, domain.MessageKind, id)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(messageBefore, messageAfter) {
				t.Fatal("invalid content partially mutated state")
			}
		})
	}
}
