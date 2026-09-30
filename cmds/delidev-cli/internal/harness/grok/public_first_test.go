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
