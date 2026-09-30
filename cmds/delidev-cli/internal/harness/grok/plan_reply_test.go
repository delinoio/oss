package grok

import (
	"context"
	_ "embed"
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

//go:embed testdata/plan-approved.json
var approvedPlanFixture []byte

// This state exists only in the isolated test subprocess. The fixture stops at
// each original request until it receives the real controller's encoded reply.
var fixturePlanRows []fixtureFileRow
var fixturePlanCursor int

func fixturePlanningInput(root, workspace, mode string, request domain.ID, input string) {
	rel, err := historySessionPath(workspace, turnFixtureSession)
	if err != nil {
		os.Exit(81)
	}
	var rows []fixtureFileRow
	if json.Unmarshal(approvedPlanFixture, &rows) != nil {
		os.Exit(82)
	}
	fixturePlanRows = rows
	for i := range fixturePlanRows {
		var v any
		if json.Unmarshal(fixturePlanRows[i].Params, &v) != nil {
			os.Exit(83)
		}
		fixturePlanRows[i].Params, _ = json.Marshal(rewritePlanningPath(v, filepath.Join(root, "grok", rel, "plan.md")))
	}
	fixtureNotify("_x.ai/queue/changed", queueFixture(input, 0))
	fixtureNotify("_x.ai/queue/changed", queueFixture(input, 1))
	fixturePlanningAdvance(workspace, mode, request)
}

func rewritePlanningPath(v any, path string) any {
	switch value := v.(type) {
	case string:
		// The fixture also embeds a JSON-encoded tool argument string. Decode it
		// before replacing paths so Windows separators remain valid JSON escapes.
		var nested any
		if json.Unmarshal([]byte(value), &nested) == nil {
			if encoded, err := json.Marshal(rewritePlanningPath(nested, path)); err == nil {
				return string(encoded)
			}
		}
		return strings.ReplaceAll(value, "/fixture/plan.md", path)
	case map[string]any:
		for key, item := range value {
			value[key] = rewritePlanningPath(item, path)
		}
	case []any:
		for i, item := range value {
			value[i] = rewritePlanningPath(item, path)
		}
	}
	return v
}

func fixturePlanningAdvance(workspace, mode string, request domain.ID) {
	for fixturePlanCursor < len(fixturePlanRows) {
		i := fixturePlanCursor
		fixturePlanCursor++
		row := fixturePlanRows[i]
		if i == 1 || i == 8 || i == 17 || i == 21 {
			fixtureResponseCounters()
		}
		if i == 27 && mode == "planning-missing-resolution" || i == 28 && mode == "planning-missing-mode" || i == 29 && mode == "planning-missing-tool" {
			continue
		}
		if i == 29 && mode == "planning-foreign-artifact" {
			v := fixtureObject(row.Params)
			v["update"].(map[string]any)["rawOutput"].(map[string]any)["PlanReady"].(map[string]any)["plan_content"] = "Changed after original approval"
			row.Params, _ = json.Marshal(v)
		}
		if row.Kind == nativewire.ServerRequest {
			id := "original-plan"
			if row.Method == "_x.ai/ask_user_question" {
				id = "original-plan-question"
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": row.Method, "params": row.Params})
			if row.Method == "_x.ai/exit_plan_mode" && mode == "planning-unclaimed-resolution" {
				continue
			}
			return
		}
		fixtureNotify(row.Method, row.Params)
	}
	fixtureResponseCounters()
	result := fixtureObject(promptResultFixture)
	usage := result["_meta"].(map[string]any)["usage"].(map[string]any)
	for key, v := range usage {
		if n, ok := v.(float64); ok {
			usage[key] = n * 5
		}
	}
	model := usage["modelUsage"].(map[string]any)[turnFixtureModel].(map[string]any)
	for key, v := range model {
		model[key] = v.(float64) * 5
	}
	ended := fixtureObject(turnCompletedFixture)
	ended["update"].(map[string]any)["usage"] = usage
	ended["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-100"
	turn, _ := json.Marshal(ended)
	fixtureNotify("_x.ai/queue/changed", queueFixture("Original input.", 2))
	fixtureNotify("_x.ai/session_notification", turn)
	fixtureNotify("_x.ai/session/prompt_complete", promptCompletedFixture)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request, "result": result})
	if mode == "planning-public" {
		fixtureInputTail(workspace, mode, fixtureNotify)
	}
}

func fixturePlanningReply(root, workspace, mode string, request domain.ID, raw []byte) {
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	if decode(raw, &reply) != nil || reply.JSONRPC != "2.0" {
		os.Exit(84)
	}
	switch reply.ID {
	case "original-plan-question":
		var answer struct {
			Outcome     QuestionOutcome               `json:"outcome"`
			Answers     map[string]string             `json:"answers"`
			Annotations map[string]QuestionAnnotation `json:"annotations"`
		}
		if decode(reply.Result, &answer) != nil || answer.Outcome != QuestionAccepted || answer.Answers["Which original mixed option?"] != "Blue" {
			os.Exit(85)
		}
	case "original-plan":
		var body struct {
			Outcome PlanOutcome `json:"outcome"`
		}
		if decode(reply.Result, &body) != nil || body.Outcome != PlanApproved {
			os.Exit(86)
		}
		_ = os.WriteFile(filepath.Join(root, "tmp", "plan-reply-started"), []byte("started"), 0600)
		if mode == "planning-exit-after-reply" {
			os.Exit(0)
		}
	default:
		os.Exit(87)
	}
	fixturePlanningAdvance(workspace, mode, request)
}

func runFixturePlanning(ctx context.Context, api *apiConnection, input domain.ID, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	return api.runInput(ctx, input, "Original input.", func(context.Context, InputClaim) error { return nil }, func(callback context.Context, v InputObservation) error {
		if v.QuestionOffer != nil {
			_, err := api.ReplyQuestion(callback, domain.NewID(), v.QuestionOffer.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{"Which original mixed option?": "Blue"}}, func(context.Context, QuestionClaim) error { return nil })
			if err != nil {
				return err
			}
		}
		return emit(callback, v)
	}, planningInput)
}

