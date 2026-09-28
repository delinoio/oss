package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func fixtureQuestionRows() []fixtureFileRow {
	var rows []fixtureFileRow
	_ = json.Unmarshal(questionToolFixture, &rows)
	return rows
}

func fixtureQuestionInput(workspace, mode string, request domain.ID, input string) {
	fixtureNotify("_x.ai/queue/changed", queueFixture(input, 0))
	fixtureNotify("_x.ai/queue/changed", queueFixture(input, 1))
	for i, row := range fixtureQuestionRows() {
		if i == 1 {
			fixtureResponseCounters()
		}
		if i == 4 && mode == "question-missing-automatic-resolution" {
			continue
		}
		if row.Kind == nativewire.ServerRequest {
			if mode == "question-foreign-proposal" {
				row.Params = []byte(strings.ReplaceAll(string(row.Params), "Which original fixture color?", "A different question?"))
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": "original-question", "method": row.Method, "params": row.Params})
			if mode == "question-unclaimed-resolution" {
				fixtureQuestionTerminal(workspace, mode, request)
			}
			return
		}
		fixtureNotify(row.Method, row.Params)
	}
}

func fixtureQuestionReply(root, workspace, mode string, request domain.ID, raw []byte) {
	var response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Outcome     QuestionOutcome               `json:"outcome"`
			Answers     map[string]string             `json:"answers"`
			Annotations map[string]QuestionAnnotation `json:"annotations"`
		} `json:"result"`
	}
	if decode(raw, &response) != nil || response.JSONRPC != "2.0" || response.ID != "original-question" || response.Result.Outcome != QuestionAccepted || len(response.Result.Answers) != 1 || response.Result.Answers["Which original fixture color?"] != "Blue" || len(response.Result.Annotations) != 0 {
		os.Exit(72)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "question-reply-started"), []byte("started"), 0600)
	if mode == "question-exit-after-reply" {
		os.Exit(0)
	}
	fixtureQuestionTerminal(workspace, mode, request)
}

func fixtureQuestionTerminal(workspace, mode string, request domain.ID) {
	rows := fixtureQuestionRows()
	resolution, completion := rows[len(rows)-2], rows[len(rows)-1]
	if mode != "question-missing-resolution" {
		fixtureNotify(resolution.Method, resolution.Params)
	}
	if mode == "question-foreign-tool" {
		completion.Params = []byte(strings.ReplaceAll(string(completion.Params), "call_delidev_read", "foreign-tool"))
	}
	if mode != "question-missing-tool" {
		fixtureNotify(completion.Method, completion.Params)
	}
	fixtureResponseCounters()
	result, usage := twoResponseResult()
	ended := fixtureObject(turnCompletedFixture)
	ended["update"].(map[string]any)["usage"] = usage
	ended["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-20"
	turn, _ := json.Marshal(ended)
	if mode == "question-conflicting-result" {
		result["_meta"].(map[string]any)["inputTokens"] = 99
	}
	if mode != "question-missing-clear" {
		fixtureNotify("_x.ai/queue/changed", queueFixture("Original input.", 2))
	}
	fixtureNotify("_x.ai/session_notification", turn)
	fixtureNotify("_x.ai/session/prompt_complete", promptCompletedFixture)
	if mode != "question-missing-rpc" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request, "result": result})
	}
}

