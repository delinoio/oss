// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxEarlyChildBytes = 2 << 20

type foregroundChild struct {
	value     domain.SubagentObservation
	task      domain.OpenCodeTaskInput
	published bool
}

// Children are original private facts, never a constructor for native control.
// The Worker joins the exact already published root task before publication.
func copyChildObservations(values []domain.SubagentObservation) []domain.SubagentObservation {
	if len(values) == 0 {
		return nil
	}
	raw, _ := json.Marshal(values)
	defer clear(raw)
	var copied []domain.SubagentObservation
	_ = json.Unmarshal(raw, &copied)
	return copied
}

func eventSession(event NativeEvent) string {
	fields, err := object(event.Properties)
	if err != nil {
		return ""
	}
	id, _ := boundedString(fields["sessionID"], 30, false)
	if id == "" && (event.Kind == SessionCreatedEvent || event.Kind == SessionUpdatedEvent) {
		info, _ := object(fields["info"])
		id, _ = boundedString(info["id"], 30, false)
	}
	return id
}

// The original reader remains serialized. Early child events remain bounded
// private arrivals until the root task and independent child read both agree.
func (s *sessionAPI) observeOwnedEvent(ctx context.Context, o *inputObserver, event NativeEvent) (result inputObservation, problem error) {
	if err := s.enter(ctx); err != nil {
		return inputObservation{}, err
	}
	defer s.leave()
	defer func() {
		if problem != nil {
			o.mu.Lock()
			problem = o.fail(ctx, "foreground-child", problem)
			o.mu.Unlock()
		}
	}()
	if s.children == nil {
		s.children = map[string]*foregroundChild{}
		s.earlyChildren = map[string][]NativeEvent{}
	}
	id := eventSession(event)
	if id != "" && id != o.input.receipt.SessionID {
		if err := validateChildArrival(event, id); err != nil {
			if s.logger != nil {
				s.logger.WarnContext(ctx, "opencode_child_arrival_rejected", "owner_id", s.owner, "event_kind", event.Kind, "code", domain.SafeError(err).Code)
			}
			return inputObservation{}, err
		}
		child := s.children[id]
		if child == nil {
			if !nativeID(id, "ses") || len(s.earlyChildren) >= 128 || len(event.Properties) > maxEarlyChildBytes-s.earlyChildBytes || len(s.earlyChildren[id]) >= 128 {
				return inputObservation{}, eventBound()
			}
			s.earlyChildBytes += len(event.Properties)
			s.earlyChildren[id] = append(s.earlyChildren[id], event)
			result := inputObservation{EventID: event.ID, Kind: event.Kind, ChildPending: true}
			result.frozen = freezeObservation(event, result)
			return result, nil
		}
		if event.Kind == PermissionAskedEvent || event.Kind == QuestionAskedEvent {
			return inputObservation{}, observerProblem()
		}
		if event.Kind == MessagePartUpdatedEvent {
			fields, _ := object(event.Properties)
			part, err := decodeNativePart(fields["part"])
			if err != nil || part.Tool != nil && part.Tool.Name == "task" || part.Kind == CompactionPartKind || part.Kind == SubtaskPartKind {
				return inputObservation{}, observerProblem()
			}
		}
		// This projection is independently read history. An arrival triggers
		// the read; it is never mislabeled as direct content authority.
		values, err := s.observeForegroundChild(ctx, child, event.ID, domain.OpenCodeChildHistorySource)
		if err != nil {
			return inputObservation{}, err
		}
		result := inputObservation{EventID: event.ID, Kind: event.Kind, Children: values, ChildPending: len(values) == 0}
		result.frozen = freezeObservation(event, result)
		return result, nil
	}
	result, err := o.observe(ctx, event)
	if err != nil {
		return result, err
	}
	if result.Part != nil && result.Part.Tool != nil && result.Part.Tool.Name == "task" && result.Part.Tool.State != ToolPending {
		part, tool := result.Part, result.Part.Tool
		task, err := domain.DecodeOpenCodeForegroundTask(tool.Input)
		if err != nil {
			if s.logger != nil {
				s.logger.WarnContext(ctx, "opencode_parent_task_rejected", "owner_id", s.owner, "phase", "input", "code", domain.SafeError(err).Code)
			}
			return result, observerProblem()
		}
		if len(tool.Metadata) > 0 && string(tool.Metadata) != "{}" {
			metadata, err := domain.DecodeOpenCodeTaskMetadata(tool.Metadata)
			owner := o.messages[part.MessageID]
			if err != nil || metadata.Parent != s.creation.identity.id || metadata.Model.Provider != s.creation.settings.Provider || metadata.Model.Model != s.creation.settings.Model || owner == nil || owner.value.Assistant == nil || owner.value.Assistant.ParentID != s.input.receipt.MessageID {
				fields, _ := object(tool.Metadata)
				if s.logger != nil {
					s.logger.WarnContext(ctx, "opencode_parent_task_shape", "owner_id", s.owner, "has_job_id", len(fields["jobId"]) != 0, "has_summary", len(fields["summary"]) != 0, "background_requested", metadata.Background != nil && *metadata.Background, "field_count", len(fields))
					s.logger.WarnContext(ctx, "opencode_parent_task_rejected", "owner_id", s.owner, "phase", "metadata", "shape_valid", err == nil, "parent_valid", metadata.Parent == s.creation.identity.id, "provider_valid", metadata.Model.Provider == s.creation.settings.Provider, "model_valid", metadata.Model.Model == s.creation.settings.Model, "message_valid", owner != nil && owner.value.Assistant != nil && owner.value.Assistant.ParentID == s.input.receipt.MessageID, "code", domain.Unsupported)
				}
				return result, observerProblem()
			}
			child := s.children[metadata.Child]
			if child == nil {
				if len(s.children) >= 128 {
					return result, eventBound()
				}
				for _, existing := range s.children {
					if existing.value.ParentToolID == part.ID || existing.value.OpenCodeTool.CallID == tool.CallID {
						return result, observerProblem()
					}
				}
				model := metadata.Model.Model
				child = &foregroundChild{task: task, value: domain.SubagentObservation{ID: domain.NewID(), NativeID: metadata.Child, ParentID: metadata.Parent, ParentToolID: part.ID, RequestedModel: &model, Status: domain.SubagentPending, OpenCodeTool: &domain.OpenCodeParentTool{MessageID: part.MessageID, PartID: part.ID, CallID: tool.CallID}}}
				s.children[metadata.Child] = child
			} else if child.value.ParentToolID != part.ID || child.value.OpenCodeTool.CallID != tool.CallID || child.value.OpenCodeTool.MessageID != part.MessageID || child.task.Prompt != task.Prompt || child.task.AgentType != task.AgentType {
				return result, observerProblem()
			}
			source := domain.OpenCodeTaskSource
			if child.published {
				source = domain.OpenCodeChildHistorySource
			}
			values, err := s.observeForegroundChild(ctx, child, event.ID, source)
			if err != nil {
				return result, err
			}
			result.Children = values
		}
	}
	result.frozen = freezeObservation(event, result)
	return result, nil
}

