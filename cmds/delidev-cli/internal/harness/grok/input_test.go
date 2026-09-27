package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fixtureInput(root, workspace, mode string, request domain.ID, raw json.RawMessage) {
	var params promptParams
	if decode(raw, &params) != nil || params.Session != turnFixtureSession || len(params.Prompt) != 1 || params.Prompt[0].Type != "text" {
		os.Exit(60)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "input-started"), []byte("started"), 0600)
	if strings.HasPrefix(mode, "planning-") {
		fixturePlanningInput(root, workspace, mode, request, params.Prompt[0].Text)
		return
	}
	if strings.HasPrefix(mode, "question-") {
		fixtureQuestionInput(workspace, mode, request, params.Prompt[0].Text)
		return
	}
	if strings.HasPrefix(mode, "read-") {
		fixtureReadInput(workspace, mode, request, params.Prompt[0].Text)
		return
	}
	if strings.HasPrefix(mode, "write-") {
		fixtureWriteInput(workspace, mode, request, params.Prompt[0].Text)
		return
	}
	write := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	response := fixtureObject(promptResultFixture)
	if mode == "input-conflicting-result" {
		response["_meta"].(map[string]any)["inputTokens"] = 99
	}
	reply := func() { write(map[string]any{"jsonrpc": "2.0", "id": request, "result": response}) }
	if mode == "input-native-error" {
		write(map[string]any{"jsonrpc": "2.0", "id": request, "error": map[string]any{"code": -32603, "message": "private-native-sentinel"}})
		return
	}
	if mode == "input-rpc-first" {
		reply()
	}
	notify := func(method string, raw []byte) {
		write(map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(raw)})
	}
	input := params.Prompt[0].Text
	if mode == "input-foreign" {
		input = "foreign input"
	}
	notify("_x.ai/queue/changed", queueFixture(input, 0))
	notify("_x.ai/queue/changed", queueFixture(input, 1))
	var passive []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(passiveFixture, &passive)
	for _, entry := range passive {
		var value struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		}
		_ = json.Unmarshal(entry.Params, &value)
		if value.Update.Kind == "session_info_update" {
			notify(entry.Method, entry.Params)
			break
		}
	}
	textMethod := "session/update"
	if mode == "input-text-method" {
		textMethod = "_x.ai/session_notification"
	}
	notify(textMethod, textChunkFixture)
	if strings.HasPrefix(mode, "stop-") && mode != "stop-completion-race" {
		return
	}
	if mode == "input-duplicate-chunk" {
		notify("session/update", textChunkFixture)
	}
	for _, entry := range passive {
		var value struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		}
		_ = json.Unmarshal(entry.Params, &value)
		if value.Update.Kind == "response_completed" {
			notify(entry.Method, entry.Params)
			break
		}
	}
	notify("_x.ai/queue/changed", queueFixture(input, 2))
	turnMethod := "_x.ai/session_notification"
	if mode == "input-turn-method" {
		turnMethod = "session/update"
	}
	notify(turnMethod, turnCompletedFixture)
	notify("_x.ai/session/prompt_complete", promptCompletedFixture)
	if mode != "input-rpc-first" && mode != "input-lost-response" {
		reply()
	}
	fixtureInputTail(workspace, mode, notify)
}

func TestOwnedInputRetainsClaimsAndRejectsUncertainReplay(t *testing.T) {
	for _, mode := range []string{"input-valid", "input-rpc-first", "input-foreign", "input-duplicate-chunk", "input-conflicting-result", "input-native-error", "input-lost-response", "input-claim-failure", "input-bind-failure", "input-publication-failure", "input-text-method", "input-turn-method"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var claims []InputClaim
			var observations []InputObservation
			request := domain.NewID()
			result, err := api.RunText(ctx, request, "Original input.", func(_ context.Context, claim InputClaim) error {
				if claim.Validate() != nil || claim.RequestID != request {
					t.Fatal("invalid original claim")
				}
				claims = append(claims, claim)
				if mode == "input-claim-failure" || mode == "input-bind-failure" && claim.Phase == BindInput {
					return context.Canceled
				}
				return nil
			}, func(_ context.Context, observation InputObservation) error {
				observations = append(observations, observation)
				if mode == "input-lost-response" && observation.Kind == InputText {
					time.AfterFunc(50*time.Millisecond, cancel)
				}
				if mode == "input-publication-failure" {
					return context.Canceled
				}
				return nil
			})
			success := mode == "input-valid" || mode == "input-rpc-first"
			if success {
				if err != nil || len(claims) != 2 || result.Meta.Prompt != claims[1].NativePromptID || len(observations) != 4 || observations[0].Kind != InputAccepted || observations[3].Kind != InputCompleted {
					t.Fatal("original native input evidence missing", err, len(observations))
				}
			} else {
				if err == nil {
					t.Fatal("uncertain original input completed")
				}
				for _, observation := range observations {
					if observation.Kind == InputCompleted {
						t.Fatal("uncertain completion published")
					}
				}
			}
			if mode == "input-claim-failure" {
				if _, err := os.Stat(filepath.Join(config.Probe.Process.Cwd, "tmp", "input-started")); !os.IsNotExist(err) {
					t.Fatal("input sent without original claim")
				}
			}
			if _, err := api.RunText(context.Background(), domain.NewID(), "Replacement input.", func(context.Context, InputClaim) error { t.Error("input replay claimed"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
				t.Fatal("original input boundary reopened")
			}
		})
	}
}

func TestInputPreflightPreservesUnusedBoundary(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "input-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	creation := domain.NewID()
	if _, err := api.Create(context.Background(), creation, domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunText(context.Background(), creation, "Reused creation identity.", func(context.Context, InputClaim) error { t.Error("creation request reused for input"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil || api.inputStarted {
		t.Fatal("creation identity consumed an input claim")
	}
	for _, input := range []string{"/always-approve on", strings.Repeat("<", 200<<10), ""} {
		if _, err := api.RunText(context.Background(), domain.NewID(), input, func(context.Context, InputClaim) error { t.Error("invalid input claimed"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
			t.Fatal("invalid native input accepted")
		}
		if api.inputStarted {
			t.Fatal("preflight consumed original input boundary")
		}
	}
}

func TestInputPublicationJoinsNativeClosure(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "input-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, returned := make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := api.RunText(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(publication context.Context, observation InputObservation) error {
			if observation.Kind != InputAccepted {
				return domain.Fail(domain.Internal, "Unexpected publication.", "Retain original input ownership.")
			}
			close(entered)
			<-publication.Done()
			return publication.Err()
		})
		returned <- err
	}()
	select {
	case <-entered:
	case err := <-returned:
		t.Fatal("input ended before publication", err)
	case <-ctx.Done():
		t.Fatal("publication was not reached")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("native closure did not retain uncertain input", err)
		}
		if ctx.Err() != nil {
			t.Fatal("publication depended on caller cancellation")
		}
	case <-ctx.Done():
		t.Fatal("native closure did not cancel publication")
	}
}
