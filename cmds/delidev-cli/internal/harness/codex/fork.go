// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const forkThread threadMethod = "thread/fork"
const maxForkTurns = 128
const maxForkRollout = 64 << 20

// ForkSource is in-memory evidence from the exact original Worker runtime.
// Its private path is never supplied by a product client or serialized publicly.
type ForkSource struct {
	home, path string
	checkpoint ContinuationCheckpoint
	fileDigest [sha256.Size]byte
	turns      []json.RawMessage
}

func unsupportedFork() error {
	return domain.Fail(domain.Unsupported, "This native history is outside the settled Codex fork profile.", "Use completed legacy root history without goals, children, queued native work or unsupported items.")
}

// InspectForkSource performs only read operations. It cannot Resume, clear a
// goal, approve a request or consume a source queue. The caller independently
// holds the original closed workspace/process lease and account authority.
func (c *Client) InspectForkSource(ctx context.Context, checkpoint ContinuationCheckpoint) (diagnosticResult *ForkSource, returned error) {
	defer c.recordFailure(ctx, domain.CodexHistory, &returned)
	if c.mode != ThreadProtocol || checkpoint.validate(ContinueAfterSuccess) != nil || checkpoint.Status != TurnCompleted {
		return nil, unsupportedFork()
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	if c.problem != nil {
		return nil, c.problem
	}
	wire, err := c.readThreadLocked(ctx, domain.NewID(), checkpoint.ThreadID)
	if err != nil || !forkableMetadata(wire, checkpoint) {
		return nil, unsupportedFork()
	}
	if err := c.noForkWorkLocked(ctx, checkpoint.ThreadID); err != nil {
		return nil, err
	}
	turns, err := c.forkTurnsLocked(ctx, checkpoint.ThreadID)
	if err != nil {
		return nil, err
	}
	last, inputs, err := decodeLatestTurnInputs(marshalForkPage(turns[len(turns)-1:]))
	if err != nil || last.ID != checkpoint.TurnID || last.Status != TurnCompleted || !slices.Equal(inputs, checkpoint.Inputs) {
		return nil, continuationUncertain()
	}
	var path string
	if json.Unmarshal(wire.Path, &path) != nil {
		return nil, unsupportedFork()
	}
	digest, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return nil, err
	}
	again, err := c.readThreadLocked(ctx, domain.NewID(), checkpoint.ThreadID)
	if err != nil || !forkableMetadata(again, checkpoint) || string(again.Path) != string(wire.Path) || *again.UpdatedAt != *wire.UpdatedAt {
		return nil, continuationUncertain()
	}
	return &ForkSource{home: c.home, path: path, checkpoint: checkpoint, fileDigest: digest, turns: turns}, nil
}

func forkableMetadata(wire threadWire, checkpoint ContinuationCheckpoint) bool {
	return wire.ID == checkpoint.ThreadID && wire.SessionID == checkpoint.SessionID && wire.ParentThreadID == nil && wire.summary().History == LegacyHistory && (wire.Status.Type == ThreadIdle || wire.Status.Type == ThreadNotLoaded) && (wire.CanAcceptDirectInput == nil || *wire.CanAcceptDirectInput) && nativePathEqual(wire.Cwd, checkpoint.Effective.Cwd) && wire.ModelProvider == checkpoint.Effective.Provider && (len(wire.Extra) == 0 || string(wire.Extra) == "null")
}

func (c *Client) noForkWorkLocked(ctx context.Context, thread domain.ID) error {
	// The original source independently proved an empty goal before Fork.
	// Sidechat's verified disabled Goals feature prevents inheritance/dispatch,
	// and this installed version rejects goal/get when that feature is disabled.
	// Ordinary Fork still requires the original native read; do not infer an
	// empty goal from a failed read or apply this exception to other profiles.
	if c.sidechat == "" {
		goal, err := c.wire.Call(ctx, domain.NewID(), "thread/goal/get", struct {
			Thread domain.ID `json:"threadId"`
		}{thread})
		if err != nil {
			return err
		}
		var g struct {
			Goal json.RawMessage `json:"goal"`
		}
		if goal.ErrorCode != nil || domain.Decode(goal.Result, &g) != nil || string(g.Goal) != "null" {
			return unsupportedFork()
		}
	}
	queue, err := c.wire.Call(ctx, domain.NewID(), "thread/queue/list", struct {
		Thread domain.ID `json:"threadId"`
		Limit  int       `json:"limit"`
	}{thread, 1})
	if err != nil {
		return err
	}
	var q struct {
		Data []json.RawMessage `json:"data"`
		Next *string           `json:"nextCursor"`
	}
	if queue.ErrorCode != nil || domain.Decode(queue.Result, &q) != nil || q.Data == nil || len(q.Data) != 0 || q.Next != nil {
		return unsupportedFork()
	}
	return nil
}

func marshalForkPage(turns []json.RawMessage) json.RawMessage {
	raw, _ := json.Marshal(struct {
		Data []json.RawMessage `json:"data"`
		Next *string           `json:"nextCursor"`
		Back *string           `json:"backwardsCursor"`
	}{Data: turns})
	return raw
}

