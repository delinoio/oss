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
	ID                 string                     `json:"id"`
	Name               checkpointToolName         `json:"name"`
	Digest             string                     `json:"digest"`
	Failed             bool                       `json:"failed,omitempty"`
	InstructionsLoaded bool                       `json:"instructions_loaded,omitempty"`
	ErrorProfile       checkpointToolErrorProfile `json:"error_profile,omitempty"`
}

// This positive observation comes from the original closed live observer. A
// legacy tool checkpoint cannot infer it from absent optional JSON fields.
// Native v1 remembered permissions are process-local. Version 2 admits only
// independently accepted one-time permissions, which retain no allowance.
// Version 3 preserves exact observed Read allowances and their applied prefix.
// Version 4 additionally retains original automatic Read policy closures.
// Version 5 adds independently answered, completed native Question history.
// Version 6 adds native inline search and independently compared Todo state.
// Version 7 adds original completed inline Write/Edit/Apply Patch results.
// Version 8 adds originally accepted, closed Question dismissals.
// Version 9 adds original Read rejection/correction and automatic closures.
// Version 10 adds completed Read's original loaded-instruction history.
// Version 11 binds other original inline tool rejections to permission names.
// Version 12 retains independently ended Read errors without rejection replay.
// Version 13 retains original external-directory allowances and policy closures.
type checkpointToolHistory struct {
	Version         uint32                          `json:"version"`
	InteractionFree bool                            `json:"interaction_free"`
	Parts           []checkpointToolPart            `json:"parts"`
	Once            []SessionClaim                  `json:"once_permissions,omitempty"`
	Always          []checkpointAlwaysPermission    `json:"always_permissions,omitempty"`
	AppliedAlways   uint32                          `json:"applied_always,omitempty"`
	Policy          []checkpointPolicyPermission    `json:"policy_permissions,omitempty"`
	Questions       []checkpointQuestionReply       `json:"question_replies,omitempty"`
	Rejections      []checkpointPermissionRejection `json:"rejected_permissions,omitempty"`
	RejectionPolicy []checkpointPolicyRejection     `json:"rejected_policy_permissions,omitempty"`
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
	searchOrTodo, fileTool, instructionsLoaded := false, false, false
	for _, history := range checkpointHistories(value) {
		if history.Todo != nil {
			if proof.Version < 6 || !nativeID(history.Todo.EventID, "evt") || !checkpointDigest(history.Todo.Digest) {
				return false
			}
			searchOrTodo = true
		}
		for _, message := range history.Messages {
			for _, part := range message.Parts {
				if part.Kind != ToolPartKind {
					continue
				}
				if index >= len(proof.Parts) {
					return false
				}
				tool := proof.Parts[index]
				if tool.ID != part.ID || tool.Digest != part.Digest || tool.Failed && (proof.Version < 9 || !checkpointRejectableTool(tool.Name) || proof.Version < 11 && tool.Name != checkpointReadTool) {
					return false
				}
				if tool.ErrorProfile != "" && (proof.Version < 12 || tool.ErrorProfile != checkpointReadError || tool.Name != checkpointReadTool || tool.Failed || tool.InstructionsLoaded || value.Stop != nil && history.RequestID == value.History.RequestID) {
					return false
				}
				if tool.InstructionsLoaded {
					if proof.Version < 10 || tool.Name != checkpointReadTool || tool.Failed {
						return false
					}
					instructionsLoaded = true
				}
				switch tool.Name {
				case checkpointReadTool, checkpointShellTool:
				case checkpointQuestionTool:
					if proof.Version < 5 {
						return false
					}
				case checkpointGlobTool, checkpointGrepTool, checkpointTodoTool:
					if proof.Version < 6 || tool.Name == checkpointTodoTool && history.Todo == nil {
						return false
					}
					searchOrTodo = true
				case checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool:
					if proof.Version < 7 {
						return false
					}
					fileTool = true
				default:
					return false
				}
				index++
			}
		}
	}
	return index == len(proof.Parts) && (proof.Version != 7 || fileTool) && (proof.Version != 6 || searchOrTodo) && (proof.Version != 8 || checkpointHasQuestionDismissal(proof)) && (proof.Version != 9 || len(proof.Rejections) != 0) && (proof.Version != 10 || instructionsLoaded) && (proof.Version != 11 || checkpointHasNamedRejection(proof)) && (proof.Version != 12 || checkpointHasReadError(proof)) && (proof.Version != 13 || checkpointHasExternalAllowance(proof))
}

