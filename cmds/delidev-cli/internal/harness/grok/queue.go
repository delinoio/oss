package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type queueKind string

const promptQueueKind queueKind = "prompt"

type queueEntry struct {
	ID       string    `json:"id"`
	Version  uint64    `json:"version"`
	Kind     queueKind `json:"kind"`
	Text     string    `json:"text"`
	Position uint64    `json:"position"`
}

type queueUpdate struct {
	Session domain.ID       `json:"sessionId"`
	Entries []queueEntry    `json:"entries"`
	Running json.RawMessage `json:"runningPromptId,omitempty"`
	Text    json.RawMessage `json:"runningText,omitempty"`
	Kind    json.RawMessage `json:"runningKind,omitempty"`
}

// inputQueue binds one original plain-text prompt. Empty queue observations do
// not prove native completion or cancellation, and repeated or foreign running
// input cannot replace the original identity. Callers retain this state with
// their durable native-input claim before publishing acceptance.
type inputQueue struct {
	session domain.ID
	text    string
	prompt  string
	queued  bool
	running bool
	cleared bool
}

func (q *inputQueue) observe(raw []byte) error {
	var value queueUpdate
	if q.session.Validate() != nil || domain.Text(q.text, "original native input", 256<<10, true) != nil || decode(raw, &value) != nil || value.Session != q.session || len(value.Entries) > 1 {
		return incompatible()
	}
	runningFields := len(value.Running) != 0 || len(value.Text) != 0 || len(value.Kind) != 0
	if len(value.Entries) == 1 {
		entry := value.Entries[0]
		if q.queued || q.running || q.cleared || runningFields || !nativeUUID(entry.ID, 4) || entry.Version != 0 || entry.Position != 0 || entry.Kind != promptQueueKind || entry.Text != q.text {
			return incompatible()
		}
		q.prompt, q.queued = entry.ID, true
		return nil
	}
	if runningFields {
		var prompt, text string
		var kind queueKind
		if decode(value.Running, &prompt) != nil || decode(value.Text, &text) != nil || decode(value.Kind, &kind) != nil || !q.queued || q.running || q.cleared || prompt != q.prompt || text != q.text || kind != promptQueueKind {
			return incompatible()
		}
		q.running = true
		return nil
	}
	if !q.running || q.cleared {
		return incompatible()
	}
	q.cleared = true
	return nil
}