func (c *Client) forkTurnsLocked(ctx context.Context, thread domain.ID) ([]json.RawMessage, error) {
	var turns []json.RawMessage
	var cursor *string
	seen := map[string]bool{}
	bytes := 0
	for {
		response, err := c.wire.Call(ctx, domain.NewID(), "thread/turns/list", struct {
			Thread    domain.ID `json:"threadId"`
			Limit     int       `json:"limit"`
			Direction string    `json:"sortDirection"`
			View      string    `json:"itemsView"`
			Cursor    *string   `json:"cursor,omitempty"`
		}{thread, 50, "asc", "full", cursor})
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []json.RawMessage `json:"data"`
			Next *string           `json:"nextCursor"`
			Back *string           `json:"backwardsCursor"`
		}
		if response.ErrorCode != nil || domain.Decode(response.Result, &page) != nil || page.Data == nil || len(page.Data) > 50 {
			return nil, unsupportedFork()
		}
		for _, raw := range page.Data {
			turn, _, err := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{raw}))
			if err != nil || turn.Status != TurnCompleted || seen[string(turn.ID)] {
				return nil, unsupportedFork()
			}
			seen[string(turn.ID)] = true
			var wire turnWire
			if domain.Decode(raw, &wire) != nil {
				return nil, unsupportedFork()
			}
			for _, item := range wire.Items {
				var identity struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(item, &identity) != nil || !slices.Contains([]string{"userMessage", "agentMessage", "reasoning"}, identity.Type) {
					return nil, unsupportedFork()
				}
			}
			bytes += len(raw)
			if len(turns) >= maxForkTurns || bytes > 4<<20 {
				return nil, unsupportedFork()
			}
			// Normalize only JSON representation for comparison; never rebuild
			// or write native history from these observed turns.
			var value any
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			if decoder.Decode(&value) != nil {
				return nil, unsupportedFork()
			}
			normal, _ := json.Marshal(value)
			turns = append(turns, normal)
		}
		if page.Next == nil {
			break
		}
		if len(page.Data) == 0 || domain.Text(*page.Next, "native fork cursor", 4096, true) != nil || seen["cursor:"+*page.Next] {
			return nil, unsupportedFork()
		}
		seen["cursor:"+*page.Next] = true
		cursor = page.Next
	}
	if len(turns) == 0 {
		return nil, unsupportedFork()
	}
	return turns, nil
}