// Gate held. Exact private history-read paths grant bounded GET authority only.
func (s *sessionAPI) readChild(ctx context.Context, path string) ([]byte, error) {
	if s.historyRead != nil {
		return nil, sessionConflict()
	}
	s.historyRead = &historyPageRead{path: path}
	defer func() { s.historyRead = nil }()
	raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
	if err == nil && s.historyRead.cursor != "" {
		return nil, eventBound()
	}
	return raw, err
}

func (s *sessionAPI) observeForegroundChild(ctx context.Context, child *foregroundChild, sourceID string, source domain.SubagentSource) (values []domain.SubagentObservation, problem error) {
	phase := "session"
	defer func() {
		if problem != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "opencode_child_projection_rejected", "owner_id", s.owner, "phase", phase, "code", domain.SafeError(problem).Code)
		}
	}()
	id := child.value.NativeID
	raw, err := s.readChild(ctx, "/session/"+id)
	if err != nil {
		return nil, err
	}
	fields, err := shape(raw, []string{"id", "parentID", "slug", "projectID", "directory", "cost", "tokens", "title", "version", "time", "permission"}, []string{"path", "agent", "model", "metadata", "summary"})
	if err != nil || !scalar(fields["id"], id) || !scalar(fields["parentID"], s.creation.identity.id) || !scalar(fields["directory"], s.cwd) || !scalar(fields["projectID"], s.creation.identity.project) || !scalar(fields["version"], SupportedVersion) || !validateCounters(fields["tokens"]) || !nonnegativeDecimal(fields["cost"]) {
		return nil, observerProblem()
	}
	phase = "creation-time"
	times, err := shape(fields["time"], []string{"created", "updated"}, []string{"archived", "compacting"})
	if err != nil {
		return nil, observerProblem()
	}
	created, ok := nativeCount(times["created"])
	updated, good := nativeCount(times["updated"])
	rootInput := s.observer.messages[s.input.receipt.MessageID]
	if !ok || !good || rootInput == nil || created < rootInput.value.Created || updated < created {
		return nil, observerProblem()
	}
	phase = "permissions"
	var permissions []PermissionRule
	if domain.Decode(fields["permission"], &permissions) != nil || permissions == nil || len(permissions) > 512 {
		return nil, observerProblem()
	}
	for _, rule := range permissions {
		if !validChildPermissionRule(rule) {
			return nil, observerProblem()
		}
	}
	for _, rule := range s.sessionPermissions {
		if (rule.Action == PermissionDeny || rule.Permission == "external_directory") && !slices.Contains(permissions, rule) {
			return nil, observerProblem()
		}
	}
	if len(fields["model"]) == 0 || len(fields["agent"]) == 0 {
		return nil, nil
	}
	phase = "model"
	model, err := shape(fields["model"], []string{"id", "providerID"}, []string{"variant"})
	if err != nil {
		phase = "model-shape"
		return nil, observerProblem()
	}
	if !scalar(model["id"], s.creation.settings.Model) {
		phase = "model-id"
		return nil, observerProblem()
	}
	if !scalar(model["providerID"], s.creation.settings.Provider) {
		phase = "provider-id"
		return nil, observerProblem()
	}
	// setAgentModel persists the same literal absent-variant equivalent as
	// the verified root profile; an alternate variant remains unsupported.
	if len(model["variant"]) != 0 && !scalar(model["variant"], "default") {
		phase = "model-variant"
		return nil, observerProblem()
	}
	if !scalar(fields["agent"], child.task.AgentType) {
		phase = "agent"
		return nil, observerProblem()
	}
	value := child.value
	value.Source, value.SourceID = source, sourceID
	value.ObservedModel, value.Output, value.Usage = nil, nil, nil
	if source != domain.OpenCodeTaskSource {
		phase = "history"
		raw, err = s.readChild(ctx, "/session/"+id+"/message?limit=1000")
		if err != nil {
			return nil, err
		}
		var rows []json.RawMessage
		if domain.Decode(raw, &rows) != nil || rows == nil || len(rows) > 1000 {
			return nil, observerProblem()
		}
		// Pinned MessageV2.page reverses its descending database slice before
		// the HTTP handler returns page.items. These rows are already chronological;
		// reversing them again would put the assistant before its original user.
		// Refuse a cursor rather than treating a bounded suffix as complete history.
		seen := map[string]bool{}
		seenParts := map[string]bool{}
		userID := ""
		completed := false
		var observedModel string
		for _, row := range rows {
			f, err := shape(row, []string{"info", "parts"}, nil)
			if err != nil {
				return nil, observerProblem()
			}
			message, err := decodeNativeMessage(f["info"])
			if err != nil || message.SessionID != id || seen[message.ID] {
				return nil, observerProblem()
			}
			seen[message.ID] = true
			var parts []json.RawMessage
			if domain.Decode(f["parts"], &parts) != nil || parts == nil || len(parts) > maxObservedParts {
				return nil, observerProblem()
			}
			if message.User != nil {
				phase = "child-input"
				if userID != "" || message.User.Provider != s.creation.settings.Provider || message.User.Model != s.creation.settings.Model || message.User.Agent != child.task.AgentType || message.User.Variant != nil || message.User.System != nil || message.User.Tools != nil || message.User.Format != nil || len(parts) != 1 {
					return nil, observerProblem()
				}
				part, err := decodeNativePart(parts[0])
				if err != nil || part.SessionID != id || part.MessageID != message.ID || part.Text == nil || part.Kind != TextPartKind || part.Text.Synthetic != nil || part.Text.Ignored != nil || sha256.Sum256([]byte(part.Text.Text)) != sha256.Sum256([]byte(child.task.Prompt)) {
					return nil, observerProblem()
				}
				seenParts[part.ID] = true
				userID = message.ID
			} else {
				phase = "child-assistant"
				a := message.Assistant
				if a == nil || userID == "" || a.ParentID != userID {
					phase = "child-parent"
					return nil, observerProblem()
				}
				if a.Provider != s.creation.settings.Provider || a.Model != s.creation.settings.Model || a.Variant != nil {
					phase = "child-message-model"
					return nil, observerProblem()
				}
				if a.Cwd != s.cwd || a.Root != s.observer.root {
					phase = "child-message-path"
					return nil, observerProblem()
				}
				if a.Agent != child.task.AgentType || a.Mode != child.task.AgentType || a.Summary != nil || a.Structured != nil {
					phase = "child-message-agent"
					return nil, observerProblem()
				}
				observedModel = a.Model
				completed = a.Completed != nil && a.Finish != nil && (*a.Finish == FinishStop || *a.Finish == FinishLength)
				value.Status = domain.SubagentRunning
				if a.Error != nil {
					value.Status = domain.SubagentFailed
					if a.Error.Kind == AbortedErrorKind && s.observer.stop != nil {
						value.Status = domain.SubagentInterrupted
					}
				} else if completed {
					value.Status = domain.SubagentCompleted
				}
				for _, rawPart := range parts {
					phase = "child-part"
					part, err := decodeNativePart(rawPart)
					if err != nil || part.SessionID != id || part.MessageID != message.ID || seenParts[part.ID] {
						return nil, observerProblem()
					}
					seenParts[part.ID] = true
					switch part.Kind {
					case TextPartKind, ReasoningPartKind:
						if part.Text == nil || part.Text.Timing == nil || part.Text.Synthetic != nil || part.Text.Ignored != nil {
							return nil, observerProblem()
						}
						if part.Kind == TextPartKind {
							value.Output = &domain.SubagentOutput{NativeMessageID: message.ID, Text: part.Text.Text, Partial: true}
						}
					case ToolPartKind:
						if part.Tool == nil || !supportedChildTool(part.Tool.Name) || len(part.Tool.Attachments) > 0 || completed && part.Tool.State != ToolCompleted && part.Tool.State != ToolError {
							return nil, observerProblem()
						}
					case StepStartPartKind, StepFinishPartKind, SnapshotPartKind, PatchPartKind:
					default:
						return nil, observerProblem()
					}
				}
				count := func(n uint64) *string { value := strconv.FormatUint(n, 10); return &value }
				var total *string
				if a.Usage.Total != nil {
					total = count(*a.Usage.Total)
				}
				value.Usage = &domain.SubagentUsage{Scope: domain.SubagentResponseUsage, Input: count(a.Usage.Input), Output: count(a.Usage.Output), Total: total, NativeReport: domain.OpenCodeChildUsageReport(a.Usage.Input, a.Usage.Output, a.Usage.Reasoning, a.Usage.CacheRead, a.Usage.CacheWrite, a.Usage.Total)}
			}
		}
		if observedModel != "" {
			value.ObservedModel = &observedModel
		}
		phase = "child-status"
		statuses, err := s.readChild(ctx, "/session/status")
		if err != nil {
			return nil, err
		}
		var states map[string]json.RawMessage
		if domain.Decode(statuses, &states) != nil || states == nil {
			return nil, observerProblem()
		}
		if state, exists := states[id]; exists {
			status, err := childStatus(state)
			if err != nil {
				return nil, err
			}
			if status != NativeStatusIdle && value.Status.Terminal() {
				value.Status = domain.SubagentRunning
			}
		}
		if !completed && value.Status == domain.SubagentCompleted {
			return nil, observerProblem()
		}
	}
	if !child.published {
		initial := value
		initial.Source = domain.OpenCodeTaskSource
		initial.Output, initial.ObservedModel, initial.Usage = nil, nil, nil
		initial.Status = domain.SubagentPending
		child.published = true
		child.value = value
		for _, event := range s.earlyChildren[id] {
			if err := validateChildArrival(event, id); err != nil {
				return nil, err
			}
			s.earlyChildBytes -= len(event.Properties)
		}
		delete(s.earlyChildren, id)
		if source != domain.OpenCodeTaskSource {
			return []domain.SubagentObservation{initial, value}, nil
		}
	}
	child.value = value
	return []domain.SubagentObservation{value}, nil
}

