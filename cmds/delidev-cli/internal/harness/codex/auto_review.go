// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"reflect"
	"slices"
)

// Decode private action context only to validate the pinned notification shape.
// Never copy actions, rationale or risk assessments into product history/logs.
func validReviewAction(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || len(raw) > 256<<10 {
		return false
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return false
	}
	var value struct {
		Type          string             `json:"type"`
		Source        string             `json:"source,omitempty"`
		Command       string             `json:"command,omitempty"`
		Cwd           string             `json:"cwd,omitempty"`
		Program       string             `json:"program,omitempty"`
		Argv          []string           `json:"argv,omitempty"`
		ApprovalID    string             `json:"approvalId,omitempty"`
		ProcessID     string             `json:"processId,omitempty"`
		Stdin         string             `json:"stdin,omitempty"`
		Files         []string           `json:"files,omitempty"`
		Target        string             `json:"target,omitempty"`
		Host          string             `json:"host,omitempty"`
		Protocol      string             `json:"protocol,omitempty"`
		Port          uint16             `json:"port,omitempty"`
		Server        string             `json:"server,omitempty"`
		ToolName      string             `json:"toolName,omitempty"`
		ConnectorID   *string            `json:"connectorId,omitempty"`
		ConnectorName *string            `json:"connectorName,omitempty"`
		ToolTitle     *string            `json:"toolTitle,omitempty"`
		Reason        *string            `json:"reason,omitempty"`
		Permissions   *PermissionProfile `json:"permissions,omitempty"`
	}
	if domain.Decode(raw, &value) != nil {
		return false
	}
	keys := map[string][]string{
		"command": {"type", "source", "command", "cwd"}, "execve": {"type", "source", "program", "argv", "cwd"}, "writeStdin": {"type", "approvalId", "processId", "stdin", "cwd"}, "applyPatch": {"type", "cwd", "files"}, "networkAccess": {"type", "target", "host", "protocol", "port"}, "mcpToolCall": {"type", "server", "toolName", "connectorId", "connectorName", "toolTitle"}, "requestPermissions": {"type", "reason", "permissions"},
	}[kind]
	if keys == nil || len(fields) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	for _, s := range []string{value.Source, value.Command, value.Cwd, value.Program, value.ApprovalID, value.ProcessID, value.Stdin, value.Target, value.Host, value.Protocol, value.Server, value.ToolName} {
		if domain.Text(s, "private review action", 256<<10, false) != nil {
			return false
		}
	}
	if (kind == "command" || kind == "execve") && !slices.Contains([]string{"shell", "unifiedExec"}, value.Source) {
		return false
	}
	if len(value.Argv) > 4096 || len(value.Files) > 1024 {
		return false
	}
	for _, s := range append(value.Argv, value.Files...) {
		if domain.Text(s, "private review argument", 32768, false) != nil {
			return false
		}
	}
	if kind == "requestPermissions" && (value.Permissions == nil || value.Permissions.Validate() != nil) {
		return false
	}
	return true
}
func (c *Client) observeAutoReviewLocked(native nativewire.Event) (Event, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &fields) != nil {
		return Event{}, incompatible()
	}
	required := []string{"threadId", "turnId", "startedAtMs", "reviewId", "targetItemId", "review", "action"}
	if native.Method == "item/autoApprovalReview/completed" {
		required = append(required, "completedAtMs", "decisionSource")
	}
	if len(fields) != len(required) {
		return Event{}, incompatible()
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return Event{}, incompatible()
		}
	}
	var reviewFields map[string]json.RawMessage
	if domain.Decode(fields["review"], &reviewFields) != nil || len(reviewFields) != 4 {
		return Event{}, incompatible()
	}
	for _, name := range []string{"status", "riskLevel", "userAuthorization", "rationale"} {
		if _, ok := reviewFields[name]; !ok {
			return Event{}, incompatible()
		}
	}

	var p struct {
		ThreadID       domain.ID `json:"threadId"`
		TurnID         domain.ID `json:"turnId"`
		StartedAtMS    *int64    `json:"startedAtMs"`
		CompletedAtMS  *int64    `json:"completedAtMs,omitempty"`
		ReviewID       string    `json:"reviewId"`
		TargetItemID   *string   `json:"targetItemId"`
		DecisionSource *string   `json:"decisionSource,omitempty"`
		Review         *struct {
			Status        domain.AutoReviewStatus `json:"status"`
			Risk          *string                 `json:"riskLevel"`
			Authorization *string                 `json:"userAuthorization"`
			Rationale     *string                 `json:"rationale"`
		} `json:"review"`
		Action json.RawMessage `json:"action"`
	}
	if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.TurnID.Validate() != nil || p.StartedAtMS == nil || p.Review == nil || !validReviewAction(p.Action) {
		return Event{}, incompatible()
	}
	if p.ThreadID != c.thread {
		return privateNative(native), nil
	}
	if c.execution.settings.ApprovalsReviewer != "auto_review" {
		return Event{}, incompatible()
	}
	turn, ok := c.execution.turns[p.TurnID]
	if !ok || turn.Turn.Status.terminal() {
		return Event{}, incompatible()
	}
	if p.Review.Risk != nil && !slices.Contains([]string{"low", "medium", "high", "critical"}, *p.Review.Risk) || p.Review.Authorization != nil && !slices.Contains([]string{"unknown", "low", "medium", "high"}, *p.Review.Authorization) || p.Review.Rationale != nil && domain.Text(*p.Review.Rationale, "private review rationale", 256<<10, false) != nil {
		return Event{}, incompatible()
	}
	started := native.Method == "item/autoApprovalReview/started"
	if started != (p.Review.Status == domain.AutoReviewInProgress) || started && (p.DecisionSource != nil || p.CompletedAtMS != nil) || !started && (p.DecisionSource == nil || *p.DecisionSource != "agent" || p.CompletedAtMS == nil) {
		return Event{}, incompatible()
	}
	v := domain.AutoReviewObservation{ReviewID: p.ReviewID, TargetItemID: p.TargetItemID, Status: p.Review.Status, StartedAtMS: *p.StartedAtMS, CompletedAtMS: p.CompletedAtMS}
	if c.execution.autoReviews == nil {
		c.execution.autoReviews = map[domain.ID]domain.AutoReviewState{}
	}

	var actionFields map[string]json.RawMessage
	_ = json.Unmarshal(p.Action, &actionFields)
	canonical, _ := json.Marshal(actionFields)
	digest := sha256.Sum256(canonical)
	key := string(p.TurnID) + "/" + p.ReviewID
	if c.execution.autoReviewActions == nil {
		c.execution.autoReviewActions = map[string][32]byte{}
	}
	var payload any
	_ = json.Unmarshal(native.Params, &payload)
	canonicalPayload, _ := json.Marshal(payload)
	payloadDigest := sha256.Sum256(canonicalPayload)
	payloadKey := key + "/" + string(v.Status)
	if c.execution.autoReviewPayloads == nil {
		c.execution.autoReviewPayloads = map[string][32]byte{}
	}
	old, exists := c.execution.autoReviews[p.TurnID][p.ReviewID]
	if exists && c.execution.autoReviewActions[key] != digest {
		return Event{}, incompatible()
	}
	if exists && reflect.DeepEqual(old, v) {
		if c.execution.autoReviewPayloads[payloadKey] != payloadDigest {
			return Event{}, incompatible()
		}
		return c.metadata(AutoReviewReplayChecked), nil
	}
	next, err := domain.ApplyAutoReview(c.execution.autoReviews[p.TurnID], v)
	if err != nil {
		return Event{}, incompatible()
	}
	c.execution.autoReviewPayloads[payloadKey] = payloadDigest
	c.execution.autoReviewActions[key] = digest
	c.execution.autoReviews[p.TurnID] = next
	if c.logger != nil {
		c.logger.Debug("native_auto_review_observed", "status", v.Status)
	}
	return Event{Kind: AutoReviewEvent, ThreadID: p.ThreadID, TurnID: p.TurnID, AutoReview: &v, Correlated: true}, nil
}
