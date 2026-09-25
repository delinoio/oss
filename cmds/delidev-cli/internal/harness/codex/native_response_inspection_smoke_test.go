package codex

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Withhold consumption of the actual raw answer event while native history
// becomes available. This establishes the pinned API's recovery limit without
// dropping a user event, touching user accounts or simulating native acceptance.
func TestManualNativeResponseInspectionRequiresOriginalLiveProof(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed native harness with local scripted responses only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider, requests, received := nativeQuestionProvider(t)
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := c.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.PlanMode, Prompt: "Return only the scripted local question fixture."})
	if err != nil {
		t.Fatal(err)
	}
	var arrival domain.ID
	response := domain.NewID()
	for {
		event, err := c.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == InteractionRequestedEvent {
			if arrival != "" || event.Interaction.Kind != UserInputInteraction || event.TurnID != turn.TurnID {
				t.Fatal("unexpected native question")
			}
			arrival = event.Interaction.ID
			if _, err := c.AnswerQuestions(ctx, response, arrival, turn.TurnID, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}}); err != nil {
				t.Fatal(err)
			}
		}
		if event.Kind == QuestionAcceptedEvent || event.Kind == TurnCompletedEvent {
			t.Fatal("native response ordering changed before closure")
		}
		if event.Kind == InteractionClosedEvent {
			break
		}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !received.Load() {
		select {
		case <-ctx.Done():
			t.Fatal("scripted native follow-up never received the answer")
		case <-ticker.C:
		}
	}
	status, err := c.InspectInteractionResponse(ctx, response, arrival)
	assertCode(t, err, domain.RecoveryRequired)
	if status.Accepted || status.Delivery != QuestionTransmitted || !c.execution.paused || c.problem == nil {
		t.Fatal("history/closure invented exact response evidence")
	}
	prior := c.problem
	accepted := false
	for {
		event, err := c.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == QuestionAcceptedEvent {
			if accepted || event.InteractionState.ID != arrival || event.InteractionState.ResponseID != response {
				t.Fatal("inspection consumed or replaced original evidence")
			}
			accepted = true
		}
		if event.Kind == TurnCompletedEvent {
			break
		}
	}
	status, err = c.InspectInteractionResponse(ctx, response, arrival)
	if err != nil || !accepted || !status.Accepted || c.problem != prior || !c.execution.paused || requests.Load() != 2 {
		t.Fatal("late original proof lost acceptance, replayed input or cleared recovery", err)
	}
	t.Logf("Codex %s: history/closure remained uncertain; original queued answer proof confirmed once; pause preserved; no external account", SupportedVersion)
}
