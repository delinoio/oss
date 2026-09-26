package opencode

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type checkpointToolName string

const (
	checkpointReadTool     checkpointToolName = "read"
	checkpointShellTool    checkpointToolName = "bash"
	checkpointQuestionTool checkpointToolName = "question"
)

type checkpointToolPart struct {
	ID     string             `json:"id"`
	Name   checkpointToolName `json:"name"`
	Digest string             `json:"digest"`
}

// This positive observation comes from the original closed live observer. A
// legacy tool checkpoint cannot infer it from absent optional JSON fields.
// Native v1 remembered permissions are process-local. Version 2 admits only
// independently accepted one-time permissions, which retain no allowance.
// Version 3 preserves exact observed Read allowances and their applied prefix.
// Version 4 additionally retains original automatic Read policy closures.
// Version 5 adds independently answered, completed native Question history.
type checkpointToolHistory struct {
	Version         uint32                       `json:"version"`
	InteractionFree bool                         `json:"interaction_free"`
	Parts           []checkpointToolPart         `json:"parts"`
	Once            []SessionClaim               `json:"once_permissions,omitempty"`
	Always          []checkpointAlwaysPermission `json:"always_permissions,omitempty"`
	AppliedAlways   uint32                       `json:"applied_always,omitempty"`
	Policy          []checkpointPolicyPermission `json:"policy_permissions,omitempty"`
	Questions       []checkpointQuestionReply    `json:"question_replies,omitempty"`
}

func validCheckpointTools(value nativeCheckpoint) bool {
	proof := value.Tools
	if proof == nil {
		return true
	}
	if len(proof.Parts) == 0 || len(proof.Parts) > maxObservedParts || !validCheckpointInteractions(value) {
		return false
	}
	index := 0
	for _, history := range checkpointHistories(value) {
		for _, message := range history.Messages {
			for _, part := range message.Parts {
				if part.Kind != ToolPartKind {
					continue
				}
				if index >= len(proof.Parts) {
					return false
				}
				tool := proof.Parts[index]
				if tool.ID != part.ID || tool.Digest != part.Digest || (tool.Name != checkpointReadTool && tool.Name != checkpointShellTool && (tool.Name != checkpointQuestionTool || proof.Version != 5)) {
					return false
				}
				index++
			}
		}
	}
	return index == len(proof.Parts)
}

