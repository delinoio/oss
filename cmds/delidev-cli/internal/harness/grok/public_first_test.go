package grok

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestPublicFirstInputRetainsOriginalToolsAndTextClosure(t *testing.T) {
	for _, mode := range []string{"write-valid", "write-reject", "planning-public", "input-valid"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatalf("initialization: %v; structured diagnostics: %s", err, logs.String())
			}
			defer api.Close()
			if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			count := 0
			_, err = api.runInput(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
				if v.ToolEvent != nil {
					count++
					if _, err := nativeToolPayload(v.ToolEvent.Payload); err != nil {
						return err
					}
				}
				if v.Permission != nil {
					decision := AllowFileOnce
					if mode == "write-reject" {
						decision = RejectFileOnce
					}
					_, err := api.ReplyFilePermission(callback, domain.NewID(), v.Permission.ArrivalID, decision, func(context.Context, FilePermissionClaim) error { return nil })
					return err
				}
				if v.QuestionOffer != nil {
					_, err := api.ReplyQuestion(callback, domain.NewID(), v.QuestionOffer.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"Which original mixed option?": "Blue"}}, func(context.Context, QuestionClaim) error { return nil })
					return err
				}
				if v.PlanOffer != nil {
					_, err := api.ReplyPlan(callback, domain.NewID(), v.PlanOffer.ArrivalID, PlanApproved, func(context.Context, PlanClaim) error { return nil })
					return err
				}
				return nil
			}, publicFirstInput)
			if mode == "write-reject" {
				if err == nil || domain.SafeError(err).Code != domain.Canceled {
					t.Fatal("rejection lost native category", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "input-valid" {
				if count != 0 || api.completedText == nil || api.completedTools != nil {
					t.Fatal("plain text lost its original closure")
				}
				return
			}
			if count == 0 || api.completedText != nil || api.completedTools == nil {
				t.Fatal("tools borrowed text history or lost independent terminal")
			}
			owned := &OwnedAPI{connection: api}
			proof, err := owned.CloseTools(context.Background())
			if err != nil || proof.Terminal.Validate(string(api.session)) != nil {
				t.Fatal("original tools cleanup", err)
			}
			if mode == "write-reject" && proof.Terminal.Reason != domain.GrokToolsPermissionRejected {
				t.Fatal("denial became user Stop")
			}
		})
	}
}

func TestPublicFirstInputInitialPlanSettlesWithoutToolObservations(t *testing.T) {
	config, logs := fixtureAPIConfig(t, "input-valid")
	config.Mode, config.Model = domain.PlanMode, turnFixtureModel
	// Setup and mode selection retain their own native deadlines, matching the
	// existing public fixture. The original input has its separate watchdog.
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatalf("initialization: %v; structured diagnostics: %s", err, logs.String())
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := api.runInput(context.Background(), domain.NewID(), "Original input.", func(context.Context, InputClaim) error {
		t.Error("public Plan input claimed before original mode binding")
		return nil
	}, func(context.Context, InputObservation) error {
		t.Error("public Plan input observed before original mode binding")
		return nil
	}, publicFirstInput); err == nil {
		t.Fatal("public Plan input bypassed original mode selection")
	}
	var modeClaims []ModeClaim
	if _, err := api.SelectPlan(context.Background(), domain.NewID(), func(_ context.Context, claim ModeClaim) error {
		modeClaims = append(modeClaims, claim)
		return claim.Validate()
	}); err != nil {
		t.Fatal(err)
	}
	if len(modeClaims) != 2 || modeClaims[0].Phase != ClaimMode || modeClaims[1].Phase != BindMode {
		t.Fatal("initial Plan mode lacks original claim and binding")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var claims []InputClaim
	var observations []InputObservation
	result, err := api.runInput(ctx, domain.NewID(), "Original input.", func(_ context.Context, claim InputClaim) error {
		claims = append(claims, claim)
		return claim.Validate()
	}, func(_ context.Context, observation InputObservation) error {
		observations = append(observations, observation)
		return nil
	}, publicFirstInput)
	if err != nil {
		t.Fatalf("text-only initial Plan: %v; structured diagnostics: %s", err, logs.String())
	}
	if len(claims) != 2 || claims[0].Phase != ClaimInput || claims[1].Phase != BindInput || result.Reason != EndTurn || result.Meta.Prompt != claims[1].NativePromptID {
		t.Fatal("original text-only Plan input lost its identity or completion")
	}
	if len(observations) != 5 || observations[0].Kind != InputAccepted || observations[3].Kind != InputResponse || observations[4].Kind != InputCompleted {
		t.Fatal("text-only Plan observations lost their original lifecycle")
	}
	for _, observation := range observations {
		if observation.ToolEvent != nil || observation.FileTool != nil || observation.Question != nil || observation.Plan != nil {
			t.Fatal("text-only Plan fabricated a tool or in-prompt mode observation")
		}
	}
	if api.completedText != nil || api.completedTools == nil || api.modeBinding == nil {
		t.Fatal("initial Plan borrowed Execute history or lost its terminal")
	}
	owned := &OwnedAPI{connection: api}
	proof, err := owned.CloseTools(ctx)
	if err != nil || proof.Terminal.Validate(string(api.session)) != nil || proof.Terminal.Reason != domain.GrokToolsEndTurn {
		t.Fatalf("text-only Plan original cleanup: %v", err)
	}
}