func supportedChildTool(name string) bool {
	return name == "read" || name == "bash" || name == "todowrite" || name != "task" && domain.OpenCodeBuiltinName(name).Valid()
}

func childStatus(raw json.RawMessage) (NativeSessionStatus, error) {
	fields, err := object(raw)
	if err != nil {
		return "", observerProblem()
	}
	if scalar(fields["type"], "retry") {
		if _, err := decodeNativeRetry(raw); err != nil {
			return "", err
		}
		return NativeStatusRetry, nil
	}
	if _, err := shape(raw, []string{"type"}, nil); err != nil {
		return "", observerProblem()
	}
	if scalar(fields["type"], "busy") {
		return NativeStatusBusy, nil
	}
	if scalar(fields["type"], "idle") {
		return NativeStatusIdle, nil
	}
	return "", observerProblem()
}

func validateChildArrival(event NativeEvent, id string) error {
	if !nativeID(event.ID, "evt") || !nativeID(id, "ses") || len(event.Properties) > maxHTTPBody {
		return observerProblem()
	}
	fields, err := object(event.Properties)
	if err != nil {
		return err
	}
	switch event.Kind {
	case SessionCreatedEvent, SessionUpdatedEvent:
		info, err := object(fields["info"])
		if err != nil || !scalar(info["id"], id) {
			return observerProblem()
		}
	case MessageUpdatedEvent:
		message, err := decodeNativeMessage(fields["info"])
		if err != nil || message.SessionID != id {
			return observerProblem()
		}
	case MessagePartUpdatedEvent:
		part, err := decodeNativePart(fields["part"])
		if err != nil || part.SessionID != id || part.Kind == CompactionPartKind || part.Kind == SubtaskPartKind || part.Tool != nil && !supportedChildTool(part.Tool.Name) {
			return observerProblem()
		}
	case MessagePartDeltaEvent:
		message, _ := boundedString(fields["messageID"], 30, true)
		part, _ := boundedString(fields["partID"], 30, true)
		if !nativeID(message, "msg") || !nativeID(part, "prt") || !scalar(fields["field"], "text") {
			return observerProblem()
		}
	case SessionStatusEvent:
		_, err = childStatus(fields["status"])
		return err
	case SessionDiffEvent:
		if !validateDiffs(fields["diff"]) {
			return observerProblem()
		}
	case SessionErrorEvent:
		_, err := decodeNativeError(fields["error"])
		return err
	case TodoUpdatedEvent:
		var values []domain.OpenCodeTodo
		if domain.Decode(fields["todos"], &values) != nil || domain.ValidateOpenCodeTodos(values) != nil {
			return observerProblem()
		}
	case SessionIdleEvent:
	default:
		// Unsupported child questions/permissions remain inert and cannot
		// borrow root approval or response authority, even before ownership.
		return observerProblem()
	}
	return nil
}

