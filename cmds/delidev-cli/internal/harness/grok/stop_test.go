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

func fixtureStop(root, workspace, mode string, original domain.ID, raw json.RawMessage) {
	var params closeParams
	if !strings.HasPrefix(mode, "stop-") || original.Validate() != nil || decode(raw, &params) != nil || params.Session != turnFixtureSession {
		os.Exit(66)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "stop-started"), []byte("started"), 0600)
	if mode == "stop-completion-race" {
		return
	}
	write := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	notify := func(method string, raw []byte) {
		write(map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(raw)})
	}
	terminal := interruptedTurnFixture
	if mode == "stop-foreign" {
		terminal = []byte(strings.ReplaceAll(string(terminal), string(turnFixtureSession), string(domain.NewID())))
	}
	if mode != "stop-missing-turn" {
		notify("_x.ai/session_notification", terminal)
	}
	if mode != "stop-missing-prompt" {
		notify("_x.ai/session/prompt_complete", interruptedCompletedFixture)
	}
	if mode != "stop-missing-rpc" {
		if mode == "stop-native-error" {
			write(map[string]any{"jsonrpc": "2.0", "id": original, "error": map[string]any{"code": -32603, "message": "private-stop-error"}})
		} else {
			write(map[string]any{"jsonrpc": "2.0", "id": original, "result": json.RawMessage(interruptedResultFixture)})
		}
	}
	if mode != "stop-missing-idle" {
		var entries []struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(passiveFixture, &entries)
		for _, entry := range entries {
			if entry.Method != "_x.ai/sessions/changed" {
				continue
			}
			value := fixtureObject(entry.Params)
			item := value["upserted"].([]any)[0].(map[string]any)
			if item["activity"] != "idle" {
				continue
			}
			item["cwd"] = workspace
			if mode == "stop-active-drift" {
				item["activity"] = "working"
			}
			raw, _ := json.Marshal(value)
			notify(entry.Method, raw)
		}
	}
}

func TestOriginalTextStopSeparatesSubmissionTerminalAndCleanup(t *testing.T) {
	for _, mode := range []string{"stop-valid", "stop-completion-race", "stop-claim-failure", "stop-publication-failure", "stop-foreign", "stop-missing-turn", "stop-missing-prompt", "stop-missing-rpc", "stop-missing-idle", "stop-active-drift", "stop-native-error"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			creation, product, input, request := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			if _, err := api.Create(context.Background(), creation, product, func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := api.StopText(context.Background(), request, func(context.Context, StopClaim) error { t.Error("unstarted input claimed Stop"); return nil }); err == nil {
				t.Fatal("unstarted input stopped")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			var missingTimer *time.Timer
			missingDone := make(chan struct{})
			defer func() {
				cancel()
				if missingTimer != nil && !missingTimer.Stop() {
					<-missingDone
				}
			}()
			var claims []StopClaim
			completed, settled := false, false
			_, err = api.RunText(ctx, input, "Original input.", func(context.Context, InputClaim) error { return nil }, func(ctx context.Context, observation InputObservation) error {
				if observation.Kind == InputText {
					for _, reused := range []domain.ID{creation, product, input, config.Probe.Process.OwnerID} {
						if _, err := api.StopText(ctx, reused, func(context.Context, StopClaim) error { t.Error("reused original ID claimed"); return nil }); err == nil {
							t.Fatal("prior request acquired Stop authority")
						}
					}
					delivered, err := api.StopText(ctx, request, func(_ context.Context, claim StopClaim) error {
						claims = append(claims, claim)
						if claim.Validate() != nil || claim.InputRequestID != input || claim.RequestID != request || claim.NativePromptID != turnFixturePrompt {
							t.Error("Stop claim lost original input")
						}
						if mode == "stop-claim-failure" {
							return context.Canceled
						}
						return nil
					})
					if mode == "stop-claim-failure" {
						return err
					}
					if err != nil || !delivered.Claimed || !delivered.Attempted || !delivered.Delivered || delivered.NativeReason != "" || delivered.Idle || delivered.CleanupJoined {
						t.Fatal("delivery invented native completion", err)
					}
					if strings.HasPrefix(mode, "stop-missing-") {
						// Missing terminal facts time out only after original delivery.
						// Race-instrumented native fixture startup is not Stop latency.
						missingTimer = time.AfterFunc(time.Second, func() {
							defer close(missingDone)
							cancel()
						})
					}
				}
				if observation.Kind == InputCompleted {
					completed = true
				}
				if observation.Kind == StopSettled {
					settled = true
					proof := observation.Stop
					if proof == nil || !proof.CleanupJoined || !proof.Idle || proof.Claim.RequestID != request || proof.NativeReason == "" {
						t.Fatal("Stop settlement omitted independent evidence")
					}
					if mode == "stop-completion-race" {
						if proof.NativeReason != EndTurn || proof.Category != "" || observation.Interruption != nil || !completed {
							t.Fatal("successful race was rewritten as interrupted")
						}
					} else if proof.NativeReason != Cancelled || proof.Category != MidTurnAbort || observation.Interruption == nil || completed {
						t.Fatal("interruption fabricated successful completion")
					}
					if mode == "stop-publication-failure" {
						return context.Canceled
					}
				}
				return nil
			})
			success := mode == "stop-valid" || mode == "stop-completion-race"
			if mode == "stop-valid" {
				if err == nil || domain.SafeError(err).Code != domain.Canceled {
					t.Fatal("native interruption classification lost", err)
				}
			} else if mode == "stop-completion-race" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("uncertain Stop succeeded")
			}
			proof, inspectErr := api.InspectStop()
			if inspectErr != nil || len(claims) != 1 || success && (!settled || !proof.CleanupJoined || proof.ProblemCode != "") || !success && mode != "stop-publication-failure" && settled {
				t.Fatal("original Stop evidence changed", inspectErr)
			}
			if api.completedText != nil {
				t.Fatal("Stop force cleanup acquired successful history authority")
			}
			original, originalErr := (&OwnedAPI{connection: api}).ObserveStoppedText(context.Background())
			if success {
				if originalErr != nil || original.Validate(turnFixtureModel) != nil || original.Stop != proof || original.CreationRequestID != creation || len(original.ChunkDigests) != 1 {
					t.Fatal("original stopped comparison missing", originalErr)
				}
			} else if originalErr == nil {
				t.Fatal("incomplete or failed Stop acquired comparison authority")
			}
			select {
			case <-api.wire.Done():
			default:
				t.Fatal("owned process cleanup not joined")
			}
			if _, err := api.StopText(context.Background(), domain.NewID(), func(context.Context, StopClaim) error { t.Error("Stop replay claimed"); return nil }); err == nil {
				t.Fatal("Stop boundary reopened")
			}
			if mode == "stop-claim-failure" {
				if _, err := os.Stat(filepath.Join(config.Probe.Process.Cwd, "tmp", "stop-started")); !os.IsNotExist(err) {
					t.Fatal("failed original claim sent native cancellation")
				}
			}
		})
	}
}