func TestQuestionControllerOriginalClaimsAndUncertainty(t *testing.T) {
	for _, mode := range []string{"question-valid", "question-answer-mutation", "question-missing-automatic-resolution", "question-missing-resolution", "question-missing-tool", "question-missing-clear", "question-missing-rpc", "question-claim-failure", "question-publication-failure", "question-foreign-proposal", "question-foreign-tool", "question-unclaimed-resolution", "question-exit-after-reply", "question-conflicting-result"} {
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var timer *time.Timer
			timed := make(chan struct{})
			defer func() {
				if timer != nil && !timer.Stop() {
					<-timed
				}
			}()
			var arrival domain.ID
			claims := 0
			completed := false
			_, err = api.RunQuestions(ctx, input, "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
				if v.QuestionOffer != nil {
					arrival = v.QuestionOffer.ArrivalID
					if mode == "question-unclaimed-resolution" {
						return nil
					}
					answer := QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"Which original fixture color?": "Blue"}}
					for _, id := range []domain.ID{creation, product, input, config.Probe.Process.OwnerID, arrival, turnFixtureSession} {
						if _, e := api.ReplyQuestion(callback, id, arrival, answer, func(context.Context, QuestionClaim) error { t.Error("reused identity claimed question"); return nil }); e == nil {
							t.Fatal("prior identity acquired response")
						}
					}
					if _, e := api.ReplyQuestion(callback, domain.NewID(), arrival, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"foreign": "Blue"}}, func(context.Context, QuestionClaim) error { t.Error("foreign answer claimed"); return nil }); e == nil {
						t.Fatal("foreign answer sent")
					}
					delivery, e := api.ReplyQuestion(callback, domain.NewID(), arrival, answer, func(_ context.Context, c QuestionClaim) error {
						claims++
						if c.Validate() != nil || c.InputRequestID != input || c.NativePromptID != turnFixturePrompt {
							t.Error("foreign question claim")
						}
						if mode == "question-answer-mutation" {
							answer.Answers["Which original fixture color?"] = "changed after original claim"
						}
						if mode == "question-claim-failure" {
							return context.Canceled
						}
						return nil
					})
					if mode == "question-claim-failure" {
						if e == nil || delivery.Claimed || delivery.Attempted {
							t.Error("failed persistence sent a question reply")
						}
						return e
					}
					if e != nil {
						return e
					}
					if !delivery.Delivered || delivery.Resolved || delivery.ToolPhase != "" {
						t.Fatal("delivery became native question completion")
					}
					if strings.HasPrefix(mode, "question-missing-") {
						timer = time.AfterFunc(time.Second, func() { defer close(timed); cancel() })
					}
					if mode == "question-publication-failure" {
						return context.Canceled
					}
				}
				if v.Kind == InputCompleted {
					completed = true
				}
				return nil
			})
			if mode == "question-valid" || mode == "question-answer-mutation" {
				if err != nil || !completed {
					t.Fatal("original question did not complete", err)
				}
			} else if err == nil || completed {
				t.Fatal("uncertain question completed", err)
			}
			if mode != "question-unclaimed-resolution" && mode != "question-foreign-proposal" && mode != "question-missing-automatic-resolution" && claims != 1 {
				t.Fatal("question claim count changed", claims)
			}
			if mode == "question-claim-failure" {
				if _, e := os.Stat(filepath.Join(filepath.Dir(config.Probe.Home), "tmp", "question-reply-started")); !os.IsNotExist(e) {
					t.Fatal("failed question claim reached native input")
				}
			}
			if _, e := api.ReplyQuestion(context.Background(), domain.NewID(), arrival, QuestionAnswer{Outcome: QuestionCancelled}, func(context.Context, QuestionClaim) error { t.Error("uncertain question replayed"); return nil }); e == nil {
				t.Fatal("question reply acquired replay authority")
			}
			if api.completedText != nil {
				t.Fatal("question acquired plain-text history")
			}
		})
	}
}
func TestOriginalQuestionReplyIsIndependentOfBlockedPublication(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "question-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	offers := make(chan QuestionOffer, 1)
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := api.RunQuestions(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
			if v.QuestionOffer != nil {
				offers <- *v.QuestionOffer
				select {
				case <-release:
				case <-callback.Done():
					return callback.Err()
				}
			}
			return nil
		})
		finished <- err
	}()
	var offer QuestionOffer
	select {
	case offer = <-offers:
	case <-ctx.Done():
		t.Fatal("proposal not published")
	}
	var wg sync.WaitGroup
	results := make(chan QuestionDelivery, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _ := api.ReplyQuestion(ctx, domain.NewID(), offer.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"Which original fixture color?": "Blue"}}, func(context.Context, QuestionClaim) error { return nil })
			results <- v
		}()
	}
	wg.Wait()
	close(results)
	delivered := 0
	for v := range results {
		if v.Delivered {
			delivered++
			if v.Resolved {
				t.Fatal("blocked publication granted native resolution")
			}
		}
	}
	close(release)
	if err := <-finished; err != nil || delivered != 1 {
		t.Fatal("concurrent original responses were not single-use", delivered, err)
	}
}

func TestOriginalQuestionReplyClaimJoinsNativeLifetimeLoss(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "question-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	offers := make(chan QuestionOffer, 1)
	inputDone := make(chan error, 1)
	go func() {
		_, err := api.RunQuestions(ctx, domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
			if v.QuestionOffer != nil {
				offers <- *v.QuestionOffer
				<-callback.Done()
				return callback.Err()
			}
			return nil
		})
		inputDone <- err
	}()
	var offer QuestionOffer
	select {
	case offer = <-offers:
	case <-ctx.Done():
		t.Fatal("original permission not observed")
	}
	claimEntered := make(chan struct{})
	replyDone := make(chan error, 1)
	go func() {
		_, err := api.ReplyQuestion(ctx, domain.NewID(), offer.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"Which original fixture color?": "Blue"}}, func(record context.Context, _ QuestionClaim) error {
			close(claimEntered)
			<-record.Done()
			return record.Err()
		})
		replyDone <- err
	}()
	select {
	case <-claimEntered:
	case <-ctx.Done():
		t.Fatal("original reply claim not entered")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-replyDone:
		if err == nil {
			t.Fatal("failed record sent original response")
		}
	case <-ctx.Done():
		t.Fatal("native lifetime did not cancel original claim")
	}
	select {
	case err := <-inputDone:
		if err == nil {
			t.Fatal("native lifetime loss completed input")
		}
	case <-ctx.Done():
		t.Fatal("input did not join response and publication")
	}
	v, err := api.textControl().inspectQuestionReply(offer.ArrivalID)
	if err != nil || v.Claimed || v.Attempted || v.Delivered || v.ProblemCode != domain.RecoveryRequired {
		t.Fatal("native lifetime loss fabricated reply acceptance", err)
	}
	if _, err := api.ReplyQuestion(ctx, domain.NewID(), offer.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"Which original fixture color?": "Blue"}}, func(context.Context, QuestionClaim) error { t.Error("lost original claim retried"); return nil }); err == nil {
		t.Fatal("lost original reply reopened")
	}
}