// Verify original child inventory independently. A root task's completion or
// absent active status cannot establish child settlement or process cleanup.
func (s *sessionAPI) verifyForegroundChildren(ctx context.Context) error {
	return s.readForegroundChildInventory(ctx, false)
}

func (s *sessionAPI) readForegroundChildInventory(ctx context.Context, stopped bool) error {
	s.childInventoryVerified = false
	s.childInventoryFacts = nil
	raw, err := s.readChild(ctx, "/session/"+s.creation.identity.id+"/children")
	if err != nil {
		return err
	}
	var rows []json.RawMessage
	if domain.Decode(raw, &rows) != nil || rows == nil || len(rows) > 128 {
		return observerProblem()
	}
	seen := map[string]bool{}
	var facts []domain.SubagentObservation
	for _, row := range rows {
		fields, err := object(row)
		id, _ := boundedString(fields["id"], 30, true)
		child := s.children[id]
		if err != nil || child == nil || seen[id] || !scalar(fields["parentID"], s.creation.identity.id) {
			return observerProblem()
		}
		seen[id] = true
		values, err := s.observeForegroundChild(ctx, child, "inventory:"+id, domain.OpenCodeChildHistorySource)
		if err != nil {
			return err
		}
		if len(values) == 0 || !child.value.Status.Terminal() && !stopped {
			return sessionUncertain()
		}
		facts = append(facts, values...)
	}
	if len(seen) != len(s.children) || len(s.earlyChildren) > 0 {
		return sessionUncertain()
	}
	s.childInventoryVerified = true
	s.childInventoryFacts = copyChildObservations(facts)
	return nil
}

