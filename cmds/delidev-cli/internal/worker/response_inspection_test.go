package worker

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestResponseControllersInspectUncertaintyWithoutReplayingOrReplacingDelivery(t *testing.T) {
	for _, approval := range []bool{false, true} {
		for _, change := range []string{"accepted", "unconfirmed", "failed", "response", "arrival", "turn", "item", "delivery", "evidence"} {
			name := "question/"
			if approval {
				name = "approval/"
			}
			t.Run(name+change, func(t *testing.T) {
				inspect := func(status codex.InteractionStatus) (codex.InteractionStatus, error) {
					switch change {
					case "unconfirmed":
						status.Accepted = false
					case "failed":
						return status, publicationUncertain()
					case "response":
						status.ResponseID = domain.NewID()
					case "arrival":
						status.ID = domain.NewID()
					case "turn":
						status.TurnID = domain.NewID()
					case "item":
						status.ItemID = "foreign"
					case "delivery":
						status.Delivery = codex.QuestionTransmitted
					case "evidence":
						status.ApprovalEvidence = codex.PermissionOutputEvidence
					}
					return status, nil
				}
				var run func() error
				var check func()
				if approval {
					f := newApprovalControllerFixture(t, "uncertain")
					f.inspection = inspect
					run = func() error {
						return f.mapper.deliverApprovalResponse(context.Background(), context.Background(), f.control, f)
					}
					check = func() {
						if f.inspections != 1 || f.claims != 1 || f.sends != 1 || f.publications != 1 || f.readJournal(responseObserved).Delivery != domain.ApprovalDeliveryUncertain {
							t.Fatal("inspection repeated side effects or replaced original uncertain delivery")
						}
					}
				} else {
					f := newQuestionControllerFixture(t, "uncertain")
					f.inspection = inspect
					run = func() error {
						return f.mapper.deliverQuestionResponse(context.Background(), context.Background(), f.control, f)
					}
					check = func() {
						if f.inspections != 1 || f.claims != 1 || f.sends != 1 || f.publications != 1 || f.readJournal(responseObserved).Delivery != domain.QuestionDeliveryUncertain {
							t.Fatal("inspection repeated side effects or replaced original uncertain delivery")
						}
					}
				}
				if err := run(); (err == nil) != (change == "accepted") {
					t.Fatal("unowned/missing proof escaped recovery or queued live proof was canceled", err)
				}
				check()
				if err := run(); err == nil {
					t.Fatal("inspection authorized another response send")
				}
				check()
			})
		}
	}
}

func TestResponseInspectionReleasesPublicationLockForOriginalEvidence(t *testing.T) {
	f := newQuestionControllerFixture(t, "uncertain")
	f.inspection = func(status codex.InteractionStatus) (codex.InteractionStatus, error) {
		if f.publications != 1 {
			t.Fatal("inspection preceded durable transport observation")
		}
		done := make(chan error, 1)
		go func() {
			_, err := f.mapper.PublishCore(context.Background(), codex.Event{Kind: codex.QuestionAcceptedEvent, ThreadID: f.mapper.thread, TurnID: f.mapper.turn, ItemID: status.ItemID, Correlated: true, InteractionState: &status})
			done <- err
		}()
		select {
		case err := <-done:
			return status, err
		case <-time.After(2 * time.Second):
			return status, publicationUncertain()
		}
	}
	if err := f.mapper.deliverQuestionResponse(context.Background(), context.Background(), f.control, f); err != nil {
		t.Fatal("inspection blocked the normal original acceptance publication", err)
	}
	if f.inspections != 1 || f.publications != 2 || f.sends != 1 || len(f.acceptanceRequests) != 1 {
		t.Fatal("concurrent inspection duplicated publication or native send")
	}
}

func TestResponseInspectionLeavesExactAcceptanceInOriginalDurableOutbox(t *testing.T) {
	f := newQuestionControllerFixture(t, "uncertain")
	var proof codex.InteractionStatus
	f.inspection = func(status codex.InteractionStatus) (codex.InteractionStatus, error) {
		proof = status
		return status, nil
	}
	if err := f.mapper.deliverQuestionResponse(context.Background(), context.Background(), f.control, f); err != nil {
		t.Fatal(err)
	}
	// This is the queued original event that observed the exact live answer,
	// not a synthetic event generated from the inspection's status snapshot.
	f.loseAcceptanceAck = true
	event := codex.Event{Kind: codex.QuestionAcceptedEvent, ThreadID: f.mapper.thread, TurnID: f.mapper.turn, ItemID: proof.ItemID, Correlated: true, InteractionState: &proof}
	handled, err := f.mapper.PublishCore(context.Background(), event)
	if !handled || err == nil || f.inspections != 1 || f.publications != 2 || f.sends != 1 {
		t.Fatal("original acceptance bypassed durable publication or repeated a send", err)
	}
	f.loseAcceptanceAck = false
	if err := f.mapper.publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.acceptanceRequests) != 2 || f.acceptanceRequests[0] != f.acceptanceRequests[1] || f.inspections != 1 || f.sends != 1 || f.readJournal(responseObserved).Delivery != domain.QuestionDeliveryUncertain {
		t.Fatal("acceptance replay replaced the receipt, transport fact or native attempt")
	}
}
