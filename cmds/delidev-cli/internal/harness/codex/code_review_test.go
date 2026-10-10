// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func reviewFixture(t *testing.T) *Client {
	t.Helper()
	root, turn, action := domain.NewID(), domain.NewID(), domain.NewID()
	selection := domain.NativeCodeReviewSelection{Target: domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: domain.NewID(), DiffRevision: strings.Repeat("a", 64)}, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)}
	entered, exited := int64(10), int64(11)
	return &Client{thread: root, codeReviewModel: "original-model", reviewProtected: security.NewProtectedJSON(nil), codeReview: &codeReviewAttempt{action: action, selection: selection, turn: turn, entered: "entry", exited: "exit", terminal: true, items: map[string]codeReviewItem{"entry": {kind: "enteredReviewMode", text: "current changes", started: 1, completed: &entered}, "exit": {kind: "exitedReviewMode", text: "rendered native prose", started: 10, completed: &exited}}}, execution: &executionState{settings: EffectiveSettings{Cwd: t.TempDir()}}}
}

func reviewRollout(t *testing.T, c *Client, change func(map[string]any)) []byte {
	t.Helper()
	records := []map[string]any{
		{"timestamp": "2026-10-10T00:00:00Z", "type": "session_meta", "payload": map[string]any{"id": c.thread, "session_id": c.thread}},
		{"timestamp": "2026-10-10T00:00:01Z", "type": "event_msg", "payload": map[string]any{"type": "entered_review_mode", "target": map[string]any{"type": "uncommittedChanges"}, "user_facing_hint": "current changes", "turn_id": c.codeReview.turn, "item_id": "entry"}},
		{"timestamp": "2026-10-10T00:00:02Z", "type": "event_msg", "payload": map[string]any{"type": "exited_review_mode", "turn_id": c.codeReview.turn, "item_id": "exit", "review_output": map[string]any{"findings": []any{map[string]any{"title": "Original finding", "body": "Explain the original defect.", "confidence_score": 0.9, "priority": 1, "code_location": map[string]any{"absolute_file_path": filepath.Join(c.execution.settings.Cwd, "file.go"), "line_range": map[string]any{"start": 3, "end": 4}}}}, "overall_correctness": "patch is incorrect", "overall_explanation": "Original structured result.", "overall_confidence_score": 0.9}}},
	}
	if change != nil {
		change(records[2]["payload"].(map[string]any))
	}
	var raw []byte
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, b...)
		raw = append(raw, '\n')
	}
	return raw
}

func TestNativeReviewFindingsRequireOriginalStructuredRollout(t *testing.T) {
	c := reviewFixture(t)
	result, err := c.decodeCodeReviewRollout(reviewRollout(t, c, nil))
	if err != nil || len(result.Findings) != 1 || result.Findings[0].Path != "file.go" || result.CleanupVerified || result.RolloutDigest != "" {
		t.Fatal("original structured findings lost or private parse claimed cleanup", err)
	}
	for _, kind := range []string{"foreign-turn", "foreign-item", "absent-output", "escaped-path", "invalid-confidence", "protected-content"} {
		t.Run(kind, func(t *testing.T) {
			current := reviewFixture(t)
			if kind == "protected-content" {
				current.reviewProtected = security.NewProtectedJSON([]string{"original-protected-token"})
			}
			raw := reviewRollout(t, current, func(p map[string]any) {
				switch kind {
				case "foreign-turn":
					p["turn_id"] = domain.NewID()
				case "foreign-item":
					p["item_id"] = "foreign"
				case "absent-output":
					p["review_output"] = nil
				default:
					out := p["review_output"].(map[string]any)
					f := out["findings"].([]any)[0].(map[string]any)
					if kind == "escaped-path" {
						f["code_location"].(map[string]any)["absolute_file_path"] = filepath.Join(filepath.Dir(current.execution.settings.Cwd), "outside.go")
					}
					if kind == "invalid-confidence" {
						f["confidence_score"] = 2
					}
					if kind == "protected-content" {
						f["body"] = "original-protected-token"
					}
				}
			})
			if _, err := current.decodeCodeReviewRollout(raw); err == nil {
				t.Fatal("unproved findings accepted")
			}
		})
	}
}