func TestOriginalStopClaimCancelsWithNativeLifetime(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "stop-valid")
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
		_, err := api.RunText(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, observation InputObservation) error {
			if observation.Kind == InputText {
				_, err := api.StopText(callback, domain.NewID(), func(claimContext context.Context, _ StopClaim) error {
					close(entered)
					<-claimContext.Done()
					return claimContext.Err()
				})
				return err
			}
			return nil
		})
		returned <- err
	}()
	select {
	case <-entered:
	case err := <-returned:
		t.Fatal("Stop claim not reached", err)
	case <-ctx.Done():
		t.Fatal("Stop claim blocked before callback")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || ctx.Err() != nil {
			t.Fatal("Stop waited for caller timeout or fabricated success", err)
		}
	case <-ctx.Done():
		t.Fatal("native closure did not release Stop claim")
	}
	if _, err := os.Stat(filepath.Join(config.Probe.Process.Cwd, "tmp", "stop-started")); !os.IsNotExist(err) {
		t.Fatal("uncertain claim sent native Stop")
	}
}

func TestOriginalStopCanSubmitWhilePublicationIsBlocked(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "stop-valid")
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
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := api.RunText(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, observation InputObservation) error {
			if observation.Kind == InputText {
				close(entered)
				select {
				case <-release:
				case <-callback.Done():
					return callback.Err()
				}
			}
			return nil
		})
		returned <- err
	}()
	select {
	case <-entered:
	case err := <-returned:
		t.Fatal("publication not reached", err)
	case <-ctx.Done():
		t.Fatal("publication not reached")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := api.StopText(canceled, domain.NewID(), func(context.Context, StopClaim) error { t.Error("canceled request claimed Stop"); return nil }); err == nil {
		t.Fatal("canceled Stop accepted")
	}
	type submission struct {
		observation StopObservation
		err         error
	}
	submissions := make(chan submission, 2)
	recorded := make(chan StopClaim, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			observation, err := api.StopText(ctx, domain.NewID(), func(_ context.Context, claim StopClaim) error { recorded <- claim; return nil })
			submissions <- submission{observation, err}
		}()
	}
	close(start)
	accepted := 0
	for i := 0; i < 2; i++ {
		submitted := <-submissions
		if submitted.err == nil {
			accepted++
			if !submitted.observation.Delivered || submitted.observation.NativeReason != "" {
				t.Fatal("delivery invented native completion")
			}
		}
	}
	if accepted != 1 || len(recorded) != 1 {
		t.Fatal("concurrent Stop claimed more than one original operation")
	}
	close(release)
	select {
	case err := <-returned:
		if err == nil || domain.SafeError(err).Code != domain.Canceled || ctx.Err() != nil {
			t.Fatal("original stopped input did not settle", err)
		}
	case <-ctx.Done():
		t.Fatal("stopped input did not settle")
	}
}
