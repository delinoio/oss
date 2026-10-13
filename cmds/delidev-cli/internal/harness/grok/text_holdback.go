package grok

import (
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// textHoldback delays whole original text chunk observations that overlap a
// possible protected-value prefix. It never rewrites, splits or merges a chunk:
// each released observation keeps its exact text, order, chunk/event indexes and
// native identity, so the retained history proof and chunk digests are unchanged.
// A chunk is released only once later text proves that no protected form can
// begin inside it, or when its response message ended cleanly.
type textHoldback struct {
	guard   *security.ProtectedStream
	queue   []heldObservation
	publish func(InputObservation) error
	refused func(chunk uint64)
}

type heldObservation struct {
	observation InputObservation
	bytes       int
}

func newTextHoldback(guard security.ProtectedJSON, publish func(InputObservation) error, refused func(chunk uint64)) *textHoldback {
	return &textHoldback{guard: guard.NewStream(security.StreamPlainText), publish: publish, refused: refused}
}

// text records one validated original chunk. It returns the protected-content
// refusal before any chunk that could complete a protected form is published.
func (h *textHoldback) text(chunk TextChunk) error {
	hold, err := h.guard.Observe(chunk.Update.Content.Text)
	if err != nil {
		h.discard()
		if errors.Is(err, security.ErrProtectedStreamValue) {
			h.refused(chunk.Meta.Chunk)
			return protectedTextRefused()
		}
		return err
	}
	copied := chunk
	h.queue = append(h.queue, heldObservation{observation: InputObservation{Kind: InputText, Chunk: &copied}, bytes: len(chunk.Update.Content.Text)})
	total := 0
	for _, item := range h.queue {
		total += item.bytes
	}
	return h.release(max(total-hold, 0))
}

// ordered publishes a non-text observation after every earlier text chunk.
// While an earlier chunk is withheld it waits behind it, preserving order.
func (h *textHoldback) ordered(observation InputObservation) error {
	if len(h.queue) == 0 {
		return h.publish(observation)
	}
	h.queue = append(h.queue, heldObservation{observation: observation})
	return nil
}

// messageEnd releases a cleanly ended response message and starts the next
// message with fresh matching state. Messages are independent streams.
func (h *textHoldback) messageEnd() error {
	err := h.release(-1)
	h.guard.Discard()
	return err
}

// release publishes leading observations whose text ends within allowed bytes;
// a negative bound releases everything.
func (h *textHoldback) release(allowed int) error {
	end, count := 0, 0
	for count < len(h.queue) {
		end += h.queue[count].bytes
		if allowed >= 0 && end > allowed {
			break
		}
		count++
	}
	released := h.queue[:count]
	h.queue = append([]heldObservation(nil), h.queue[count:]...)
	for _, item := range released {
		if err := h.publish(item.observation); err != nil {
			h.discard()
			return err
		}
	}
	return nil
}

// discard drops every withheld observation without publishing it. It runs on
// refusal, failure, cancellation and every other non-clean terminal path.
func (h *textHoldback) discard() {
	h.queue = nil
	h.guard.Discard()
}

func protectedTextRefused() *domain.Error {
	return domain.Fail(domain.Unsupported, "Grok Build text chunks reconstruct a protected value.", "Retain the original native session and inspect its output before resuming; no withheld text was published.")
}