func TestOriginalPlanControllerPreservesUncertainty(t *testing.T) {
	for _, mode := range []string{"planning-valid", "planning-claim-failure", "planning-publication-failure", "planning-missing-resolution", "planning-missing-mode", "planning-missing-tool", "planning-foreign-artifact", "planning-unclaimed-resolution", "planning-exit-after-reply"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, mode)
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(logs.String())
				}
			})
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
			var arrival domain.ID
			claims, completed := 0, false
			_, err = runFixturePlanning(ctx, api, input, func(callback context.Context, v InputObservation) error {
				if v.PlanOffer != nil {
					arrival = v.PlanOffer.ArrivalID
					if mode == "planning-unclaimed-resolution" {
						return nil
					}
					for _, id := range []domain.ID{creation, product, input, config.Probe.Process.OwnerID, arrival, turnFixtureSession} {
						if _, err := api.ReplyPlan(callback, id, arrival, PlanApproved, func(context.Context, PlanClaim) error { t.Error("reused identity claimed Plan"); return nil }); err == nil {
							t.Fatal("Plan reused original operation")
						}
					}
					v.PlanOffer.Origin.Revision = 99
					v.PlanOffer.ContentDigest = strings.Repeat("ab", 32)
					delivery, err := api.ReplyPlan(callback, domain.NewID(), arrival, PlanApproved, func(_ context.Context, c PlanClaim) error {
						claims++
						if c.Validate() != nil || c.InputRequestID != input || c.Revision != 1 {
							t.Error("Plan claim lost original immutable revision")
						}
						if mode == "planning-claim-failure" {
							return context.Canceled
						}
						return nil
					})
					if mode == "planning-claim-failure" {
						if err == nil || delivery.Claimed || delivery.Attempted {
							t.Error("uncertain claim sent Plan response")
						}
						return err
					}
					if err != nil {
						return err
					}
					if !delivery.Delivered || delivery.Resolved || delivery.ToolPhase != "" {
						t.Error("delivery fabricated native Plan completion")
					}
					if mode == "planning-publication-failure" {
						return context.Canceled
					}
				}
				if v.Kind == InputCompleted {
					completed = true
				}
				return nil
			})
			if mode == "planning-valid" {
				if err != nil || !completed {
					t.Fatal("original Plan did not complete", err)
				}
			} else if err == nil || completed {
				t.Fatal("uncertain Plan completed", err)
			}
			if mode != "planning-unclaimed-resolution" && claims != 1 {
				t.Fatal("original Plan claim count changed", claims)
			}
			if mode == "planning-claim-failure" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(config.Probe.Home), "tmp", "plan-reply-started")); !os.IsNotExist(err) {
					t.Fatal("failed Plan claim reached native")
				}
			}
			if _, err := api.ReplyPlan(context.Background(), domain.NewID(), arrival, PlanApproved, func(context.Context, PlanClaim) error { t.Error("Plan response replayed"); return nil }); err == nil {
				t.Fatal("retained Plan acquired resend authority")
			}
			if api.completedText != nil {
				t.Fatal("Plan acquired plain-text history")
			}
		})
	}
}