func (s *sessionAPI) checkpointToolHistory(value nativeCheckpoint) *checkpointToolHistory {
	o := s.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.problem != nil {
		return nil
	}
	proof := &checkpointToolHistory{Version: 1, InteractionFree: true}
	if s.predecessor != nil {
		if checkpointReplacementProfile(*s.predecessor) != nil {
			return nil
		}
		if s.predecessor.Tools != nil {
			proof.Parts = append(proof.Parts, s.predecessor.Tools.Parts...)
			proof.Once = append(proof.Once, s.predecessor.Tools.Once...)
			proof.Questions = append(proof.Questions, s.predecessor.Tools.Questions...)
			for _, original := range s.predecessor.Tools.Always {
				original.Rules = slices.Clone(original.Rules)
				proof.Always = append(proof.Always, original)
			}
			for _, original := range s.predecessor.Tools.Policy {
				original.Sources = slices.Clone(original.Sources)
				proof.Policy = append(proof.Policy, original)
			}
		}
	}
	always := map[string]checkpointAlwaysPermission{}
	for id, interaction := range o.interactions {
		if interaction != nil && interaction.value.Kind == QuestionInteraction {
			reply, valid := o.checkpointQuestion(interaction)
			if !valid {
				return nil
			}
			proof.Questions = append(proof.Questions, reply)
			continue
		}
		if interaction != nil && interaction.alwaysAccepted {
			approval, valid := o.checkpointAlways(interaction)
			if !valid {
				return nil
			}
			always[id] = approval
			continue
		}
		if interaction != nil && len(interaction.alwaysObservations) != 0 {
			policy, valid := o.checkpointPolicy(interaction)
			if !valid {
				return nil
			}
			proof.Policy = append(proof.Policy, policy)
			continue
		}
		claim, valid := o.checkpointOnce(interaction)
		if !valid {
			return nil
		}
		proof.Once = append(proof.Once, claim)
	}
	if len(proof.Once) != 0 {
		proof.Version, proof.InteractionFree = 2, false
		slices.SortFunc(proof.Once, func(a, b SessionClaim) int { return strings.Compare(string(a.RequestID), string(b.RequestID)) })
	}
	for _, id := range o.alwaysOrder {
		approval, exists := always[id]
		if !exists {
			return nil
		}
		proof.Always = append(proof.Always, approval)
		delete(always, id)
	}
	if len(always) != 0 {
		return nil
	}
	if len(proof.Always) != 0 {
		applied := uint32(0)
		if s.predecessor != nil && s.predecessor.Tools != nil {
			applied = uint32(len(s.predecessor.Tools.Always))
		}
		if s.creation == nil || len(s.creation.settings.Permission) != 0 || s.restoredAlways != applied || !slices.Equal(s.sessionPermissions, checkpointAppliedPermissions(proof, applied)) {
			return nil
		}
		proof.Version, proof.InteractionFree, proof.AppliedAlways = 3, false, s.restoredAlways
	}
	if len(proof.Policy) != 0 {
		proof.Version, proof.InteractionFree = 4, false
		slices.SortFunc(proof.Policy, func(a, b checkpointPolicyPermission) int { return strings.Compare(a.InteractionID, b.InteractionID) })
	}
	if len(proof.Questions) != 0 {
		proof.Version, proof.InteractionFree = 5, false
		slices.SortFunc(proof.Questions, func(a, b checkpointQuestionReply) int {
			return strings.Compare(string(a.Claim.RequestID), string(b.Claim.RequestID))
		})
	}
	for _, message := range value.History.Messages {
		for _, part := range message.Parts {
			if part.Kind != ToolPartKind {
				continue
			}
			observed := o.parts[part.ID]
			if observed == nil || mutationDigest(observed.raw) != part.Digest || !checkpointInlineTool(observed.value.Tool) {
				return nil
			}
			proof.Parts = append(proof.Parts, checkpointToolPart{ID: part.ID, Name: checkpointToolName(observed.value.Tool.Name), Digest: part.Digest})
		}
	}
	if len(proof.Parts) == 0 {
		return nil
	}
	value.Tools = proof
	if !validCheckpointTools(value) {
		return nil
	}
	return proof
}

// Complete inline results need no restored artifact path or dynamic native
// instruction-loader state. Other tool states remain retained but cannot
// acquire replacement authority through this initial Read/Shell profile.
func checkpointInlineTool(tool *NativeToolPart) bool {
	if tool == nil || tool.State != ToolCompleted || tool.Timing == nil || tool.Timing.End == nil || tool.Timing.Compacted != nil || tool.Output == nil || len(tool.Attachments) != 0 {
		return false
	}
	if tool.PartMetadata != nil {
		var metadata struct {
			ProviderExecuted *bool `json:"providerExecuted"`
		}
		if domain.Decode(tool.PartMetadata, &metadata) != nil || metadata.ProviderExecuted != nil && *metadata.ProviderExecuted {
			return false
		}
	}
	switch checkpointToolName(tool.Name) {
	case checkpointReadTool:
		var metadata domain.OpenCodeReadMetadata
		return domain.Decode(tool.Metadata, &metadata) == nil && metadata.Preview != nil && metadata.Truncated != nil && !*metadata.Truncated && metadata.Loaded != nil && len(metadata.Loaded) == 0 && (metadata.Interrupted == nil || !*metadata.Interrupted)
	case checkpointShellTool:
		var metadata struct {
			Output      *string         `json:"output"`
			Exit        json.RawMessage `json:"exit"`
			Truncated   *bool           `json:"truncated"`
			Interrupted *bool           `json:"interrupted"`
		}
		var exit *int64
		return domain.Decode(tool.Metadata, &metadata) == nil && metadata.Output != nil && len(metadata.Exit) != 0 && json.Unmarshal(metadata.Exit, &exit) == nil && exit != nil && *exit >= -9007199254740991 && *exit <= 9007199254740991 && metadata.Truncated != nil && !*metadata.Truncated && (metadata.Interrupted == nil || !*metadata.Interrupted)
	case checkpointQuestionTool:
		_, valid := checkpointQuestionAnswers(tool.Metadata)
		return valid
	default:
		return false
	}
}