func TestNativeReviewLifecycleRejectsForeignAndReorderedItems(t *testing.T) {
	c := reviewFixture(t)
	c.codeReview.terminal = false
	c.codeReview.entered = ""
	c.codeReview.exited = ""
	c.codeReview.items = map[string]codeReviewItem{}
	started, completed := int64(1), int64(2)
	raw := json.RawMessage(`{"type":"enteredReviewMode","id":"entry","review":"current changes"}`)
	native := nativewire.Event{Method: "item/started"}
	if _, err := c.observeCodeReviewLocked(native, domain.NewID(), c.codeReview.turn, raw, &started, nil); err == nil {
		t.Fatal("foreign root accepted")
	}
	if _, err := c.observeCodeReviewLocked(native, c.thread, c.codeReview.turn, raw, &started, nil); err != nil {
		t.Fatal(err)
	}
	exit := json.RawMessage(`{"type":"exitedReviewMode","id":"exit","review":"rendered"}`)
	if _, err := c.observeCodeReviewLocked(native, c.thread, c.codeReview.turn, exit, &started, nil); err == nil {
		t.Fatal("exit before entered completion accepted")
	}
	native.Method = "item/completed"
	observed, err := c.observeCodeReviewLocked(native, c.thread, c.codeReview.turn, raw, &started, &completed)
	if err != nil || observed.CodeReview == nil || observed.CodeReview.Stage != domain.NativeReviewEntered {
		t.Fatal("entered state lost", err)
	}
	if _, err := c.observeCodeReviewLocked(native, c.thread, c.codeReview.turn, exit, &started, &completed); err == nil {
		t.Fatal("unsolicited completion accepted")
	}
}

func (f *threadFixture) handleNativeCodeReview(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-native-review-") || method != "review/start" {
		return false
	}
	if f.thread == nil {
		os.Exit(90)
	}
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		os.Exit(91)
	}
	out, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(92)
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": params})
	_ = out.Close()
	if params["threadId"] != f.thread["id"] || params["delivery"] != "inline" {
		os.Exit(93)
	}
	f.turn = domain.NewID()
	if f.mode == "thread-native-review-loss" {
		time.Sleep(2 * time.Second)
	}
	write(id, map[string]any{"turn": fixtureTurn(f.turn, TurnRunning), "reviewThreadId": f.thread["id"]})
	return true
}

func TestDedicatedReviewSendsFrozenTargetOnceAndRetainsResponseLoss(t *testing.T) {
	for _, kind := range []domain.NativeCodeReviewTargetKind{domain.ReviewUncommitted, domain.ReviewBaseBranch, domain.ReviewCommit, domain.ReviewCustom} {
		t.Run(string(kind), func(t *testing.T) {
			c, capture := openThreadFixture(t, "thread-native-review-ready")
			settings := threadSettings(t)
			settings.Options = domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "never", ServiceTier: "fast"}
			if _, err := c.StartThread(context.Background(), domain.NewID(), settings); err != nil {
				t.Fatal(err)
			}
			selected := reviewFixture(t).codeReview.selection
			selected.Target.Kind = kind
			if kind == domain.ReviewBaseBranch {
				selected.Target.Reference = "original-branch"
				selected.BaseCommit = strings.Repeat("d", 40)
			}
			if kind == domain.ReviewCommit {
				selected.Target.Reference = strings.Repeat("e", 40)
			}
			if kind == domain.ReviewCustom {
				selected.Target.Instructions = "Inspect the original selection."
			}
			action := domain.NewID()
			if _, err := c.StartCodeReview(context.Background(), action, selected); err != nil {
				t.Fatal(err)
			}
			if _, err := c.StartCodeReview(context.Background(), domain.NewID(), selected); err == nil {
				t.Fatal("repeated dedicated send admitted")
			}
			count := 0
			for _, request := range capturedThreads(t, capture) {
				if request["method"] != "review/start" {
					continue
				}
				count++
				target := request["params"].(map[string]any)["target"].(map[string]any)
				if kind == domain.ReviewBaseBranch && target["branch"] != selected.BaseCommit {
					t.Fatal("moving branch sent instead of pinned commit")
				}
				if kind == domain.ReviewCommit && target["sha"] != selected.Target.Reference {
					t.Fatal("commit target changed")
				}
				if kind == domain.ReviewCustom && target["instructions"] != selected.Target.Instructions {
					t.Fatal("custom target changed")
				}
			}
			if count != 1 {
				t.Fatal("dedicated operation was replaced or repeated")
			}
		})
	}
	c, capture := openThreadFixture(t, "thread-native-review-loss")
	settings := threadSettings(t)
	settings.Options = domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "never", ServiceTier: "fast"}
	if _, err := c.StartThread(context.Background(), domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err := c.StartCodeReview(ctx, domain.NewID(), reviewFixture(t).codeReview.selection)
	cancel()
	if err == nil {
		t.Fatal("lost acknowledgment reported success")
	}
	claimed, err := c.CodeReviewSendClaimed(context.Background())
	if err != nil || !claimed {
		t.Fatal("lost response cleared original send claim", err)
	}
	if _, err := c.StartCodeReview(context.Background(), domain.NewID(), reviewFixture(t).codeReview.selection); err == nil {
		t.Fatal("unknown review retried")
	}
	count := 0
	for _, request := range capturedThreads(t, capture) {
		if request["method"] == "review/start" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("unknown completion issued another operation")
	}
}
