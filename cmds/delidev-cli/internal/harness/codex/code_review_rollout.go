// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// CodeReviewResult reads original structured findings from the original owned
// rollout. V2 exitedReviewMode.review is rendered text, never JSON authority.
// The Worker must independently join native/auth/workspace cleanup before it
// can publish this result with CleanupVerified=true.
func (c *Client) CodeReviewResult(ctx context.Context) (domain.NativeCodeReviewResult, error) {
	var result domain.NativeCodeReviewResult
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	a := c.codeReview
	if a == nil || !a.terminal || c.execution.active != "" || c.problem != nil || c.execution.turns[a.turn].Turn.Status != TurnCompleted {
		return result, codeReviewUncertain()
	}
	if err := c.verifySidechat(ctx, c.execution.settings.Cwd, c.thread); err != nil {
		return result, err
	}
	wire, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	var path string
	if err != nil || wire.ID != c.thread || wire.SessionID != c.thread || wire.Status.Type != ThreadIdle || wire.ParentThreadID != nil || wire.ForkedFromID != nil || json.Unmarshal(wire.Path, &path) != nil {
		return result, codeReviewUncertain()
	}
	before, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return result, err
	}
	// Reuse the path/component/identity proof before and after the bounded read.
	root, err := os.OpenRoot(c.home)
	if err != nil {
		return result, codeReviewUncertain()
	}
	defer root.Close()
	relative, err := filepath.Rel(c.home, path)
	if err != nil {
		return result, codeReviewUncertain()
	}
	file, err := root.OpenFile(relative, os.O_RDONLY|forkNonblockFlag(), 0)
	if err != nil {
		return result, codeReviewUncertain()
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxForkRollout+1))
	if err != nil || len(raw) > maxForkRollout || sha256.Sum256(raw) != before {
		return result, codeReviewUncertain()
	}
	result, err = c.decodeCodeReviewRollout(raw)
	if err != nil {
		return domain.NativeCodeReviewResult{}, err
	}
	after, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil || before != after {
		return domain.NativeCodeReviewResult{}, codeReviewUncertain()
	}
	result.RolloutDigest = hex.EncodeToString(after[:])
	return result, nil
}

type codeReviewNativeTarget struct {
	Type         string  `json:"type"`
	Branch       string  `json:"branch,omitempty"`
	SHA          string  `json:"sha,omitempty"`
	Title        *string `json:"title,omitempty"`
	Instructions string  `json:"instructions,omitempty"`
}

func (a *codeReviewAttempt) matchesTarget(t codeReviewNativeTarget) bool {
	if t.Title != nil {
		return false
	}
	switch a.selection.Target.Kind {
	case domain.ReviewUncommitted:
		return t.Type == "uncommittedChanges" && t.Branch == "" && t.SHA == "" && t.Instructions == ""
	case domain.ReviewBaseBranch:
		return t.Type == "baseBranch" && t.Branch == a.selection.BaseCommit && t.SHA == "" && t.Instructions == ""
	case domain.ReviewCommit:
		return t.Type == "commit" && t.SHA == a.selection.Target.Reference && t.Branch == "" && t.Instructions == ""
	case domain.ReviewCustom:
		return t.Type == "custom" && t.Instructions == strings.TrimSpace(a.selection.Target.Instructions) && t.Branch == "" && t.SHA == ""
	}
	return false
}

type codeReviewNativeOutput struct {
	Findings []struct {
		Title      string  `json:"title"`
		Body       string  `json:"body"`
		Confidence float64 `json:"confidence_score"`
		Priority   int32   `json:"priority"`
		Location   struct {
			Path  string `json:"absolute_file_path"`
			Lines struct {
				Start uint32 `json:"start"`
				End   uint32 `json:"end"`
			} `json:"line_range"`
		} `json:"code_location"`
	} `json:"findings"`
	Correctness string  `json:"overall_correctness"`
	Explanation string  `json:"overall_explanation"`
	Confidence  float64 `json:"overall_confidence_score"`
}

