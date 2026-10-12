// SPDX-License-Identifier: Apache-2.0
package opencode

// SkillCommandProof retains only original native invocation identities and
// immutable comparison evidence. Prompt, arguments and package content stay in
// their separately owned private native/accepted-input stores.
type SkillCommandProof struct {
	Name            string `json:"name"`
	Location        string `json:"location"`
	ContentRevision string `json:"content_revision"`
	ArgumentsSHA256 string `json:"arguments_sha256"`
	TextSHA256      string `json:"text_sha256"`
	PartSHA256      string `json:"part_sha256"`
	EventID         string `json:"event_id"`
	InputID         string `json:"input_id"`
	PartID          string `json:"part_id"`
	AssistantID     string `json:"assistant_id"`
}

func validSkillCommandHistory(history HistoryObservation) bool {
	proof := history.SkillCommand
	if proof == nil {
		return true
	}
	if proof.Name == "" || !checkpointPath(proof.Location) || !checkpointDigest(proof.ContentRevision) || !checkpointDigest(proof.ArgumentsSHA256) || !checkpointDigest(proof.TextSHA256) || !checkpointDigest(proof.PartSHA256) || !nativeID(proof.EventID, "evt") || proof.InputID != history.InputID || proof.AssistantID != history.AssistantID || !nativeID(proof.PartID, "prt") {
		return false
	}
	for _, message := range history.Messages {
		if message.ID != proof.InputID {
			continue
		}
		return message.Role == UserMessageRole && len(message.Parts) == 1 && message.Parts[0].ID == proof.PartID && message.Parts[0].Kind == TextPartKind && message.Parts[0].Digest == proof.PartSHA256
	}
	return false
}
