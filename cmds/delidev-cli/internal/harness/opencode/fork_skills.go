// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

// A relocation retains comparison evidence for one authenticated child part.
// Original native command events, arguments and source history remain intact.
type forkSkillRelocation struct {
	RequestID       domain.ID `json:"request_id"`
	MessageID       string    `json:"message_id"`
	PartID          string    `json:"part_id"`
	Name            string    `json:"name"`
	ContentRevision string    `json:"content_revision"`
	SourceLocation  string    `json:"source_location"`
	TargetLocation  string    `json:"target_location"`
	OriginalSHA256  string    `json:"original_sha256"`
	BeforeSHA256    string    `json:"before_sha256"`
	AfterSHA256     string    `json:"after_sha256"`
	BodySHA256      string    `json:"body_sha256"`
	TextSHA256      string    `json:"text_sha256,omitempty"`
}

type forkSkillPatch struct {
	proof forkSkillRelocation
	body  []byte
}

func forkHasSkillHistory(c nativeCheckpoint) bool {
	for _, h := range checkpointHistories(c) {
		if h.SkillCommand != nil {
			return true
		}
		for _, m := range h.Messages {
			for _, part := range m.Parts {
				if part.Kind == ToolPartKind {
					return true
				}
			}
		}
	}
	return false
}

func forkSkillToolProfile(p *checkpointToolHistory) bool {
	if p == nil {
		return true
	}
	if p.Version != 14 || !p.InteractionFree || len(p.Once)+len(p.Always)+len(p.Policy)+len(p.Questions)+len(p.Rejections)+len(p.RejectionPolicy) != 0 || p.AppliedAlways != 0 {
		return false
	}
	for _, part := range p.Parts {
		if part.Name != checkpointSkillTool || part.Failed || part.InstructionsLoaded || part.ErrorProfile != "" {
			return false
		}
	}
	return true
}

