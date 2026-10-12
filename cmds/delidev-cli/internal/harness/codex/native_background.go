// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"sort"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func boundedBackgroundOutput(t *domain.NativeBackgroundTerminal, text string) {
	available := domain.MaxNativeBackgroundOutput - len(t.Output)
	if len(text) > available {
		text = text[:available]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		t.OutputTruncated = true
	}
	t.Output += text
}

// Only original commandExecution items bind native process identifiers. List
// membership alone cannot adopt a restored, borrowed, or foreign process.
func (c *Client) observeBackgroundLocked(e Event) error {
	if !e.Correlated || e.ThreadID != c.thread {
		return nil
	}
	if c.background == nil {
		c.background = map[string]domain.NativeBackgroundTerminal{}
	}
	if e.Tool != nil && e.Tool.Kind == CommandTool && e.Tool.Command != nil && e.Tool.Command.ProcessID != nil {
		cmd := e.Tool.Command
		if cmd.Source != AgentCommand && cmd.Source != ExecStartupCommand {
			return nil
		}
		t, known := c.background[e.ItemID]
		if !known {
			if len(c.background) >= 512 {
				return domain.NativeBackgroundRecovery()
			}
			t = domain.NativeBackgroundTerminal{ItemID: e.ItemID, ProcessID: *cmd.ProcessID, Command: cmd.Command, Cwd: cmd.Cwd}
		}
		original := domain.NativeBackgroundTerminal{ItemID: e.ItemID, ProcessID: *cmd.ProcessID, Command: cmd.Command, Cwd: cmd.Cwd}
		if !domain.SameNativeBackgroundTarget(t, original) {
			return domain.NativeBackgroundRecovery()
		}
		if cmd.ExitCode != nil {
			t.ExitCode = cmd.ExitCode
		}
		if cmd.AggregatedOutput != nil && t.Output == "" {
			boundedBackgroundOutput(&t, *cmd.AggregatedOutput)
		}
		if t.Validate() != nil {
			return domain.NativeBackgroundRecovery()
		}
		c.background[e.ItemID] = t
	}
	if e.Kind == ToolOutputEvent && e.ToolOutputKind == CommandTool {
		if t, known := c.background[e.ItemID]; known {
			boundedBackgroundOutput(&t, e.TextDelta)
			c.background[e.ItemID] = t
		}
	}
	return nil
}
func (c *Client) listBackgroundLocked(ctx context.Context) ([]domain.NativeBackgroundTerminal, error) {
	if c.version != NativeShellVersion || c.mode != ThreadProtocol || c.execution == nil || c.thread.Validate() != nil || c.problem != nil || c.backgroundInvalid {
		return nil, domain.NativeBackgroundRecovery()
	}
	if c.backgroundReads == nil {
		c.backgroundReads = map[domain.ID]bool{}
	}
	requestID := domain.NewID()
	c.backgroundReads[requestID] = true
	response, err := c.wire.Call(ctx, requestID, "thread/backgroundTerminals/list", struct {
		ThreadID domain.ID `json:"threadId"`
		Cursor   *string   `json:"cursor"`
		Limit    uint32    `json:"limit"`
	}{c.thread, nil, domain.MaxNativeBackgroundTerminals + 1})
	if err != nil {
		return nil, err
	}
	delete(c.backgroundReads, requestID)
	if response.ErrorCode != nil {
		return nil, nativeRejected(*response.ErrorCode)
	}
	var wire struct {
		Data []struct {
			ItemID    string   `json:"itemId"`
			ProcessID string   `json:"processId"`
			Command   string   `json:"command"`
			Cwd       string   `json:"cwd"`
			OSPID     *uint32  `json:"osPid"`
			CPU       *float64 `json:"cpuPercent"`
			RSS       *uint64  `json:"rssKb"`
		} `json:"data"`
		Next *string `json:"nextCursor"`
	}
	if domain.Decode(response.Result, &wire) != nil || wire.Data == nil || wire.Next != nil || len(wire.Data) > domain.MaxNativeBackgroundTerminals {
		return nil, domain.NativeBackgroundRecovery()
	}
	seen := map[string]bool{}
	for _, w := range wire.Data {
		t, known := c.background[w.ItemID]
		if !known || seen[w.ProcessID] || w.OSPID != nil || w.CPU != nil || w.RSS != nil || !domain.SameNativeBackgroundTarget(t, domain.NativeBackgroundTerminal{ItemID: w.ItemID, ProcessID: w.ProcessID, Command: w.Command, Cwd: w.Cwd}) {
			return nil, domain.NativeBackgroundRecovery()
		}
		seen[w.ProcessID] = true
	}
	if c.backgroundListed == nil {
		c.backgroundListed = map[string]bool{}
	}
	result := make([]domain.NativeBackgroundTerminal, 0, len(c.background))
	for key, t := range c.background {
		if c.backgroundListed[key] && !t.Present && seen[t.ProcessID] {
			return nil, domain.NativeBackgroundRecovery()
		}
		t.Present = seen[t.ProcessID]
		if t.Present {
			c.backgroundListed[key] = true
		}
		c.background[key] = t
		if c.backgroundListed[key] {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ItemID < result[j].ItemID })
	return result, nil
}
func (c *Client) InspectBackground(ctx context.Context) ([]domain.NativeBackgroundTerminal, error) {
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	return c.listBackgroundLocked(ctx)
}

