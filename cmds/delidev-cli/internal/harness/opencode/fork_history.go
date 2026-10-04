// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ForkPartIdentity struct {
	Source string `json:"source"`
	Child  string `json:"child"`
}
type ForkMessageIdentity struct {
	Source string             `json:"source"`
	Child  string             `json:"child"`
	Parts  []ForkPartIdentity `json:"parts"`
}

// The inherited map is comparison-only. Original paths/counters remain native
// provenance; neither ID cloning nor this private descriptor grants execution.
type nativeCheckpointFork struct {
	Version          uint32                `json:"version"`
	RequestID        domain.ID             `json:"request_id"`
	SourceReference  CheckpointReference   `json:"source_reference"`
	SourceWorkspace  string                `json:"source_workspace"`
	SourceHistories  []HistoryObservation  `json:"source_histories"`
	ClonedHistories  []HistoryObservation  `json:"cloned_histories"`
	Identities       []ForkMessageIdentity `json:"identities"`
	SelectionPending bool                  `json:"selection_pending"`
}

func forkSourceProfile(c nativeCheckpoint) error {
	if c.Version != 1 || c.Fork != nil || !checkpointGlobalRoot(c) || c.Project != "global" || c.Reference.RequiresResume || c.Stop != nil || c.Context != nil || c.Tools != nil || c.Snapshot != nil || c.ProjectAdoption != nil || len(c.References) != 0 || !validCheckpointLineage(c) {
		return incompatible()
	}
	for _, h := range checkpointHistories(c) {
		if h.Todo != nil {
			return incompatible()
		}
		for _, m := range h.Messages {
			for _, p := range m.Parts {
				switch p.Kind {
				case TextPartKind, StepStartPartKind, StepFinishPartKind:
				default:
					return incompatible()
				}
			}
		}
	}
	return nil
}