// Authenticate the whole original package and native output before constructing
// any PATCH. String replacement is confined to the generated path fields.
func prepareForkSkillPatches(ctx context.Context, source nativeCheckpoint, original, child []json.RawMessage, target []skills.OpenCodeSelection) ([]forkSkillPatch, error) {
	if len(original) != len(child) || len(original) > maxObservedMessages {
		return nil, sessionUncertain()
	}
	selected := map[string]skills.OpenCodeSelection{}
	for _, proof := range source.Skills {
		pkg, err := skills.OpenCodePackage(ctx, skills.NativeSelection{Name: proof.Name, Path: proof.Location, ContentRevision: proof.ContentRevision})
		if err != nil || pkg.ExplicitOnly != proof.ExplicitOnly {
			return nil, sessionUncertain()
		}
		selected[pkg.Name] = pkg
	}
	targets := map[string]skills.OpenCodeSelection{}
	for _, pkg := range target {
		actual, err := skills.OpenCodePackage(ctx, pkg.NativeSelection)
		if err != nil || actual.Content != pkg.Content || actual.Description != pkg.Description || actual.ExplicitOnly != pkg.ExplicitOnly {
			return nil, sessionUncertain()
		}
		targets[pkg.Name] = pkg
	}
	if len(selected) != len(targets) {
		return nil, sessionUncertain()
	}
	for name, pkg := range selected {
		next, ok := targets[name]
		if !ok || next.ContentRevision != pkg.ContentRevision || next.ExplicitOnly != pkg.ExplicitOnly || next.Content != pkg.Content || next.Description != pkg.Description {
			return nil, sessionUncertain()
		}
	}
	commands := map[string]*SkillCommandProof{}
	for _, history := range checkpointHistories(source) {
		if history.SkillCommand != nil {
			commands[history.SkillCommand.PartID] = history.SkillCommand
		}
	}
	patches := []forkSkillPatch{}
	for index, raw := range original {
		fields, err := object(raw)
		if err != nil {
			return nil, err
		}
		var before, after []json.RawMessage
		next, err := object(child[index])
		if err != nil || domain.Decode(fields["parts"], &before) != nil || domain.Decode(next["parts"], &after) != nil || len(before) != len(after) {
			return nil, sessionUncertain()
		}
		for n, rawPart := range before {
			part, err := decodeNativePart(rawPart)
			if err != nil {
				return nil, err
			}
			mapped, err := decodeNativePart(after[n])
			if err != nil {
				return nil, err
			}
			var pkg skills.OpenCodeSelection
			var output string
			var originalLocation string
			command := commands[part.ID]
			if command != nil {
				var ok bool
				pkg, ok = selected[command.Name]
				if !ok || !checkpointPath(command.Location) || filepath.Base(command.Location) != "SKILL.md" || filepath.Base(filepath.Dir(command.Location)) != pkg.Name || filepath.Base(filepath.Dir(filepath.Dir(command.Location))) != "skills" || filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(command.Location)))) != "opencode" || command.ContentRevision != pkg.ContentRevision || command.PartSHA256 != mutationDigest(canonicalNative(rawPart)) || part.Text == nil || command.TextSHA256 != mutationDigest([]byte(part.Text.Text)) {
					return nil, sessionUncertain()
				}
				originalLocation = command.Location
				marker := "\n\nBase directory for this skill: " + filepath.Dir(originalLocation) + "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."
				if strings.Count(part.Text.Text, marker) != 1 {
					return nil, incompatible()
				}
				replacement := "\n\nBase directory for this skill: " + filepath.Dir(targets[pkg.Name].Path) + "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."
				output = strings.Replace(part.Text.Text, marker, replacement, 1)
			} else if part.Kind == ToolPartKind {
				tool := part.Tool
				if !checkpointInlineSkill(tool) || tool.PartMetadata != nil || tool.Timing == nil || tool.Timing.Compacted != nil || len(tool.Attachments) != 0 {
					return nil, incompatible()
				}
				input, err := shape(tool.Input, []string{"name"}, nil)
				if err != nil {
					return nil, err
				}
				name, _ := boundedString(input["name"], 128, true)
				var ok bool
				pkg, ok = selected[name]
				if !ok || pkg.ExplicitOnly {
					return nil, incompatible()
				}
				metadata, err := shape(tool.Metadata, []string{"name", "dir"}, nil)
				directory, valid := boundedString(metadata["dir"], 32768, true)
				if err != nil || !valid || !scalar(metadata["name"], name) {
					return nil, sessionUncertain()
				}
				observer := inputObserver{skillPackages: []skills.OpenCodeSelection{pkg}}
				if observer.verifySkillToolAtDirectory(tool, directory) != nil {
					return nil, sessionUncertain()
				}
				originalLocation = filepath.Join(directory, "SKILL.md")
				prefix := "<skill_content name=\"" + name + "\">\n# Skill: " + name + "\n\n" + strings.TrimSpace(pkg.Content) + "\n\nBase directory for this skill: "
				// Prefix length proves the generated base path position even when package
				// content contains other absolute paths or matching prose.
				suffix := "\nRelative paths in this skill (e.g., scripts/, reference/) are relative to this base directory.\nNote: file list is sampled.\n\n<skill_files>\n"
				tail := strings.TrimPrefix(*tool.Output, prefix+directory+suffix)
				closing := "\n</skill_files>\n</skill_content>"
				list := strings.TrimSuffix(tail, closing)
				entries := []string{}
				if list != "" {
					for _, entry := range strings.Split(list, "\n") {
						path := strings.TrimSuffix(strings.TrimPrefix(entry, "<file>"), "</file>")
						rel, err := filepath.Rel(directory, path)
						if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
							return nil, sessionUncertain()
						}
						entries = append(entries, "<file>"+filepath.Join(filepath.Dir(targets[name].Path), rel)+"</file>")
					}
				}
				output = prefix + filepath.Dir(targets[name].Path) + suffix + strings.Join(entries, "\n") + closing
			} else {
				continue
			}
			fields, err := object(after[n])
			if err != nil {
				return nil, err
			}
			proof := forkSkillRelocation{RequestID: domain.NewID(), MessageID: mapped.MessageID, PartID: mapped.ID, Name: pkg.Name, ContentRevision: pkg.ContentRevision, SourceLocation: originalLocation, TargetLocation: targets[pkg.Name].Path, OriginalSHA256: mutationDigest(canonicalNative(rawPart)), BeforeSHA256: mutationDigest(canonicalNative(after[n]))}
			if command != nil {
				fields["text"], _ = json.Marshal(output)
				proof.TextSHA256 = mutationDigest([]byte(output))
			} else {
				state, _ := object(fields["state"])
				state["output"], _ = json.Marshal(output)
				state["metadata"], _ = json.Marshal(struct {
					Name string `json:"name"`
					Dir  string `json:"dir"`
				}{pkg.Name, filepath.Dir(proof.TargetLocation)})
				fields["state"], _ = json.Marshal(state)
			}
			body, err := json.Marshal(fields)
			if err != nil || len(body) > maxHTTPBody {
				return nil, eventBound()
			}
			proof.AfterSHA256, proof.BodySHA256 = mutationDigest(canonicalNative(body)), mutationDigest(body)
			patches = append(patches, forkSkillPatch{proof, body})
		}
	}
	return patches, nil
}

