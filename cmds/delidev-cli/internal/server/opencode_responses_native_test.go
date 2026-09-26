package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestManualNativeOpenCodeDeliversOriginalResponses(t *testing.T) {
	for _, publication := range []nativeOpenCodePublication{nativePermissionResponsePublication, nativeQuestionResponsePublication, nativePermissionResponseLostAckPublication, nativeQuestionResponseLostAckPublication} {
		t.Run(fmt.Sprint(publication), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, publication) })
	}
}

func respondOriginalOpenCodeFixture(t *testing.T, ctx context.Context, f *publicationFixture, publisher *worker.OpenCodeEventPublisher, row store.Record, original domain.ExecutionInteraction, lostAck bool) {
	t.Helper()
	client := delidevv1connect.NewInteractionServiceClient(f.http.Client(), f.http.URL)
	responseID := domain.NewID()
	meta := &pb.Mutation{RequestId: string(responseID), Id: string(row.ID), ExpectedRevision: row.Revision}
	var err error
	if original.Type == domain.UserQuestionInteraction {
		raw, _ := json.Marshal(domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: [][]string{{"Second", "First"}}}})
		response, e := client.RespondQuestion(ctx, ownerRequest(f.service.Identity, &pb.RespondQuestionRequest{Mutation: meta, ResponseJson: raw}))
		if e != nil {
			t.Fatal(e)
		}
		control := &pb.QuestionResponseControl{JobId: string(f.job), InteractionId: string(row.ID), ResponseId: string(responseID), Revision: response.Msg.Interaction.Revision}
		err = publisher.DeliverQuestionResponse(ctx, ctx, control)
	} else {
		raw, _ := json.Marshal(domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}})
		response, e := client.RespondApproval(ctx, ownerRequest(f.service.Identity, &pb.RespondApprovalRequest{Mutation: meta, ResponseJson: raw}))
		if e != nil {
			t.Fatal(e)
		}
		control := &pb.ApprovalResponseControl{JobId: string(f.job), InteractionId: string(row.ID), ResponseId: string(responseID), Revision: response.Msg.Interaction.Revision}
		err = publisher.DeliverApprovalResponse(ctx, ctx, control)
	}
	if (err != nil) != lostAck {
		t.Fatalf("original response delivery uncertainty changed: %v", err)
	}
	_, retained := readPublishedInteraction(t, f, row.ID)
	if retained.Closure != domain.InteractionOpen || retained.Response != nil && (retained.Response.State != domain.QuestionResponseTransmitted || retained.Response.Acceptance != nil) || retained.ApprovalResponse != nil && (retained.ApprovalResponse.State != domain.ApprovalResponseTransmitted || retained.ApprovalResponse.Acceptance != nil) {
		t.Fatal("HTTP delivery fabricated native acceptance or request closure")
	}
}

func verifyOriginalOpenCodeResponse(t *testing.T, ctx context.Context, f *publicationFixture, path string) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("original response lost its interaction")
	}
	_, value := readPublishedInteraction(t, f, rows[0].ID)
	var evidence *domain.OpenCodeReplyEvidence
	var responseID domain.ID
	if value.Type == domain.UserQuestionInteraction {
		if value.Response == nil || value.Response.State != domain.QuestionResponseAccepted || value.Response.Acceptance == nil {
			t.Fatal("native question reply was not accepted")
		}
		evidence, responseID = value.Response.Acceptance.OpenCode, value.Response.ID
	} else {
		if value.ApprovalResponse == nil || value.ApprovalResponse.State != domain.ApprovalResponseAccepted || value.ApprovalResponse.Acceptance == nil {
			t.Fatal("native permission reply was not accepted")
		}
		evidence, responseID = value.ApprovalResponse.Acceptance.OpenCode, value.ApprovalResponse.ID
	}
	if value.Closure != domain.InteractionNativeClosed || evidence == nil || !evidence.HTTPAccepted || evidence.ProposalEventID != value.OpenCode.NativeEventID || evidence.NativeRequestID != value.NativeRequestID.Text {
		t.Fatal("native reply lost independent acceptance or closure")
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	var journal struct {
		Claims []opencode.SessionClaim `json:"claims"`
	}
	if err != nil || json.Unmarshal(raw, &journal) != nil || len(journal.Claims) != 3 || journal.Claims[2].RequestID != responseID || journal.Claims[2].BodyDigest != evidence.BodyDigest || journal.Claims[2].InteractionID != value.NativeRequestID.Text || journal.Claims[2].ArrivalID != value.OpenCode.NativeEventID || journal.Claims[2].CallID != value.OpenCode.CallID {
		t.Fatal("response did not retain exactly one original native claim")
	}
}
