package claude

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const protectedDeltaSecret = "fixture-delta-secret/+é"

func protectedDeltaForms() []string {
	secret := []byte(protectedDeltaSecret)
	return []string{protectedDeltaSecret, base64.StdEncoding.EncodeToString(secret), base64.RawStdEncoding.EncodeToString(secret), base64.URLEncoding.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret)}
}

func protectedDeltaFixture(t *testing.T) *ExecutionBinding {
	t.Helper()
	config, _ := apiFixtureConfig(t, "binding")
	config.Process.ProtectedValues = []string{protectedDeltaSecret}
	b, err := BindExecution(config, domain.NewID(), lifecycleFixtureText)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
	init := lifecycleInit(t, b)
	init = lifecycleChange(t, init, "tools", []string{"Bash", "Read", "Write", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "Task"})
	lifecycleObserve(t, b, init)
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	return b
}

func protectedDeltaBlock(t *testing.T, b *ExecutionBinding, message string, index int, block map[string]any) {
	t.Helper()
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": index, "content_block": block}))
}

func protectedDeltaSend(b *ExecutionBinding, t *testing.T, index int, delta map[string]any) (LifecycleObservation, error) {
	t.Helper()
	return b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": index, "delta": delta}))
}

// publishedDeltas appends only what public observers could see.
func publishedDeltas(published *strings.Builder, observation LifecycleObservation) {
	for _, event := range observation.Content {
		if event.Delta != nil {
			published.WriteString(*event.Delta)
		}
	}
}

func TestProtectedValueSplitAcrossDeltasIsRefusedBeforeExposure(t *testing.T) {
	profiles := []struct {
		name  string
		block map[string]any
		delta func(string) map[string]any
	}{
		{"text", map[string]any{"type": "text", "text": ""}, func(v string) map[string]any { return map[string]any{"type": "text_delta", "text": v} }},
		{"thinking", map[string]any{"type": "thinking", "thinking": "", "signature": ""}, func(v string) map[string]any { return map[string]any{"type": "thinking_delta", "thinking": v} }},
		{"tool-input", map[string]any{"type": "tool_use", "id": "tool_protected", "name": "Write", "input": map[string]any{}}, func(v string) map[string]any { return map[string]any{"type": "input_json_delta", "partial_json": v} }},
	}
	for _, profile := range profiles {
		for formIndex, form := range protectedDeltaForms() {
			body := "ok " + form + " end"
			if profile.name == "tool-input" {
				body = `{"note":"` + form + `"}`
			}
			for split := 1; split < len(body); split++ {
				if !utf8.RuneStart(body[split]) {
					continue
				}
				b := protectedDeltaFixture(t)
				contentStart(t, b, "", "msg_protected")
				protectedDeltaBlock(t, b, "msg_protected", 0, profile.block)
				var published strings.Builder
				refused := false
				for _, part := range []string{body[:split], body[split:]} {
					observation, err := protectedDeltaSend(b, t, 0, profile.delta(part))
					if err != nil {
						refused = true
						break
					}
					publishedDeltas(&published, observation)
				}
				if !refused || b.problem == nil {
					t.Fatalf("%s form %d split %d was not refused", profile.name, formIndex, split)
				}
				if strings.Contains(published.String(), form) || !strings.HasPrefix(body, published.String()) {
					t.Fatalf("%s form %d split %d published a reconstructible or reordered sequence", profile.name, formIndex, split)
				}
				for _, state := range b.content.active[""].blocks {
					if state.guard.Held() != 0 {
						t.Fatal("refusal retained a private holdback")
					}
				}
			}
		}
	}
}

func TestProtectedDeltaHoldbackKeepsSafeTextOrderedAndReleasedOnCleanCompletion(t *testing.T) {
	b := protectedDeltaFixture(t)
	contentStart(t, b, "", "msg_safe")
	protectedDeltaBlock(t, b, "msg_safe", 0, map[string]any{"type": "text", "text": ""})
	parts := []string{"Harmless ", "fixture-delta ", "text ", "fixture-", "delta-", "other "}
	// The final parts keep an unresolved protected prefix through completion.
	parts = append(parts, "fixture-delta-sec")
	var published strings.Builder
	withheld := 0
	for _, part := range parts {
		observation, err := protectedDeltaSend(b, t, 0, map[string]any{"type": "text_delta", "text": part})
		if err != nil {
			t.Fatal(err)
		}
		if len(observation.Content) != 1 || observation.Content[0].Kind != ContentChanged {
			t.Fatal("delta changed the one-observation lifecycle shape")
		}
		if observation.Content[0].Withheld {
			if observation.Content[0].Delta != nil {
				t.Fatal("withheld delta carried text")
			}
			withheld++
		}
		publishedDeltas(&published, observation)
		if held := b.content.active[""].blocks[0].guard.Held(); held >= len(protectedDeltaSecret) {
			t.Fatalf("holdback is not bounded by the longest form: %d", held)
		}
	}
	full := strings.Join(parts, "")
	if withheld == 0 || !strings.HasPrefix(full, published.String()) || published.String() == full {
		t.Fatal("expected a bounded private holdback of the unresolved suffix", withheld)
	}
	completed := lifecycleObserve(t, b, contentCompleted(t, b, "", "msg_safe", map[string]any{"type": "text", "text": full})).Content[0]
	if completed.Kind != ContentCompleted || completed.Held == nil || published.String()+*completed.Held != full || *completed.Block.Text != full || b.content.bufferedBytes != 0 {
		t.Fatal("clean completion lost the held suffix or the exact block")
	}
	if b.content.active[""].blocks[0].guard.Held() != 0 {
		t.Fatal("completed block retained its holdback")
	}
}