func (s *sessionAPI) checkpointToolHistory(value nativeCheckpoint) *checkpointToolHistory {
	o := s.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.problem != nil {
		return nil
	}
	if (o.todo == nil) != (value.History.Todo == nil) || o.todo != nil && *o.todo != *value.History.Todo {
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
			proof.Rejections = append(proof.Rejections, s.predecessor.Tools.Rejections...)
			for _, original := range s.predecessor.Tools.RejectionPolicy {
				original.Sources = slices.Clone(original.Sources)
				proof.RejectionPolicy = append(proof.RejectionPolicy, original)
			}
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
		if interaction != nil && interaction.rejected {
			if interaction.attempt == nil {
				policy, valid := o.checkpointRejectedPolicy(interaction)
				if !valid {
					return nil
				}
				proof.RejectionPolicy = append(proof.RejectionPolicy, policy)
			} else {
				rejection, valid := o.checkpointRejectedPermission(interaction)
				if !valid {
					return nil
				}
				proof.Rejections = append(proof.Rejections, rejection)
			}
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
	rejectedParts := make(map[string]bool)
	for _, rejection := range proof.Rejections {
		rejectedParts[rejection.Claim.PartID] = true
	}
	for _, rejection := range proof.RejectionPolicy {
		rejectedParts[rejection.PartID] = true
	}
	for _, message := range value.History.Messages {
		for _, part := range message.Parts {
			if part.Kind != ToolPartKind {
				continue
			}
			observed := o.parts[part.ID]
			if observed == nil || mutationDigest(observed.raw) != part.Digest || !checkpointContextInlineTool(value, observed.value) {
				return nil
			}
			retained := checkpointToolPart{ID: part.ID, Name: checkpointToolName(observed.value.Tool.Name), Digest: part.Digest, Failed: checkpointRejectedTool(observed.value.Tool), InstructionsLoaded: checkpointLoadedInstructions(observed.value.Tool)}
			if retained.Failed && retained.Name == checkpointReadTool && !rejectedParts[part.ID] {
				if value.Stop != nil {
					return nil
				}
				retained.Failed, retained.ErrorProfile = false, checkpointReadError
			}
			proof.Parts = append(proof.Parts, retained)
		}
	}
	if len(proof.Parts) == 0 {
		return nil
	}
	value.Tools = proof
	for _, part := range proof.Parts {
		if checkpointSearchOrTodo(part.Name) {
			proof.Version = 6
		}
	}
	if latestCheckpointTodo(value) != nil {
		proof.Version = 6
	}
	for _, part := range proof.Parts {
		if checkpointFileTool(part.Name) {
			proof.Version = 7
		}
	}
	if checkpointHasQuestionDismissal(proof) {
		proof.Version = 8
	}
	if len(proof.Rejections) != 0 || len(proof.RejectionPolicy) != 0 {
		proof.Version, proof.InteractionFree = 9, false
		slices.SortFunc(proof.Rejections, func(a, b checkpointPermissionRejection) int {
			return strings.Compare(string(a.Claim.RequestID), string(b.Claim.RequestID))
		})
		slices.SortFunc(proof.RejectionPolicy, func(a, b checkpointPolicyRejection) int { return strings.Compare(a.InteractionID, b.InteractionID) })
	}
	for _, part := range proof.Parts {
		if part.InstructionsLoaded {
			proof.Version = 10
		}
	}
	if checkpointHasNamedRejection(proof) {
		proof.Version = 11
	}
	if checkpointHasReadError(proof) {
		proof.Version = 12
	}
	if checkpointHasExternalAllowance(proof) {
		proof.Version = 13
	}
	if !validCheckpointTools(value) {
		return nil
	}
	return proof
}

// Complete inline results need no restored artifact path. Completed Read's
// loaded instructions are native history, not a separately restored cache.
// Other tool states remain retained but cannot
// acquire replacement authority through the closed inline tool profiles.
func checkpointInlineTool(tool *NativeToolPart) bool {
	if tool == nil || tool.Timing == nil || tool.Timing.End == nil || tool.Timing.Compacted != nil || len(tool.Attachments) != 0 {
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
	if tool.State == ToolError {
		return checkpointDismissedQuestionTool(tool) || checkpointRejectedTool(tool)
	}
	if tool.State != ToolCompleted || tool.Output == nil {
		return false
	}
	switch checkpointToolName(tool.Name) {
	case checkpointReadTool:
		_, valid := checkpointReadMetadata(tool.Metadata)
		return valid
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
	case checkpointGlobTool, checkpointGrepTool:
		return checkpointSearchMetadata(tool.Metadata, checkpointToolName(tool.Name))
	case checkpointTodoTool:
		return checkpointInlineTodo(tool)
	case checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool:
		return checkpointFileMetadata(tool.Metadata, checkpointToolName(tool.Name))
	default:
		return false
	}
}

// Native pruning changes only the completed timestamp marker. Its separately
// retained proof must exist before the original inline tool profile is reused.
func checkpointContextInlineTool(c nativeCheckpoint, p NativePart) bool {
	if p.Tool == nil || p.Tool.Timing == nil || p.Tool.Timing.Compacted == nil {
		return checkpointInlineTool(p.Tool)
	}
	if c.Context == nil {
		return false
	}
	proven := false
	for _, proof := range c.Context.Pruned {
		if proof.ID == p.ID && proof.MessageID == p.MessageID && proof.Compacted == *p.Tool.Timing.Compacted {
			proven = true
		}
	}
	if !proven {
		return false
	}
	tool := *p.Tool
	timing := *tool.Timing
	timing.Compacted = nil
	tool.Timing = &timing
	return checkpointInlineTool(&tool)
}
