package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fixtureInputTail(workspace, mode string, notify func(string, []byte)) {
	if mode == "closure-tail-missing" {
		return
	}
	var passive []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(passiveFixture, &passive)
	for _, entry := range passive {
		value := fixtureObject(entry.Params)
		if entry.Method == "_x.ai/sessions/changed" {
			upserted := value["upserted"].([]any)
			if upserted[0].(map[string]any)["activity"] != "idle" {
				continue
			}
			upserted[0].(map[string]any)["cwd"] = workspace
			if mode == "closure-tail-working" {
				upserted[0].(map[string]any)["activity"] = "working"
			}
		} else if update, ok := value["update"].(map[string]any); !ok || update["sessionUpdate"] != "last_turn_summary" {
			continue
		}
		raw, _ := json.Marshal(value)
		notify(entry.Method, raw)
	}
}

func fixtureClosure(root, mode string, request domain.ID, raw json.RawMessage) {
	var params closeParams
	if decode(raw, &params) != nil || params.Session != turnFixtureSession {
		os.Exit(65)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "closure-started"), []byte("started"), 0600)
	write := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	session := params.Session
	if mode == "closure-foreign-removal" {
		session = domain.NewID()
	}
	remove := func() {
		write(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/sessions/changed", "params": map[string]any{"upserted": []any{}, "removed": []domain.ID{session}}})
	}
	if mode == "closure-removal-first" {
		remove()
	}
	outcome := "closed"
	if mode == "closure-wrong-outcome" {
		outcome = "not_found"
	}
	if mode != "closure-lost-response" {
		write(map[string]any{"jsonrpc": "2.0", "id": request, "result": map[string]any{"_meta": map[string]any{"x.ai/closeOutcome": outcome}}})
	}
	if mode != "closure-ack-only" && mode != "closure-removal-first" {
		remove()
	}
}

func TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval(t *testing.T) {
	for _, mode := range []string{"closure-valid", "closure-removal-first", "closure-foreign-removal", "closure-wrong-outcome", "closure-lost-response", "closure-ack-only", "closure-tail-missing", "closure-tail-working", "closure-claim-failure", "closure-bind-failure"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			creation, product, input := domain.NewID(), domain.NewID(), domain.NewID()
			if _, err := api.Create(context.Background(), creation, product, func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := api.CloseText(context.Background(), domain.NewID(), func(context.Context, ClosureClaim) error {
				t.Error("uncompleted input acquired closure claim")
				return nil
			}); err == nil || api.closureStarted {
				t.Fatal("uncompleted input closed")
			}
			if _, err := api.RunText(context.Background(), input, "Original input.", func(context.Context, InputClaim) error { return nil }, func(context.Context, InputObservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			for _, reused := range []domain.ID{creation, product, input} {
				if _, err := api.CloseText(context.Background(), reused, func(context.Context, ClosureClaim) error { t.Error("reused closure request claimed"); return nil }); err == nil || api.closureStarted {
					t.Fatal("prior request acquired closure authority")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var claims []ClosureClaim
			request := domain.NewID()
			result, err := api.CloseText(ctx, request, func(_ context.Context, claim ClosureClaim) error {
				if claim.Validate() != nil || claim.RequestID != request || claim.NativeSessionID != turnFixtureSession || claim.NativePromptID != turnFixturePrompt {
					t.Error("invalid closure claim")
				}
				claims = append(claims, claim)
				if mode == "closure-claim-failure" || mode == "closure-bind-failure" && claim.Phase == BindClosure {
					return context.Canceled
				}
				return nil
			})
			success := mode == "closure-valid" || mode == "closure-removal-first"
			if success {
				if err != nil || len(claims) != 2 || result.RequestID != request || result.NativePromptID != turnFixturePrompt || result.Summary == "" {
					t.Fatal("original closure incomplete", err)
				}
			} else if err == nil {
				t.Fatal("uncertain closure succeeded")
			}
			if mode == "closure-claim-failure" || mode == "closure-tail-missing" || mode == "closure-tail-working" {
				if _, err := os.Stat(filepath.Join(config.Probe.Process.Cwd, "tmp", "closure-started")); !os.IsNotExist(err) {
					t.Fatal("native closure preceded claim/auxiliary proof")
				}
			}
			select {
			case <-api.wire.Done():
			default:
				t.Fatal("owned process cleanup was not joined")
			}
			if _, err := api.CloseText(context.Background(), domain.NewID(), func(context.Context, ClosureClaim) error { t.Error("closure replay claimed"); return nil }); err == nil {
				t.Fatal("original closure boundary reopened")
			}
		})
	}
}

func TestClosureNativeShapesRejectForeignAndPartialAcknowledgments(t *testing.T) {
	for _, raw := range []string{`{}`, `{"_meta":{"x.ai/closeOutcome":"closed","extension":true}}`, `{"_meta":{"x.ai/closeOutcome":null}}`, `{"_meta":{"x.ai/closeOutcome":"already_closed"}}`, `{"_meta":{"x.ai/CLOSEOutcome":"closed"}}`} {
		if validateCloseResult([]byte(raw)) == nil {
			t.Fatal("invalid close acknowledgment accepted")
		}
	}
	for _, raw := range []string{`{"upserted":null,"removed":[]}`, `{"upserted":[],"removed":null}`, `{"upserted":[],"removed":[]}`, `{"upserted":[],"removed":["foreign"]}`} {
		if validateRemoval([]byte(raw), turnFixtureSession) == nil {
			t.Fatal("missing original removal accepted")
		}
	}
}

func TestTextClosureClaimCancelsWithNativeLifetime(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "closure-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunText(context.Background(), domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(context.Context, InputObservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, returned := make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := api.CloseText(ctx, domain.NewID(), func(claimContext context.Context, claim ClosureClaim) error {
			close(entered)
			<-claimContext.Done()
			return claimContext.Err()
		})
		returned <- err
	}()
	select {
	case <-entered:
	case err := <-returned:
		t.Fatal("closure ended before claim", err)
	case <-ctx.Done():
		t.Fatal("claim not reached")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || ctx.Err() != nil {
			t.Fatal("closure waited for caller cancellation or lost uncertainty", err)
		}
	case <-ctx.Done():
		t.Fatal("blocked closure claim did not join native exit")
	}
	if _, err := os.Stat(filepath.Join(config.Probe.Process.Cwd, "tmp", "closure-started")); !os.IsNotExist(err) {
		t.Fatal("failed claim sent native close")
	}
}