func TestPlanReplyEncodedResultIsObject(t *testing.T) {
	for _, outcome := range []PlanOutcome{PlanApproved, PlanCancelled, PlanAbandoned} {
		body, err := planReplyBody(outcome)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"result": body})
		var envelope struct {
			Result struct {
				Outcome PlanOutcome `json:"outcome"`
			} `json:"result"`
		}
		if err != nil || decode(raw, &envelope) != nil || envelope.Result.Outcome != outcome {
			t.Fatal("native Plan result was serialized as bytes instead of an object")
		}
	}
}

func TestOriginalPlanReplyIsIndependentOfBlockedPublication(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "planning-valid")
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
	offers := make(chan PlanOffer, 1)
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := runFixturePlanning(ctx, api, domain.NewID(), func(callback context.Context, v InputObservation) error {
			if v.PlanOffer != nil {
				offers <- *v.PlanOffer
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
	var offer PlanOffer
	select {
	case offer = <-offers:
	case <-ctx.Done():
		t.Fatal("proposal not published")
	}
	var wg sync.WaitGroup
	results := make(chan PlanDelivery, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _ := api.ReplyPlan(ctx, domain.NewID(), offer.ArrivalID, PlanApproved, func(context.Context, PlanClaim) error { return nil })
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

func TestOriginalPlanReplyClaimJoinsNativeLifetimeLoss(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "planning-valid")
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
	offers := make(chan PlanOffer, 1)
	inputDone := make(chan error, 1)
	go func() {
		_, err := runFixturePlanning(ctx, api, domain.NewID(), func(callback context.Context, v InputObservation) error {
			if v.PlanOffer != nil {
				offers <- *v.PlanOffer
				<-callback.Done()
				return callback.Err()
			}
			return nil
		})
		inputDone <- err
	}()
	var offer PlanOffer
	select {
	case offer = <-offers:
	case <-ctx.Done():
		t.Fatal("original permission not observed")
	}
	claimEntered := make(chan struct{})
	replyDone := make(chan error, 1)
	go func() {
		_, err := api.ReplyPlan(ctx, domain.NewID(), offer.ArrivalID, PlanApproved, func(record context.Context, _ PlanClaim) error {
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
	v, err := api.textControl().inspectPlanReply(offer.ArrivalID)
	if err != nil || v.Claimed || v.Attempted || v.Delivered || v.ProblemCode != domain.RecoveryRequired {
		t.Fatal("native lifetime loss fabricated reply acceptance", err)
	}
	if _, err := api.ReplyPlan(ctx, domain.NewID(), offer.ArrivalID, PlanApproved, func(context.Context, PlanClaim) error { t.Error("lost original claim retried"); return nil }); err == nil {
		t.Fatal("lost original reply reopened")
	}
}
