// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const CodeReviewEvent EventKind = "code-review"

type CodeReviewObservation struct {
	ActionID domain.ID
	Stage    domain.NativeCodeReviewState
	ItemID   string
}
type codeReviewItem struct {
	kind      string
	text      string
	started   int64
	completed *int64
}
type codeReviewAttempt struct {
	action    domain.ID
	selection domain.NativeCodeReviewSelection
	turn      domain.ID
	items     map[string]codeReviewItem
	entered   string
	exited    string
	terminal  bool
}

func configureCodeReview(config *Config) error {
	if config.CodeReviewModel == "" {
		return nil
	}
	if config.Mode != ThreadProtocol || domain.Text(config.CodeReviewModel, "original review model", 256, true) != nil || config.Sidechat != "" || config.EnableImageGeneration || config.RevertHistory || config.ManagedForkHistory || config.SkillsRoot != "" || config.ImageRoot != "" || config.ModelObservation {
		return domain.NativeCodeReviewUnavailable()
	}
	// Reuse the existing closed private restriction profile. This is not a
	// Sidechat/Fork operation or a grant of its product capabilities.
	config.Sidechat = ReadOnlySidechatV1
	encoded, err := json.Marshal(config.CodeReviewModel)
	if err != nil {
		return domain.NativeCodeReviewUnavailable()
	}
	config.Process.Args = append(config.Process.Args, "-c", "review_model="+string(encoded))
	return nil
}

func codeReviewUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original native code review is unconfirmed.", "Retain its original request, target, history and cleanup evidence; do not resend or substitute ordinary feedback.")
}

// StartCodeReview owns one original native send on an independent read-only
// thread. A rejection/lost response cannot authorize another operation.
func (c *Client) StartCodeReview(ctx context.Context, action domain.ID, selection domain.NativeCodeReviewSelection) (diagnosticResult TurnResult, returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	result := TurnResult{RequestID: action}
	if action.Validate() != nil || selection.Validate() != nil || c.codeReviewModel == "" {
		return result, domain.NativeCodeReviewUnavailable()
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if c.eligibleTurnLocked(false) != nil || c.codeReview != nil || c.execution.active != "" || len(c.execution.turns) != 0 || len(c.execution.inputs) != 0 || c.codeReviewModel != c.execution.settings.Model || !sidechatEffective(c.execution.settings) {
		return result, domain.NativeCodeReviewUnavailable()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return result, err
	}
	if err := c.verifySidechat(ctx, c.execution.settings.Cwd, c.thread); err != nil {
		return result, err
	}
	target := map[string]any{}
	switch selection.Target.Kind {
	case domain.ReviewUncommitted:
		target["type"] = "uncommittedChanges"
	case domain.ReviewBaseBranch:
		target["type"], target["branch"] = "baseBranch", selection.BaseCommit
	case domain.ReviewCommit:
		target["type"], target["sha"], target["title"] = "commit", selection.Target.Reference, nil
	case domain.ReviewCustom:
		target["type"], target["instructions"] = "custom", selection.Target.Instructions
	default:
		return result, domain.NativeCodeReviewUnavailable()
	}
	// Claim before native Call, including transport/acknowledgment uncertainty.
	c.codeReview = &codeReviewAttempt{action: action, selection: selection, items: map[string]codeReviewItem{}}
	response, err := c.wire.Call(ctx, action, "review/start", struct {
		ThreadID domain.ID      `json:"threadId"`
		Target   map[string]any `json:"target"`
		Delivery string         `json:"delivery"`
	}{c.thread, target, "inline"})
	var ack struct {
		Turn     json.RawMessage `json:"turn"`
		ThreadID domain.ID       `json:"reviewThreadId"`
	}
	if err != nil || response.ErrorCode != nil || domain.DecodeBounded(response.Result, &ack, c.nativeFrameLimit()) != nil || ack.ThreadID != c.thread {
		c.problem = codeReviewUncertain()
		return result, c.problem
	}
	turn, err := decodeTurn(ack.Turn)
	if err != nil || turn.Status != TurnRunning {
		c.problem = codeReviewUncertain()
		return result, c.problem
	}
	c.codeReview.turn = turn.ID
	c.execution.turns[turn.ID] = trackedTurn{Turn: turn, Mode: domain.PlanMode}
	c.execution.active = turn.ID
	result.TurnID = turn.ID
	return result, nil
}

func (c *Client) observeCodeReviewLocked(native nativewire.Event, thread, turn domain.ID, raw json.RawMessage, started, completed *int64) (Event, error) {
	a := c.codeReview
	if c.codeReviewModel == "" || a == nil {
		return privateNative(native), nil
	}
	if thread != c.thread || turn != a.turn || a.turn.Validate() != nil || a.terminal {
		return Event{}, codeReviewUncertain()
	}
	var item struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Review string `json:"review"`
	}
	if domain.DecodeBounded(raw, &item, c.nativeFrameLimit()) != nil || domain.Text(item.ID, "native review item", 256, true) != nil || domain.Text(item.Review, "native review display", nativewire.MaxFrame, false) != nil {
		return Event{}, codeReviewUncertain()
	}
	previous, seen := a.items[item.ID]
	if native.Method == "item/started" {
		if started == nil || *started < 0 || completed != nil {
			return Event{}, codeReviewUncertain()
		}
		if seen {
			if previous.kind != item.Type || previous.text != item.Review || previous.started != *started {
				return Event{}, codeReviewUncertain()
			}
			return Event{Kind: MetadataEvent}, nil
		}
		if item.Type == "enteredReviewMode" {
			if a.entered != "" || a.exited != "" {
				return Event{}, codeReviewUncertain()
			}
			a.entered = item.ID
		} else if item.Type == "exitedReviewMode" {
			prior, ok := a.items[a.entered]
			if !ok || prior.completed == nil || a.exited != "" {
				return Event{}, codeReviewUncertain()
			}
			a.exited = item.ID
		} else {
			return Event{}, codeReviewUncertain()
		}
		a.items[item.ID] = codeReviewItem{kind: item.Type, text: item.Review, started: *started}
		return Event{Kind: MetadataEvent}, nil
	}
	if completed == nil || *completed < 0 || !seen || previous.kind != item.Type || previous.text != item.Review || *completed < previous.started {
		return Event{}, codeReviewUncertain()
	}
	if previous.completed != nil {
		if *previous.completed != *completed {
			return Event{}, codeReviewUncertain()
		}
		return Event{Kind: MetadataEvent}, nil
	}
	previous.completed = completed
	a.items[item.ID] = previous
	stage := domain.NativeReviewEntered
	if item.Type == "exitedReviewMode" {
		stage = domain.NativeReviewExited
	}
	return Event{Kind: CodeReviewEvent, ThreadID: thread, TurnID: turn, ItemID: item.ID, Correlated: true, CodeReview: &CodeReviewObservation{ActionID: a.action, Stage: stage, ItemID: item.ID}}, nil
}