func (s *sessionAPI) relocateForkSkills(ctx context.Context, source nativeCheckpoint, original, child []json.RawMessage, messages []HistoryMessage) ([]forkSkillRelocation, []json.RawMessage, error) {
	patches, err := prepareForkSkillPatches(ctx, source, original, child, s.apiProfile.Skills)
	if err != nil {
		return nil, nil, err
	}
	expected := slices.Clone(child)
	proofs := []forkSkillRelocation{}
	for _, patch := range patches {
		p := patch.proof
		claim := SessionClaim{RequestID: p.RequestID, Kind: RelocateForkSkillMutation, SessionID: s.forkAttempt.child, MessageID: p.MessageID, PartID: p.PartID, InputRequestID: source.Reference.InputRequestID, BodyDigest: p.BodySHA256}
		if claim.Validate() != nil || s.claim(ctx, claim) != nil {
			return nil, nil, sessionUncertain()
		}
		path := "/session/" + claim.SessionID + "/message/" + p.MessageID + "/part/" + p.PartID
		s.forkAttempt.mutation = &nativeForkMutation{http.MethodPatch, path, p.BodySHA256}
		_, _, sendErr := s.request(ctx, http.MethodPatch, path, patch.body, http.StatusOK)
		s.forkAttempt.mutation = nil
		_ = sendErr // The complete exact child read below reconciles a lost response.
		for index, message := range messages {
			if message.ID != p.MessageID {
				continue
			}
			fields, _ := object(expected[index])
			var parts []json.RawMessage
			if domain.Decode(fields["parts"], &parts) != nil {
				return nil, nil, sessionUncertain()
			}
			found := false
			for i, part := range message.Parts {
				if part.ID == p.PartID {
					parts[i] = bytes.Clone(patch.body)
					found = true
				}
			}
			if !found {
				return nil, nil, sessionUncertain()
			}
			fields["parts"], _ = json.Marshal(parts)
			expected[index], _ = json.Marshal(fields)
		}
		actual, err := s.forkHistory(ctx, s.forkAttempt.child, nil)
		if err != nil || !sameForkRawHistory(actual, expected) {
			return nil, nil, sessionUncertain()
		}
		if actual, err := s.forkHistory(ctx, source.Reference.SessionID, checkpointInventory(source)); err != nil || !sameForkRawHistory(actual, original) {
			return nil, nil, sessionUncertain()
		}
		proofs = append(proofs, p)
		if s.logger != nil {
			s.logger.InfoContext(ctx, "opencode_fork_skill_relocation_verified", "owner_id", s.owner, "request_id", p.RequestID, "message_id", p.MessageID, "part_id", p.PartID, "skill_name", p.Name, "content_revision", p.ContentRevision)
		}
	}
	return proofs, expected, nil
}

func applyForkSkillProofs(histories []HistoryObservation, proofs []forkSkillRelocation) ([]HistoryObservation, error) {
	byPart := map[string]forkSkillRelocation{}
	for _, proof := range proofs {
		if _, exists := byPart[proof.PartID]; exists {
			return nil, sessionUncertain()
		}
		byPart[proof.PartID] = proof
	}
	for index := range histories {
		h := &histories[index]
		for m := range h.Messages {
			for n := range h.Messages[m].Parts {
				part := &h.Messages[m].Parts[n]
				proof, exists := byPart[part.ID]
				if !exists {
					continue
				}
				if h.Messages[m].ID != proof.MessageID || part.Digest != proof.BeforeSHA256 {
					return nil, sessionUncertain()
				}
				part.Digest = proof.AfterSHA256
				if h.SkillCommand != nil && h.SkillCommand.PartID == part.ID {
					h.SkillCommand.Location, h.SkillCommand.PartSHA256, h.SkillCommand.TextSHA256 = proof.TargetLocation, proof.AfterSHA256, proof.TextSHA256
				}
				delete(byPart, part.ID)
			}
		}
		h.Digest = ""
		raw, _ := json.Marshal(h)
		h.Digest = mutationDigest(raw)
		if !validCheckpointHistory(*h) {
			return nil, sessionUncertain()
		}
	}
	if len(byPart) != 0 {
		return nil, sessionUncertain()
	}
	return histories, nil
}