// Compare every native byte through the closed typed decoders. Only child-local
// identity fields and the assistant's mapped parent may differ. Text, timing,
// usage/model/settings and historical paths are all independently preserved.
func compareForkHistory(source, child []json.RawMessage, sourceID, childID string) ([]ForkMessageIdentity, []HistoryMessage, error) {
	if !nativeID(sourceID, "ses") || !nativeID(childID, "ses") || sourceID == childID || len(source) < 2 || len(source) != len(child) || len(source) > maxObservedMessages {
		return nil, nil, sessionUncertain()
	}
	all := map[string]bool{sourceID: true, childID: true}
	for _, raw := range source {
		fields, err := shape(raw, []string{"info", "parts"}, nil)
		if err != nil {
			return nil, nil, err
		}
		info, err := decodeNativeMessage(fields["info"])
		if err != nil || info.SessionID != sourceID || all[info.ID] {
			return nil, nil, sessionUncertain()
		}
		all[info.ID] = true
		var parts []json.RawMessage
		if domain.Decode(fields["parts"], &parts) != nil || parts == nil {
			return nil, nil, sessionUncertain()
		}
		for _, raw := range parts {
			p, err := decodeNativePart(raw)
			if err != nil || p.SessionID != sourceID || p.MessageID != info.ID || all[p.ID] {
				return nil, nil, sessionUncertain()
			}
			all[p.ID] = true
		}
	}
	mapping := map[string]string{}
	identities := make([]ForkMessageIdentity, 0, len(source))
	history := make([]HistoryMessage, 0, len(source))
	count := 0
	for index, raw := range source {
		original, _ := shape(raw, []string{"info", "parts"}, nil)
		next, err := shape(child[index], []string{"info", "parts"}, nil)
		if err != nil {
			return nil, nil, err
		}
		a, err := decodeNativeMessage(original["info"])
		if err != nil {
			return nil, nil, err
		}
		b, err := decodeNativeMessage(next["info"])
		if err != nil || b.SessionID != childID || all[b.ID] || a.Role != b.Role {
			return nil, nil, sessionUncertain()
		}
		all[b.ID] = true
		old, err := object(original["info"])
		if err != nil {
			return nil, nil, err
		}
		expected, err := object(next["info"])
		if err != nil {
			return nil, nil, err
		}
		old["id"], _ = json.Marshal(b.ID)
		old["sessionID"], _ = json.Marshal(childID)
		if a.User != nil {
			if a.User.Agent != string(BuildAgent) || a.User.System != nil || a.User.Tools != nil || a.User.Format != nil || a.User.Variant != nil {
				return nil, nil, incompatible()
			}
		} else {
			if a.Assistant == nil || a.Assistant.Completed == nil || a.Assistant.Error != nil || a.Assistant.Finish == nil || *a.Assistant.Finish != FinishStop || a.Assistant.Summary != nil || a.Assistant.Structured != nil || a.Assistant.Variant != nil || a.Assistant.Agent != string(BuildAgent) || a.Assistant.Mode != string(BuildAgent) || mapping[a.Assistant.ParentID] == "" {
				return nil, nil, incompatible()
			}
			old["parentID"], _ = json.Marshal(mapping[a.Assistant.ParentID])
		}
		left, _ := json.Marshal(old)
		right, _ := json.Marshal(expected)
		if !bytes.Equal(canonicalNative(left), canonicalNative(right)) {
			return nil, nil, sessionUncertain()
		}
		mapping[a.ID] = b.ID
		var ap, bp []json.RawMessage
		if domain.Decode(original["parts"], &ap) != nil || domain.Decode(next["parts"], &bp) != nil || ap == nil || bp == nil || len(ap) != len(bp) {
			return nil, nil, sessionUncertain()
		}
		identity := ForkMessageIdentity{Source: a.ID, Child: b.ID, Parts: []ForkPartIdentity{}}
		message := HistoryMessage{ID: b.ID, Role: b.Role, Digest: mutationDigest(canonicalNative(next["info"])), Parts: []HistoryPart{}}
		for i, rawPart := range ap {
			a, err := decodeNativePart(rawPart)
			if err != nil {
				return nil, nil, err
			}
			b, err := decodeNativePart(bp[i])
			if err != nil || b.MessageID != message.ID || b.SessionID != childID || all[b.ID] || a.Kind != b.Kind {
				return nil, nil, sessionUncertain()
			}
			all[b.ID] = true
			count++
			if count > maxObservedParts {
				return nil, nil, eventBound()
			}
			switch a.Kind {
			case TextPartKind:
				if a.Text == nil || a.Text.Synthetic != nil || a.Text.Ignored != nil || a.Text.Metadata != nil {
					return nil, nil, incompatible()
				}
			case StepStartPartKind:
				if a.Step == nil || a.Step.Snapshot != nil {
					return nil, nil, incompatible()
				}
			case StepFinishPartKind:
				if a.Step == nil || a.Step.Snapshot != nil {
					return nil, nil, incompatible()
				}
			default:
				return nil, nil, incompatible()
			}
			old, _ := object(rawPart)
			next, _ := object(bp[i])
			old["id"], _ = json.Marshal(b.ID)
			old["messageID"], _ = json.Marshal(message.ID)
			old["sessionID"], _ = json.Marshal(childID)
			left, _ := json.Marshal(old)
			right, _ := json.Marshal(next)
			if !bytes.Equal(canonicalNative(left), canonicalNative(right)) {
				return nil, nil, sessionUncertain()
			}
			identity.Parts = append(identity.Parts, ForkPartIdentity{Source: a.ID, Child: b.ID})
			message.Parts = append(message.Parts, HistoryPart{ID: b.ID, Kind: b.Kind, Digest: mutationDigest(canonicalNative(bp[i]))})
		}
		identities = append(identities, identity)
		history = append(history, message)
	}
	return identities, history, nil
}

func cloneForkHistories(source nativeCheckpoint, identities []ForkMessageIdentity, messages []HistoryMessage, child string) ([]HistoryObservation, error) {
	original := checkpointHistories(source)
	histories := make([]HistoryObservation, 0, len(original))
	position := 0
	for _, h := range original {
		copy := copyHistoryObservation(h)
		copy.SessionID = child
		for index, m := range h.Messages {
			if position >= len(identities) || identities[position].Source != m.ID || messages[position].ID != identities[position].Child {
				return nil, sessionUncertain()
			}
			copy.Messages[index] = messages[position]
			if m.ID == h.InputID {
				copy.InputID = messages[position].ID
			}
			if m.ID == h.AssistantID {
				copy.AssistantID = messages[position].ID
			}
			position++
		}
		copy.Digest = ""
		raw, _ := json.Marshal(copy)
		copy.Digest = mutationDigest(raw)
		if !validCheckpointHistory(copy) {
			return nil, sessionUncertain()
		}
		histories = append(histories, copy)
	}
	if position != len(messages) {
		return nil, sessionUncertain()
	}
	return histories, nil
}