func TestProtectedDeltaStateDoesNotLeakAcrossBlocks(t *testing.T) {
	b := protectedDeltaFixture(t)
	contentStart(t, b, "", "msg_independent")
	protectedDeltaBlock(t, b, "msg_independent", 0, map[string]any{"type": "text", "text": ""})
	first := "tail fixture-delta-"
	if _, err := protectedDeltaSend(b, t, 0, map[string]any{"type": "text_delta", "text": first}); err != nil {
		t.Fatal(err)
	}
	completed := lifecycleObserve(t, b, contentCompleted(t, b, "", "msg_independent", map[string]any{"type": "text", "text": first})).Content[0]
	if completed.Held == nil || *completed.Held == "" {
		t.Fatal("expected the unresolved suffix to be released only with its own completion")
	}
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0}))
	protectedDeltaBlock(t, b, "msg_independent", 1, map[string]any{"type": "text", "text": ""})
	// The next block continues with the rest of the form; it is another
	// original block and must not inherit the previous guard state.
	observation, err := protectedDeltaSend(b, t, 1, map[string]any{"type": "text_delta", "text": "secret/+é continues"})
	if err != nil || observation.Content[0].Delta == nil || *observation.Content[0].Delta != "secret/+é continues" {
		t.Fatal("independent block inherited another block's guard state", err)
	}
}

func TestProtectedDeltaInitialBlockTextParticipatesInMatching(t *testing.T) {
	b := protectedDeltaFixture(t)
	contentStart(t, b, "", "msg_initial")
	protectedDeltaBlock(t, b, "msg_initial", 0, map[string]any{"type": "text", "text": "start fixture-delta-"})
	if _, err := protectedDeltaSend(b, t, 0, map[string]any{"type": "text_delta", "text": "secret/+é"}); err == nil {
		t.Fatal("initial published text was ignored")
	}
}

func TestProtectedDeltaToolInputRefusesUnresolvedSuffixAtCompletion(t *testing.T) {
	config, _ := apiFixtureConfig(t, "binding")
	config.Process.ProtectedValues = []string{"}brace-form-secret"}
	b, err := BindExecution(config, domain.NewID(), lifecycleFixtureText)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
	lifecycleObserve(t, b, lifecycleChange(t, lifecycleInit(t, b), "tools", []string{"Bash", "Read", "Write", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "Task"}))
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	contentStart(t, b, "", "msg_tool")
	block := map[string]any{"type": "tool_use", "id": "tool_brace", "name": "Write", "input": map[string]any{}}
	protectedDeltaBlock(t, b, "msg_tool", 0, block)
	if _, err := protectedDeltaSend(b, t, 0, map[string]any{"type": "input_json_delta", "partial_json": "{"}); err != nil {
		t.Fatal(err)
	}
	observation, err := protectedDeltaSend(b, t, 0, map[string]any{"type": "input_json_delta", "partial_json": "}"})
	if err != nil || !observation.Content[0].Withheld {
		t.Fatal("closing brace should stay private while it may start a protected form", err)
	}
	completed := contentCompleted(t, b, "", "msg_tool", block)
	if _, err := b.Observe(completed); err == nil {
		t.Fatal("tool input completed with an unreleasable protected-prefix suffix")
	}
	if b.content.active[""].blocks[0].guard.Held() != 0 {
		t.Fatal("refusal retained a private holdback")
	}
}

func TestInterruptedBlockDiscardsPrivateHoldback(t *testing.T) {
	b := protectedDeltaFixture(t)
	b.interrupt = &InterruptClaim{1, b.owner, b.session, b.input, b.turnID, domain.NewID()}
	contentStart(t, b, "", "msg_interrupted_protected")
	protectedDeltaBlock(t, b, "msg_interrupted_protected", 0, map[string]any{"type": "text", "text": ""})
	text := "partial fixture-delta-"
	if _, err := protectedDeltaSend(b, t, 0, map[string]any{"type": "text_delta", "text": text}); err != nil {
		t.Fatal(err)
	}
	state := b.content.active[""].blocks[0]
	if state.guard.Held() == 0 {
		t.Fatal("expected an unresolved private suffix")
	}
	closed := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0}))
	if closed.Content[0].Kind != ContentInterruptStreamClosed || closed.Content[0].Delta != nil || closed.Content[0].Held != nil || state.guard.Held() != 0 {
		t.Fatal("interrupted stream published or retained its holdback")
	}
}

func TestUnconfiguredProtectionLeavesDeltasUnchanged(t *testing.T) {
	b := contentFixture(t)
	contentStart(t, b, "", "msg_plain")
	lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}))
	observation := lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "fixture-delta-"}}))
	if observation.Content[0].Withheld || *observation.Content[0].Delta != "fixture-delta-" {
		t.Fatal("unprotected delta was delayed")
	}
	completed := lifecycleObserve(t, b, contentCompleted(t, b, "", "msg_plain", map[string]any{"type": "text", "text": "fixture-delta-"})).Content[0]
	if completed.Held != nil {
		t.Fatal("completion invented a held suffix")
	}
}