func (c *Client) decodeCodeReviewRollout(raw []byte) (domain.NativeCodeReviewResult, error) {
	var result domain.NativeCodeReviewResult
	a := c.codeReview
	if a == nil || len(raw) > maxForkRollout {
		return result, codeReviewUncertain()
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	sessionBound, entered, exited := false, false, false
	var output *codeReviewNativeOutput
	for scanner.Scan() {
		var line struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if domain.DecodeBounded(scanner.Bytes(), &line, 1<<20) != nil {
			return result, codeReviewUncertain()
		}
		if _, err := time.Parse(time.RFC3339Nano, line.Timestamp); err != nil {
			return result, codeReviewUncertain()
		}
		if line.Type == "session_meta" {
			if sessionBound {
				return result, codeReviewUncertain()
			}
			var metadata map[string]json.RawMessage
			var id, session domain.ID
			if domain.Decode(line.Payload, &metadata) != nil || json.Unmarshal(metadata["id"], &id) != nil || json.Unmarshal(metadata["session_id"], &session) != nil || id != c.thread || session != c.thread {
				return result, codeReviewUncertain()
			}
			sessionBound = true
			continue
		}
		if !sessionBound {
			return result, codeReviewUncertain()
		}
		if line.Type != "event_msg" {
			continue
		}
		var fields map[string]json.RawMessage
		var kind string
		if domain.DecodeBounded(line.Payload, &fields, 1<<20) != nil || json.Unmarshal(fields["type"], &kind) != nil {
			return result, codeReviewUncertain()
		}
		switch kind {
		case "entered_review_mode":
			var value struct {
				Type   string                 `json:"type"`
				Target codeReviewNativeTarget `json:"target"`
				Hint   *string                `json:"user_facing_hint,omitempty"`
				Turn   domain.ID              `json:"turn_id"`
				Item   string                 `json:"item_id"`
			}
			if entered || exited || domain.DecodeBounded(line.Payload, &value, 1<<20) != nil || value.Turn != a.turn || value.Item != a.entered || !a.matchesTarget(value.Target) || value.Hint == nil || *value.Hint != a.items[a.entered].text {
				return result, codeReviewUncertain()
			}
			entered = true
		case "exited_review_mode":
			var value struct {
				Type   string                  `json:"type"`
				Turn   domain.ID               `json:"turn_id"`
				Item   string                  `json:"item_id"`
				Output *codeReviewNativeOutput `json:"review_output"`
			}
			if !entered || exited || !c.reviewProtected.Safe(line.Payload) || domain.DecodeBounded(line.Payload, &value, 1<<20) != nil || value.Turn != a.turn || value.Item != a.exited || value.Output == nil || value.Output.Findings == nil || len(value.Output.Findings) > 64 {
				return result, codeReviewUncertain()
			}
			exited = true
			output = value.Output
		}
	}
	if scanner.Err() != nil || !sessionBound || !entered || !exited || output == nil {
		return result, codeReviewUncertain()
	}
	result = domain.NativeCodeReviewResult{Version: 1, ActionID: a.action, Selection: a.selection, ThreadID: domain.NativeIdentity(c.thread), TurnID: domain.NativeIdentity(a.turn), EnteredItemID: a.entered, ExitedItemID: a.exited, Findings: []domain.NativeCodeReviewFinding{}, Explanation: output.Explanation, Correctness: output.Correctness, Confidence: output.Confidence}
	for _, native := range output.Findings {
		if !filepath.IsAbs(native.Location.Path) {
			return domain.NativeCodeReviewResult{}, codeReviewUncertain()
		}
		relative, err := filepath.Rel(c.execution.settings.Cwd, native.Location.Path)
		if err != nil {
			return domain.NativeCodeReviewResult{}, codeReviewUncertain()
		}
		finding := domain.NativeCodeReviewFinding{Title: native.Title, Body: native.Body, Confidence: native.Confidence, Priority: native.Priority, Path: filepath.ToSlash(relative), StartLine: native.Location.Lines.Start, EndLine: native.Location.Lines.End}
		if finding.Validate() != nil {
			return domain.NativeCodeReviewResult{}, codeReviewUncertain()
		}
		result.Findings = append(result.Findings, finding)
	}
	// Validate public bounds without claiming cleanup from private parsing.
	checked := result
	checked.RolloutDigest = strings.Repeat("0", 64)
	checked.CleanupVerified = true
	if checked.Validate() != nil {
		return domain.NativeCodeReviewResult{}, codeReviewUncertain()
	}
	return result, nil
}
