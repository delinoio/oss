// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// Original app identity is account- and generation-bound. App/link/tool names
// are observations, never alternate account, terminal, file or browser grants.
type CodexAppCallIdentity struct {
	AccountID  ID              `json:"account_id"`
	Generation ID              `json:"configuration_generation"`
	AppID      string          `json:"app_id"`
	LinkID     *string         `json:"link_id"`
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	AppName    *string         `json:"app_name"`
	ActionName *string         `json:"action_name"`
	ReadOnly   *bool           `json:"read_only_hint"`
}

type CodexAppResult struct {
	// Result values are tool output data. Native transport metadata, OAuth
	// material and executable MCP App presentation are not product fields.
	Content    []json.RawMessage `json:"content"`
	Structured json.RawMessage   `json:"structured_content"`
}

type CodexAppCallObservation struct {
	Identity     CodexAppCallIdentity `json:"identity"`
	Result       *CodexAppResult      `json:"result"`
	ErrorPresent bool                 `json:"error_present"`
	DurationMS   *int64               `json:"duration_ms"`
	Progress     *string              `json:"progress"`
}

const CodexAppApprovalQuestionPrefix = "mcp_tool_call_approval_"
const CodexAppApprovalAllow = "Allow"
const CodexAppApprovalCancel = "Cancel"

// This is the original native app's one-call approval transported through its
// user-input request. It cannot approve another native question or tool family.
type CodexAppApprovalContext struct {
	NativeCallID string               `json:"native_call_id"`
	Identity     CodexAppCallIdentity `json:"identity"`
}

func (c CodexAppApprovalContext) Validate(request QuestionRequest) error {
	if Text(c.NativeCallID, "original app call identity", 1024, true) != nil || c.Identity.Validate() != nil || !request.Blocking || request.AutoResolutionMS != nil || len(request.Questions) != 1 {
		return invalidInteraction()
	}
	q := request.Questions[0]
	if !strings.HasPrefix(q.ID, CodexAppApprovalQuestionPrefix) || len(q.ID) <= len(CodexAppApprovalQuestionPrefix) || q.Other || q.Secret || len(q.Options) != 2 || q.Options[0].Label != CodexAppApprovalAllow || q.Options[1].Label != CodexAppApprovalCancel {
		return invalidInteraction()
	}
	return nil
}

func (c CodexAppCallIdentity) Validate() error {
	if c.AccountID.Validate() != nil || c.Generation.Validate() != nil || c.AppID == "_default" || Text(c.AppID, "original app identity", 1024, true) != nil || Text(c.Tool, "original app tool", 1024, true) != nil || len(c.Arguments) == 0 || len(c.Arguments) > MaxMessageText || !json.Valid(c.Arguments) {
		return invalidCodexApps()
	}
	for _, value := range []*string{c.LinkID, c.AppName, c.ActionName} {
		if value != nil && Text(*value, "original app provenance", 4096, false) != nil {
			return invalidCodexApps()
		}
	}
	return nil
}

func (c CodexAppCallObservation) Validate(status ToolStatus) error {
	if c.Identity.Validate() != nil || status != ToolRunning && status != ToolCompleted && status != ToolFailed || c.DurationMS != nil && *c.DurationMS < 0 || c.Progress != nil && Text(*c.Progress, "original app progress", MaxMessageText, false) != nil {
		return invalidCodexApps()
	}
	if status == ToolRunning && (c.Result != nil || c.ErrorPresent || c.DurationMS != nil) || status == ToolCompleted && (c.Result == nil || c.ErrorPresent) {
		return invalidCodexApps()
	}
	if c.Result != nil {
		if c.Result.Content == nil || len(c.Result.Content) > 1024 || len(c.Result.Structured) == 0 || len(c.Result.Structured) > MaxMessageText || !json.Valid(c.Result.Structured) {
			return invalidCodexApps()
		}
		for _, content := range c.Result.Content {
			if len(content) == 0 || len(content) > MaxMessageText || !json.Valid(content) {
				return invalidCodexApps()
			}
		}
	}
	return nil
}

func (c CodexAppCallIdentity) SameOriginal(next CodexAppCallIdentity) bool {
	return c.AccountID == next.AccountID && c.Generation == next.Generation && c.AppID == next.AppID && c.Tool == next.Tool && bytes.Equal(c.Arguments, next.Arguments) && reflect.DeepEqual(c.LinkID, next.LinkID) && reflect.DeepEqual(c.AppName, next.AppName) && reflect.DeepEqual(c.ActionName, next.ActionName) && reflect.DeepEqual(c.ReadOnly, next.ReadOnly)
}

func ValidateCodexAppCallTransition(previous, next ToolSnapshot) error {
	if previous.Kind != CodexAppTool || next.Kind != CodexAppTool || previous.Validate() != nil || next.Validate() != nil || previous.Status != ToolRunning || !previous.CodexApp.Identity.SameOriginal(next.CodexApp.Identity) {
		return invalidTool()
	}
	return nil
}