func cloneForkProof(p *nativeCheckpointFork) *nativeCheckpointFork {
	if p == nil {
		return nil
	}
	copy := *p
	copy.SourceHistories = slices.Clone(p.SourceHistories)
	copy.ClonedHistories = slices.Clone(p.ClonedHistories)
	copy.Identities = slices.Clone(p.Identities)
	for i := range copy.SourceHistories {
		copy.SourceHistories[i] = copyHistoryObservation(copy.SourceHistories[i])
	}
	for i := range copy.ClonedHistories {
		copy.ClonedHistories[i] = copyHistoryObservation(copy.ClonedHistories[i])
	}
	for i := range copy.Identities {
		copy.Identities[i].Parts = slices.Clone(copy.Identities[i].Parts)
	}
	return &copy
}

func validCheckpointFork(c nativeCheckpoint) bool {
	p := c.Fork
	if p == nil {
		return true
	}
	source := p.SourceReference
	if p.Version != 1 || c.Version != 1 || c.Project != "global" || !checkpointGlobalRoot(c) || p.RequestID.Validate() != nil || p.RequestID != c.Reference.CreationRequestID || !checkpointDigest(source.SHA256) || source.OwnerID.Validate() != nil || source.CreationRequestID.Validate() != nil || source.InputRequestID.Validate() != nil || !nativeID(source.SessionID, "ses") || !nativeID(source.InputID, "msg") || !nativeID(source.PartID, "prt") || !checkpointDigest(source.InputSHA256) || !checkpointDigest(source.HistorySHA256) || source.RequiresResume || source.SessionID == c.Reference.SessionID || !checkpointPath(p.SourceWorkspace) || p.SourceWorkspace == c.Workspace || len(p.SourceHistories) == 0 || len(p.SourceHistories) != len(p.ClonedHistories) || p.Identities == nil || len(p.Identities) < 2 || len(p.Identities) > maxObservedMessages || len(p.SourceHistories) > maxObservedMessages/2 {
		return false
	}
	histories := checkpointHistories(c)
	if len(histories) < len(p.ClonedHistories) || p.SelectionPending && len(histories) != len(p.ClonedHistories) {
		return false
	}
	all := map[string]bool{source.SessionID: true, c.Reference.SessionID: true}
	position := 0
	parts := 0
	for i, original := range p.SourceHistories {
		cloned := p.ClonedHistories[i]
		left, _ := json.Marshal(cloned)
		right, _ := json.Marshal(histories[i])
		if !validCheckpointHistory(original) || !validCheckpointHistory(cloned) || original.SessionID != source.SessionID || cloned.SessionID != c.Reference.SessionID || original.RequestID != cloned.RequestID || len(original.Messages) != len(cloned.Messages) || !bytes.Equal(left, right) {
			return false
		}
		for j, m := range original.Messages {
			if position >= len(p.Identities) {
				return false
			}
			ids := p.Identities[position]
			next := cloned.Messages[j]
			if ids.Source != m.ID || ids.Child != next.ID || all[ids.Source] || all[ids.Child] || m.Role != next.Role || len(m.Parts) != len(next.Parts) || len(m.Parts) != len(ids.Parts) {
				return false
			}
			all[ids.Source], all[ids.Child] = true, true
			if m.ID == original.InputID && next.ID != cloned.InputID || m.ID == original.AssistantID && next.ID != cloned.AssistantID {
				return false
			}
			for k, a := range m.Parts {
				b := next.Parts[k]
				pair := ids.Parts[k]
				if a.ID != pair.Source || b.ID != pair.Child || all[pair.Source] || all[pair.Child] || a.Kind != b.Kind {
					return false
				}
				all[pair.Source], all[pair.Child] = true, true
				parts++
				switch a.Kind {
				case TextPartKind, StepStartPartKind, StepFinishPartKind:
				default:
					return false
				}
			}
			position++
		}
	}
	last := p.SourceHistories[len(p.SourceHistories)-1]
	return position == len(p.Identities) && parts <= maxObservedParts && last.RequestID == source.InputRequestID && last.InputID == source.InputID && last.Digest == source.HistorySHA256 && len(last.Messages) > 0 && len(last.Messages[0].Parts) > 0 && last.Messages[0].Parts[0].ID == source.PartID && (!p.SelectionPending || c.Reference.InputRequestID == source.InputRequestID && c.Reference.InputSHA256 == source.InputSHA256)
}
