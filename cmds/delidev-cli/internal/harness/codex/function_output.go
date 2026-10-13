// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Function output notifications intentionally omit media. Use only the exact
// item from the owning thread's full current-turn history as publication
// evidence; notification bytes still have to match the native media-free
// projection before that history item is accepted.
func (c *Client) functionOutputHistoryItemLocked(ctx context.Context, turn domain.ID, itemID string, notification json.RawMessage) (json.RawMessage, error) {
	reply, err := c.wire.Call(ctx, domain.NewID(), "thread/turns/list", struct {
		Thread    domain.ID `json:"threadId"`
		Limit     int       `json:"limit"`
		Direction string    `json:"sortDirection"`
		View      string    `json:"itemsView"`
	}{c.thread, 1, "desc", "full"})
	if err != nil || reply.ErrorCode != nil {
		return nil, incompatible()
	}
	var page struct {
		Data            []json.RawMessage `json:"data"`
		NextCursor      *string           `json:"nextCursor"`
		BackwardsCursor *string           `json:"backwardsCursor"`
	}
	if domain.DecodeBounded(reply.Result, &page, c.nativeFrameLimit()) != nil || len(page.Data) != 1 {
		return nil, incompatible()
	}
	for _, cursor := range []*string{page.NextCursor, page.BackwardsCursor} {
		if cursor != nil && domain.Text(*cursor, "native turn cursor", 4096, true) != nil {
			return nil, incompatible()
		}
	}
	var wire turnWire
	if domain.DecodeBounded(page.Data[0], &wire, c.nativeFrameLimit()) != nil || wire.ID != turn || wire.ItemsView != "full" || wire.Items == nil {
		return nil, incompatible()
	}
	var authoritative json.RawMessage
	seen := make(map[string]bool, len(wire.Items))
	for _, raw := range wire.Items {
		var identity map[string]json.RawMessage
		var id, kind string
		if domain.DecodeBounded(raw, &identity, c.nativeFrameLimit()) != nil || json.Unmarshal(identity["id"], &id) != nil || json.Unmarshal(identity["type"], &kind) != nil || domain.Text(id, "native history item identity", 1024, true) != nil || seen[id] {
			return nil, incompatible()
		}
		seen[id] = true
		if id == itemID {
			if kind != "functionCallOutput" || authoritative != nil {
				return nil, incompatible()
			}
			authoritative = raw
		}
	}
	if authoritative == nil || !functionOutputNotificationMatches(notification, authoritative) {
		return nil, incompatible()
	}
	return authoritative, nil
}

func functionOutputNotificationMatches(notification, authoritative json.RawMessage) bool {
	notificationID, observed, err := decodeFunctionOutput(notification)
	if err != nil {
		return false
	}
	historyID, complete, err := decodeFunctionOutput(authoritative)
	if err != nil || notificationID != historyID || observed.Name != complete.Name || !sameFunctionNamespace(observed.Namespace, complete.Namespace) || observed.Structured != complete.Structured {
		return false
	}
	if !complete.Structured {
		return observed.Text != nil && complete.Text != nil && *observed.Text == *complete.Text
	}
	// The app-server filters image and audio entries from notifications. Every
	// remaining entry must still match the authoritative history in order.
	filtered := make([]domain.FunctionOutputContent, 0, len(complete.Content)) // Non-nil so an empty projection equals an omitted one.
	for _, content := range complete.Content {
		if content.Kind != domain.FunctionImage && content.Kind != domain.FunctionAudio {
			filtered = append(filtered, content)
		}
	}
	if !reflect.DeepEqual(append([]domain.FunctionOutputContent{}, observed.Content...), filtered) {
		return false
	}
	// The public projection redacts ciphertext to presence only, so compare the
	// private bytes of the entries the notification keeps before they are hidden.
	return slices.Equal(functionOutputCiphertexts(notification), functionOutputCiphertexts(authoritative))
}

// Return the ordered private ciphertext values of an already validated item.
// Validation owns strict field checks, so this projection ignores other fields.
func functionOutputCiphertexts(raw json.RawMessage) []string {
	var item functionOutputItem
	var parts []struct {
		Type      domain.FunctionOutputKind `json:"type"`
		Encrypted *string                   `json:"encrypted_content"`
	}
	if json.Unmarshal(raw, &item) != nil || json.Unmarshal(item.Output, &parts) != nil {
		return nil
	}
	values := []string{}
	for _, part := range parts {
		if part.Type == domain.FunctionEncrypted && part.Encrypted != nil {
			values = append(values, *part.Encrypted)
		}
	}
	return values
}

func sameFunctionNamespace(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// The enclosing native frame is already bounded by the client profile (1 MiB,
// or 16 MiB when image observations are admitted). Private media locators and
// ciphertext may use that whole frame; only the public projection is smaller.
const functionOutputRawLimit = 16 << 20

type functionOutputItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Namespace *string         `json:"namespace"`
	Output    json.RawMessage `json:"output"`
}

