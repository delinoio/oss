package grok

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPublicGrokOriginalRepliesAndProjection(t *testing.T) {
	for _, profile := range []string{"read-valid", "write-valid", "write-reject", "question-valid", "planning-valid", "input-valid"} {
		t.Run(profile, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, profile)
			config.Model = turnFixtureModel
			claims := 0
			record := PlanningRecorders{Creation: func(context.Context, CreationClaim) error { return nil }, Mode: func(context.Context, ModeClaim) error { return nil }, Input: func(context.Context, InputClaim) error { return nil }, File: func(context.Context, FilePermissionClaim) error { claims++; return nil }, Question: func(context.Context, QuestionClaim) error { claims++; return nil }, Plan: func(context.Context, PlanClaim) error { claims++; return nil }}
			api, err := OpenOwnedPublicAPI(context.Background(), config, record, func(context.Context, ClosureClaim) error { return nil }, func(context.Context, StopClaim) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			mode := domain.GrokDefaultMode
			tools := map[string]domain.ToolSnapshot{}
			indices := map[uint64]string{}
			var offered []domain.ID
			completed := false
			_, err = api.RunPublic(ctx, domain.NewID(), "Original input.", func(callback context.Context, v InputObservation) error {
				if v.Kind == InputCompleted || v.Kind == InputPermissionRejected {
					completed = true
					return nil
				}
				if v.Kind != InputFileTool && v.Kind != InputQuestion && v.Kind != InputPlan {
					return nil
				}
				out, err := PublicObservationOf(v, mode)
				if err != nil {
					return err
				}
				if out.Mode != nil {
					mode = out.Mode.Mode
				}
				if out.Interaction != nil && out.Interaction.Validate() != nil {
					t.Fatal("lost original automatic/resolution stage", out.Interaction)
				}
				if g := out.Tool; g != nil {
					if g.Arguments != nil {
						if out.ToolID == "" {
							out.ToolID = indices[g.Arguments.Index]
							g.Name = tools[out.ToolID].Grok.Name
						} else {
							indices[g.Arguments.Index] = out.ToolID
						}
					}
					status := domain.ToolPending
					if g.Phase == domain.GrokCompleted {
						status = domain.ToolCompleted
					}
					if g.Phase == domain.GrokFailed {
						status = domain.ToolFailed
					}
					prior, exists := tools[out.ToolID]
					if exists && g.Name == domain.GrokAsk && (g.Phase == domain.GrokCompleted || g.Phase == domain.GrokFailed) {
						g.Questions = prior.Grok.Questions
					}
					if g.Name == domain.GrokExitPlan {
						g.Content = ""
					}
					snapshot := domain.ToolSnapshot{Kind: domain.GrokNativeTool, Status: status, Grok: g}
					if snapshot.Validate() != nil {
						t.Fatalf("invalid normalized tool: %#v", g)
					}
					if exists && domain.ValidateGrokToolTransition(prior, snapshot, string(turnFixtureSession)) != nil {
						t.Fatalf("lost original transition: %#v -> %#v", prior.Grok, g)
					}
					tools[out.ToolID] = snapshot
					if g.Phase == domain.GrokDeclared {
						for index, id := range indices {
							if id == out.ToolID {
								delete(indices, index)
							}
						}
					}
				}
				if r := out.Request; r != nil {
					kind := domain.NativeApprovalInteraction
					if r.Kind == domain.GrokQuestionInteraction {
						kind = domain.UserQuestionInteraction
					}
					if r.Validate(kind, domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(r.ArrivalID)}, out.ToolID) != nil {
						t.Fatalf("invalid normalized original request: %#v", r)
					}
					offered = append(offered, r.ArrivalID)
					switch r.Kind {
					case domain.GrokFilePermission:
						decision := AllowFileOnce
						if profile == "write-reject" {
							decision = RejectFileOnce
						}
						if _, err := api.ReplyFilePermission(callback, domain.NewID(), r.ArrivalID, decision); err != nil {
							return err
						}
					case domain.GrokQuestionInteraction:
						answers := map[string]string{}
						for _, q := range r.Questions {
							answers[q.Question] = "Blue"
						}
						if _, err := api.ReplyQuestion(callback, domain.NewID(), r.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: answers}); err != nil {
							return err
						}
					case domain.GrokPlanApproval:
						if r.Plan.Validate(string(turnFixtureSession)) != nil || tools[r.Plan.WriteToolID].Grok.Content != r.Plan.Content {
							t.Fatal("Plan lost original content/revision")
						}
						if _, err := api.ReplyPlan(callback, domain.NewID(), r.ArrivalID, PlanApproved); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if profile == "write-reject" {
				if domain.SafeError(err).Code != domain.Canceled {
					t.Fatal(err, logs.String())
				}
			} else if err != nil {
				t.Fatal(err, logs.String())
			}
			if !completed || claims != len(offered) {
				t.Fatal("reply count or root terminal changed", claims, len(offered))
			}
			if profile == "input-valid" {
				if _, err := api.CompletedTextScope(ctx); err != nil {
					t.Fatal("plain text lost original closure authority", err)
				}
			} else {
				if _, err := api.CompletedTextScope(ctx); err == nil {
					t.Fatal("rich input acquired plain history authority")
				}
				terminal, err := api.ClosePublic(ctx)
				if err != nil {
					t.Fatal(err, logs.String())
				}
				if terminal.Validate(string(turnFixtureSession)) != nil || terminal.PermissionRejected != (profile == "write-reject") || terminal.Mode != mode {
					t.Fatal("rich completion lost independent original facts")
				}
				replay, err := api.ClosePublic(ctx)
				if err != nil || !reflect.DeepEqual(replay, terminal) || claims != len(offered) {
					t.Fatal("cleanup receipt replay repeated original reply")
				}
			}
			for _, arrival := range offered {
				if _, err := api.ReplyFilePermission(context.Background(), domain.NewID(), arrival, AllowFileOnce); err == nil {
					t.Fatal("original arrival acquired repeated native reply")
				}
			}
		})
	}
}

func TestPublicGrokPreservesOriginalPlainTextStop(t *testing.T) {
	for _, mode := range []string{"stop-valid", "stop-completion-race"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := OpenOwnedPublicAPI(context.Background(), config, PlanningRecorders{Creation: func(context.Context, CreationClaim) error { return nil }, Mode: func(context.Context, ModeClaim) error { return nil }, Input: func(context.Context, InputClaim) error { return nil }, File: func(context.Context, FilePermissionClaim) error {
				t.Fatal("text Stop claimed a file reply")
				return nil
			}, Question: func(context.Context, QuestionClaim) error { t.Fatal("text Stop claimed a question reply"); return nil }, Plan: func(context.Context, PlanClaim) error { t.Fatal("text Stop claimed a Plan reply"); return nil }}, func(context.Context, ClosureClaim) error { t.Fatal("Stop acquired native close history"); return nil }, func(context.Context, StopClaim) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			settled := false
			_, err = api.RunPublic(ctx, domain.NewID(), "Original input.", func(ctx context.Context, v InputObservation) error {
				if v.Kind == InputText {
					_, err := api.StopText(ctx, domain.NewID())
					return err
				}
				if v.Kind == StopSettled {
					settled = v.Stop != nil && v.Stop.Idle && v.Stop.CleanupJoined
				}
				return nil
			})
			if !settled || mode == "stop-valid" && domain.SafeError(err).Code != domain.Canceled || mode == "stop-completion-race" && err != nil {
				t.Fatal("public profile lost original Stop settlement", err, logs.String())
			}
			if _, err := api.ObserveStoppedText(context.Background()); err != nil {
				t.Fatal("public profile lost original stopped comparison", err)
			}
			if _, err := api.ClosePublic(context.Background()); err == nil {
				t.Fatal("plain Stop acquired rich terminal authority")
			}
		})
	}
}