// The private Worker send journal and server claim must precede this method.
// Its in-memory fence also rejects duplicates, including after a lost ACK.
func (c *Client) ControlBackground(ctx context.Context, a domain.NativeBackgroundAction) (domain.NativeBackgroundObservation, error) {
	o := domain.NativeBackgroundObservation{ActionID: a.ActionID, Delivery: domain.NativeBackgroundNotSent}
	if a.Validate() != nil || a.State != domain.NativeBackgroundClaimed {
		return o, domain.NativeBackgroundRecovery()
	}
	if err := c.acquireControl(ctx); err != nil {
		return o, err
	}
	defer func() { <-c.control }()
	if a.NativeThreadID != domain.NativeIdentity(c.thread) || a.JobID != c.ownerID || c.execution == nil {
		return o, domain.NativeBackgroundRecovery()
	}
	if c.backgroundSent == nil {
		c.backgroundSent = map[domain.ID]bool{}
	}
	if c.backgroundSent[a.ActionID] || len(c.backgroundSent) >= domain.MaxNativeBackgroundActions {
		return o, domain.NativeBackgroundRecovery()
	}
	current, err := c.listBackgroundLocked(ctx)
	if err != nil {
		return o, err
	}
	targets := []domain.NativeBackgroundTerminal{}
	for _, t := range current {
		if t.Present {
			targets = append(targets, t)
		}
	}
	for _, target := range a.Targets {
		found := false
		for _, t := range targets {
			if domain.SameNativeBackgroundTarget(target, t) {
				found = true
			}
		}
		if !found {
			return o, domain.Fail(domain.Conflict, "The selected native background process is stale.", "Refresh the original native inventory; this control was not sent.")
		}
	}
	if a.Operation == domain.NativeBackgroundClean && len(targets) != len(a.Targets) {
		return o, domain.Fail(domain.Conflict, "The native background scope changed before clean.", "Refresh and explicitly select the current original scope.")
	}
	if a.Operation == domain.NativeBackgroundList {
		o.Acknowledged = true
		o.Delivery = domain.NativeBackgroundAcknowledged
		o.NativeEmpty = len(targets) == 0
		return o, nil
	}
	if ctx.Err() != nil {
		return o, domain.SafeError(ctx.Err())
	}
	c.backgroundSent[a.ActionID] = true
	o.Delivery = domain.NativeBackgroundSendUnknown
	method := "thread/backgroundTerminals/clean"
	params := map[string]any{"threadId": c.thread}
	if a.Operation == domain.NativeBackgroundTerminate {
		method = "thread/backgroundTerminals/terminate"
		params["processId"] = a.ProcessID
	}
	response, err := c.wire.Call(ctx, a.ActionID, method, params)
	if err != nil {
		o.ProblemCode = domain.RecoveryRequired
		return o, domain.NativeBackgroundRecovery()
	}
	if response.ErrorCode != nil {
		o.ProblemCode = domain.SafeError(nativeRejected(*response.ErrorCode)).Code
		return o, nativeRejected(*response.ErrorCode)
	}
	if a.Operation == domain.NativeBackgroundTerminate {
		var result struct {
			Terminated *bool `json:"terminated"`
		}
		if domain.Decode(response.Result, &result) != nil || result.Terminated == nil {
			o.ProblemCode = domain.RecoveryRequired
			return o, domain.NativeBackgroundRecovery()
		}
		o.Terminated = result.Terminated
	} else {
		var empty struct{}
		if domain.Decode(response.Result, &empty) != nil {
			o.ProblemCode = domain.RecoveryRequired
			return o, domain.NativeBackgroundRecovery()
		}
	}
	o.Acknowledged = true
	o.Delivery = domain.NativeBackgroundAcknowledged
	return o, nil
}