// Validate native bytes without reserializing protected history. Only the safe
// projection escapes this decoder; media locators/ciphertext grant no I/O.
func decodeFunctionOutput(raw json.RawMessage) (string, *domain.FunctionOutput, error) {
	var fields map[string]json.RawMessage
	if len(raw) > functionOutputRawLimit || !functionOutputUnicode(raw) || domain.DecodeBounded(raw, &fields, functionOutputRawLimit) != nil || (len(fields) != 4 && len(fields) != 5) || fields["type"] == nil || fields["id"] == nil || fields["name"] == nil || fields["output"] == nil {
		return "", nil, incompatible()
	}
	for key := range fields {
		if key != "type" && key != "id" && key != "name" && key != "namespace" && key != "output" {
			return "", nil, incompatible()
		}
	}
	var item functionOutputItem
	if domain.DecodeBounded(raw, &item, functionOutputRawLimit) != nil || item.Type != "functionCallOutput" || domain.Text(item.ID, "native function output identity", 1024, true) != nil {
		return "", nil, incompatible()
	}
	out := &domain.FunctionOutput{Name: item.Name, Namespace: item.Namespace}
	var text string
	if len(item.Output) > 0 && item.Output[0] == '"' && json.Unmarshal(item.Output, &text) == nil {
		out.Text = &text
	} else {
		var content []json.RawMessage
		if domain.DecodeBounded(item.Output, &content, functionOutputRawLimit) != nil || content == nil || len(content) > 1024 {
			return "", nil, incompatible()
		}
		out.Structured = true
		if len(content) > 0 {
			out.Content = make([]domain.FunctionOutputContent, 0, len(content))
		}
		for _, rawPart := range content {
			var part struct {
				Type      domain.FunctionOutputKind   `json:"type"`
				Text      *string                     `json:"text"`
				ImageURL  *string                     `json:"image_url"`
				FileID    *string                     `json:"file_id"`
				Detail    *domain.FunctionImageDetail `json:"detail"`
				AudioURL  *string                     `json:"audio_url"`
				Encrypted *string                     `json:"encrypted_content"`
			}
			var present map[string]json.RawMessage
			if domain.Decode(rawPart, &present) != nil || domain.Decode(rawPart, &part) != nil {
				return "", nil, incompatible()
			}
			allowed := map[string]bool{"type": true}
			safe := domain.FunctionOutputContent{Kind: part.Type}
			switch part.Type {
			case domain.FunctionText:
				allowed["text"] = true
				if part.Text == nil {
					return "", nil, incompatible()
				}
				safe.Text = part.Text
			case domain.FunctionImage:
				allowed["image_url"], allowed["file_id"], allowed["detail"] = true, true, true
				if (part.ImageURL == nil) == (part.FileID == nil) || present["image_url"] != nil && present["file_id"] != nil || present["detail"] != nil && part.Detail == nil {
					return "", nil, incompatible()
				}
				safe.Detail = part.Detail
				safe.ReferenceKind = domain.FunctionImageURL
				if part.FileID != nil {
					safe.ReferenceKind = domain.FunctionFileID
				}
			case domain.FunctionAudio:
				allowed["audio_url"] = true
				if part.AudioURL == nil {
					return "", nil, incompatible()
				}
				safe.ReferenceKind = domain.FunctionAudioURL
			case domain.FunctionEncrypted:
				allowed["encrypted_content"] = true
				if part.Encrypted == nil {
					return "", nil, incompatible()
				}
			default:
				return "", nil, incompatible()
			}
			for key := range present {
				if !allowed[key] {
					return "", nil, incompatible()
				}
			}
			for _, reference := range []*string{part.ImageURL, part.FileID, part.AudioURL, part.Encrypted} {
				if reference != nil && domain.Text(*reference, "private native function content", functionOutputRawLimit, false) != nil {
					return "", nil, incompatible()
				}
			}
			out.Content = append(out.Content, safe)
		}
	}
	if out.Validate() != nil {
		return "", nil, incompatible()
	}
	return item.ID, out, nil
}

type functionOutputIdentity struct {
	Turn      domain.ID
	Digest    [32]byte
	Completed bool
}

func (c *Client) retainFunctionOutput(raw json.RawMessage, id string, turn domain.ID, stage domain.FunctionOutputStage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return incompatible()
	}
	normal, err := json.Marshal(value)
	if err != nil {
		return incompatible()
	}
	digest := sha256.Sum256(normal)
	if c.functionOutputs == nil {
		c.functionOutputs = map[string]functionOutputIdentity{}
	}
	prior, known := c.functionOutputs[id]
	if known && (prior.Turn != turn || prior.Digest != digest || prior.Completed || stage != domain.FunctionOutputCompleted) || !known && len(c.functionOutputs) >= 10000 {
		return incompatible()
	}
	c.functionOutputs[id] = functionOutputIdentity{Turn: turn, Digest: digest, Completed: stage == domain.FunctionOutputCompleted}
	return nil
}

// Reject unpaired escapes before encoding/json can silently replace native bytes.
// Escaped literal backslashes are skipped as pairs, so literal text stays intact.
func functionOutputUnicode(raw json.RawMessage) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