// ForkThread makes one native creation attempt into this fresh private runtime.
// The caller synchronizes the operation claim first. Unknown outcomes latch
// reconciliation and never permit another creation on this connection.
func (c *Client) ForkThread(ctx context.Context, requestID domain.ID, source *ForkSource, settings ThreadSettings) (result ThreadResult, returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	result.RequestID = requestID
	defer func() {
		if c.logger != nil {
			code := domain.Code("ok")
			if returned != nil {
				code = domain.SafeError(returned).Code
			}
			c.logger.InfoContext(ctx, "Codex native fork operation", "owner_id", c.ownerID, "request_id", requestID, "code", code)
		}
	}()
	if source == nil || requestID.Validate() != nil || c.mode != ThreadProtocol || nativePathEqual(c.home, source.home) {
		return result, unsupportedFork()
	}
	params, err := settings.params()
	if err != nil {
		return result, err
	}
	if err := source.Verify(ctx); err != nil {
		return result, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if c.problem != nil {
		return result, c.problem
	}
	if c.thread != "" {
		return result, turnConflict()
	}
	if err := c.verifyAPI(ctx, settings.Cwd); err != nil {
		return result, err
	}
	if c.sidechat != "" && !sidechatSettings(settings) {
		return result, sidechatUnavailable()
	}
	if err := c.verifySidechat(ctx, settings.Cwd, ""); err != nil {
		return result, err
	}
	request := struct {
		Thread       domain.ID      `json:"threadId"`
		Last         domain.ID      `json:"lastTurnId"`
		Path         string         `json:"path"`
		Model        string         `json:"model"`
		Provider     string         `json:"modelProvider"`
		Cwd          string         `json:"cwd"`
		Roots        []string       `json:"runtimeWorkspaceRoots,omitempty"`
		Instructions string         `json:"developerInstructions,omitempty"`
		Policy       ApprovalPolicy `json:"approvalPolicy,omitempty"`
		Reviewer     string         `json:"approvalsReviewer"`
		Sandbox      string         `json:"sandbox,omitempty"`
		Tier         string         `json:"serviceTier,omitempty"`
		Config       map[string]any `json:"config,omitempty"`
		Exclude      bool           `json:"excludeTurns"`
		DeferGoal    bool           `json:"deferGoalContinuation"`
	}{source.checkpoint.ThreadID, source.checkpoint.TurnID, source.path, params.Model, params.ModelProvider, params.Cwd, params.WorkspaceRoots, params.DeveloperInstructions, params.ApprovalPolicy, params.ApprovalsReviewer, params.Sandbox, params.ServiceTier, params.Config, true, true}
	response, err := c.wire.Call(ctx, requestID, string(forkThread), request)
	if err != nil {
		c.problem = threadUncertain()
		if c.logger != nil {
			c.logger.WarnContext(ctx, "Codex Fork transport unconfirmed", "owner_id", c.ownerID, "code", domain.SafeError(err).Code)
		}
		return result, c.problem
	}
	if response.ErrorCode != nil {
		c.problem = threadUncertain()
		if c.logger != nil {
			c.logger.WarnContext(ctx, "Codex native Fork rejected", "owner_id", c.ownerID, "native_error_code", *response.ErrorCode)
		}
		return result, c.problem
	}
	thread, effective, err := decodeBoundThread(response.Result, settings, "", forkThread, c.version)
	result.Thread, result.Effective = thread, effective
	if thread != nil {
		c.thread = thread.ID
	}
	var bound boundThreadWire
	var wire threadWire
	if domain.Decode(response.Result, &bound) == nil {
		wire, _ = decodeThread(bound.Thread, c.version)
	}
	if err != nil || thread == nil || effective == nil || thread.ID == source.checkpoint.ThreadID || thread.SessionID != thread.ID || thread.Status.Type != ThreadIdle || wire.ForkedFromID == nil || *wire.ForkedFromID != source.checkpoint.ThreadID || !c.forkDefaults(source.checkpoint.Effective, *effective) {
		c.problem = threadUncertain()
		if c.logger != nil {
			c.logger.WarnContext(ctx, "Codex fork boundary rejected", "owner_id", c.ownerID, "decoded", err == nil, "thread_observed", thread != nil, "settings_observed", effective != nil)
		}
		return result, c.problem
	}
	if err := c.verifySidechat(ctx, settings.Cwd, thread.ID); err != nil {
		c.problem = threadUncertain()
		c.problem.Guidance = domain.SafeError(err).Guidance
		return result, c.problem
	}
	turns, err := c.forkTurnsLocked(ctx, thread.ID)
	if err != nil || !slices.EqualFunc(turns, source.turns, func(a, b json.RawMessage) bool { return string(a) == string(b) }) {
		c.problem = threadUncertain()
		return result, c.problem
	}
	if err := c.noForkWorkLocked(ctx, thread.ID); err != nil {
		c.problem = threadUncertain()
		return result, c.problem
	}
	if err := source.Verify(ctx); err != nil {
		c.problem = threadUncertain()
		return result, c.problem
	}
	c.execution = newExecutionState(*thread, *effective)
	c.execution.continuationPending = true
	return result, nil
}

func (s *ForkSource) Verify(ctx context.Context) error {
	if s == nil {
		return continuationUncertain()
	}
	actual, err := forkRolloutDigest(ctx, s.home, s.path)
	if err != nil || actual != s.fileDigest {
		return continuationUncertain()
	}
	return nil
}

func forkRolloutDigest(ctx context.Context, home, path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if security.CheckPrivateDir(home) != nil || !filepath.IsAbs(path) {
		return result, unsupportedFork()
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.HasPrefix(rel, "sessions"+string(filepath.Separator)) || !strings.HasSuffix(rel, ".jsonl") {
		return result, unsupportedFork()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return result, unsupportedFork()
	}
	defer root.Close()
	current := ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || !forkPrivateRolloutNode(filepath.Join(home, current), info) {
			return result, unsupportedFork()
		}
	}
	before, err := root.Lstat(rel)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maxForkRollout {
		return result, unsupportedFork()
	}
	file, err := root.OpenFile(rel, os.O_RDONLY|forkNonblockFlag(), 0)
	if err != nil {
		return result, unsupportedFork()
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return result, continuationUncertain()
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var count int64
	for {
		if err := ctx.Err(); err != nil {
			return result, domain.SafeError(err)
		}
		n, err := file.Read(buffer)
		count += int64(n)
		if count > before.Size() {
			return result, continuationUncertain()
		}
		hash.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, continuationUncertain()
		}
	}
	after, err := root.Lstat(rel)
	if err != nil || count != before.Size() || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result, continuationUncertain()
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

// Only paths are remapped. Native defaults and permission flags are immutable;
// their enum names alone cannot stand in for the original effective policy.
func sameForkDefaults(a, b EffectiveSettings) bool {
	return a.Model == b.Model && a.Provider == b.Provider && optionalForkString(a.Effort, b.Effort) && optionalForkString(a.ServiceTier, b.ServiceTier) && a.ApprovalPolicy == b.ApprovalPolicy && a.ApprovalsReviewer == b.ApprovalsReviewer && a.Sandbox.Type == b.Sandbox.Type && a.Sandbox.NetworkAccess == b.Sandbox.NetworkAccess && a.Sandbox.ExcludeSlashTmp == b.Sandbox.ExcludeSlashTmp && a.Sandbox.ExcludeTmpdirEnvVar == b.Sandbox.ExcludeTmpdirEnvVar
}

func optionalForkString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