// Pinned 1.18.32 can cancel a foreground child without writing its assistant
// terminal record. Preserve that unfinished native history and report the
// original scope's joined cleanup separately, never a native success/error.
// Remove this projection when the pinned native profile durably settles every
// aborted child; independent process cleanup remains required in either case.
func (s *sessionAPI) closeStoppedForegroundChildren(stop StopReceipt, history HistoryObservation) error {
	if !s.childInventoryVerified || !stop.CleanupVerified || !stop.InterruptedObserved {
		for _, child := range s.children {
			if !child.value.Status.Terminal() {
				return sessionUncertain()
			}
		}
		return nil
	}
	proof := domain.OpenCodeStopObservation{RequestID: stop.RequestID, InputRequestID: stop.InputRequestID, InputPartID: s.input.receipt.PartID, AssistantID: history.AssistantID, HistoryDigest: history.Digest, HTTPAccepted: stop.HTTPAccepted, InterruptedObserved: stop.InterruptedObserved, TerminalObserved: stop.TerminalObserved, IdleObserved: stop.IdleObserved, PendingCleared: stop.PendingCleared, CleanupVerified: stop.CleanupVerified}
	proof.RetryObservations = slices.Clone(s.observer.stop.retries)
	if proof.Validate() != nil {
		return sessionUncertain()
	}
	for _, child := range s.children {
		if child.value.Status.Terminal() {
			continue
		}
		value := child.value
		value.Source, value.SourceID, value.Status = domain.OpenCodeChildCleanupSource, "cleanup:"+value.NativeID, domain.SubagentInterrupted
		value.OpenCodeCleanup = &proof
		child.value = value
		s.childInventoryFacts = append(s.childInventoryFacts, value)
		if s.logger != nil {
			s.logger.Info("opencode_original_child_scope_cleaned", "owner_id", s.owner, "status", value.Status, "native_terminal_available", false)
		}
	}
	return nil
}

// This method reads only the original native scope. Worker callers cannot
// supply an endpoint, child identity or projection to manufacture inventory.
func (a *OwnedAPI) InspectForegroundChildren(ctx context.Context) ([]domain.SubagentObservation, error) {
	if !a.valid() {
		return nil, sessionInvalid()
	}
	select {
	case a.reading <- struct{}{}:
		defer func() { <-a.reading }()
	case <-ctx.Done():
		return nil, unavailable()
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.leave()
	if a.completed == nil && a.stoppedCompletion == nil {
		if err := s.verifyForegroundChildren(ctx); err != nil {
			return nil, err
		}
	} else if !s.childInventoryVerified {
		return nil, sessionUncertain()
	}
	return copyChildObservations(s.childInventoryFacts), nil
}

func validChildPermissionRule(rule PermissionRule) bool {
	return domain.Text(rule.Permission, "native permission", 256, true) == nil && domain.Text(rule.Pattern, "native permission pattern", 32768, true) == nil && (rule.Action == PermissionAsk || rule.Action == PermissionAllow || rule.Action == PermissionDeny)
}