func forkClonedTools(histories []HistoryObservation) *checkpointToolHistory {
	parts := []checkpointToolPart{}
	for _, history := range histories {
		for _, message := range history.Messages {
			for _, part := range message.Parts {
				if part.Kind == ToolPartKind {
					parts = append(parts, checkpointToolPart{ID: part.ID, Name: checkpointSkillTool, Digest: part.Digest})
				}
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return &checkpointToolHistory{Version: 14, InteractionFree: true, Parts: parts}
}

func validForkSkillRelocations(c nativeCheckpoint) bool {
	p := c.Fork
	type retainedPart struct {
		message string
		part    HistoryPart
		command *SkillCommandProof
	}
	index := func(histories []HistoryObservation) map[string]retainedPart {
		out := map[string]retainedPart{}
		for _, h := range histories {
			for _, m := range h.Messages {
				for _, part := range m.Parts {
					out[part.ID] = retainedPart{m.ID, part, h.SkillCommand}
				}
			}
		}
		return out
	}
	cloned, original := index(p.ClonedHistories), index(p.SourceHistories)
	sourceIDs := map[string]string{}
	for _, message := range p.Identities {
		for _, part := range message.Parts {
			sourceIDs[part.Child] = part.Source
		}
	}
	selected := map[string]checkpointSkill{}
	for _, pkg := range c.Skills {
		selected[pkg.Name] = pkg
	}
	seen := map[string]bool{}
	requests := map[domain.ID]bool{p.RequestID: true, p.SourceReference.InputRequestID: true, c.Reference.OwnerID: true}
	for _, proof := range p.SkillRelocations {
		if proof.RequestID.Validate() != nil || requests[proof.RequestID] || seen[proof.PartID] || !nativeID(proof.MessageID, "msg") || !nativeID(proof.PartID, "prt") || !checkpointDigest(proof.OriginalSHA256) || !checkpointDigest(proof.BeforeSHA256) || !checkpointDigest(proof.AfterSHA256) || !checkpointDigest(proof.BodySHA256) || proof.TextSHA256 != "" && !checkpointDigest(proof.TextSHA256) || !checkpointPath(proof.SourceLocation) {
			return false
		}
		pkg, ok := selected[proof.Name]
		if !ok || pkg.ContentRevision != proof.ContentRevision || pkg.Location != proof.TargetLocation || proof.SourceLocation == proof.TargetLocation {
			return false
		}
		next, ok := cloned[proof.PartID]
		if !ok || next.message != proof.MessageID || next.part.Digest != proof.AfterSHA256 || next.part.Kind != TextPartKind && next.part.Kind != ToolPartKind || (next.part.Kind == TextPartKind) != (proof.TextSHA256 != "") {
			return false
		}
		old, ok := original[sourceIDs[proof.PartID]]
		if !ok || old.part.Digest != proof.OriginalSHA256 || old.part.Kind != next.part.Kind {
			return false
		}
		if next.part.Kind == TextPartKind {
			a, b := old.command, next.command
			if a == nil || b == nil || a.PartID != old.part.ID || b.PartID != next.part.ID || a.Location != proof.SourceLocation || b.Location != proof.TargetLocation || a.Name != proof.Name || b.Name != proof.Name || a.ContentRevision != proof.ContentRevision || b.ContentRevision != proof.ContentRevision || b.TextSHA256 != proof.TextSHA256 || a.EventID != b.EventID || a.ArgumentsSHA256 != b.ArgumentsSHA256 {
				return false
			}
		}
		seen[proof.PartID], requests[proof.RequestID] = true, true
	}
	// Every inherited command/tool needs a relocation; absence grants no legacy
	// path alias or access to a parent's package after independent deletion.
	for _, h := range p.ClonedHistories {
		if h.SkillCommand != nil && !seen[h.SkillCommand.PartID] {
			return false
		}
		for _, m := range h.Messages {
			for _, part := range m.Parts {
				if part.Kind == ToolPartKind && !seen[part.ID] {
					return false
				}
			}
		}
	}
	if len(p.SkillRelocations) == 0 {
		return len(p.OriginalClonedHistories) == 0
	}
	if len(p.OriginalClonedHistories) != len(p.ClonedHistories) {
		return false
	}
	for _, history := range p.OriginalClonedHistories {
		if !validCheckpointHistory(history) {
			return false
		}
	}
	expected, err := applyForkSkillProofs(copyForkHistories(p.OriginalClonedHistories), p.SkillRelocations)
	if err != nil {
		return false
	}
	left, _ := json.Marshal(expected)
	right, _ := json.Marshal(p.ClonedHistories)
	return bytes.Equal(left, right)
}

func copyForkHistories(histories []HistoryObservation) []HistoryObservation {
	if histories == nil {
		return nil
	}
	out := make([]HistoryObservation, len(histories))
	for i, h := range histories {
		out[i] = copyHistoryObservation(h)
	}
	return out
}
func forkLastInputDigest(p *nativeCheckpointFork) string {
	last := p.ClonedHistories[len(p.ClonedHistories)-1]
	if last.SkillCommand != nil {
		return last.SkillCommand.TextSHA256
	}
	return p.SourceReference.InputSHA256
}
