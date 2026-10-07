package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestPRRemediationHistoryAndResumeKeepOriginalAttemptsWithoutDispatch(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Remediation history")), "private-history-fixture-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	remoteCalls := 0
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		remoteCalls++
		return retainedFeedbackObservation(q), nil
	})
	collected, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17"}))
	if err != nil {
		t.Fatal(err)
	}
	setID := domain.ID(collected.Msg.ProblemSet.Id)
	var set domain.PRProblemSet
	if err := domain.Decode(collected.Msg.ProblemSet.DocumentJson, &set); err != nil {
		t.Fatal(err)
	}
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	reserve := func(cancel bool) store.Record {
		t.Helper()
		var saved store.Record
		_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.reserve-history", nil, func(tx *store.Tx) (any, error) {
			r, _, err := tx.GetPRProblemSet(setID)
			if err != nil {
				return nil, err
			}
			problems, _, err := tx.ListPRProblems(setID, "", 1)
			if err != nil {
				return nil, err
			}
			p, err := store.Decode[domain.PRProblem](problems[0])
			if err != nil {
				return nil, err
			}
			saved, err = tx.ReservePRRemediation(setID, r.Revision, domain.PRRemediationManual, domain.DefaultRemediationPolicy(), []domain.PRRemediationProblemRef{{ID: problems[0].ID, ContentVersion: p.ContentVersion}})
			if err != nil {
				return nil, err
			}
			if cancel {
				saved, err = tx.CancelPRRemediation(saved.ID, saved.Revision)
			}
			return nil, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	first, second := reserve(true), reserve(true)
	request := &pb.ListPullRequestRemediationAttemptsRequest{RemoteRepositoryId: set.Target.RemoteRepositoryID, PullRequestId: set.Target.PullRequestID, PageSize: 1}
	page, err := f.client.ListPullRequestRemediationAttempts(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || len(page.Msg.Attempts) != 1 || page.Msg.Attempts[0].Id != string(second.ID) || page.Msg.NextPageToken == "" {
		t.Fatal("first remediation page", err)
	}
	request.PageToken = page.Msg.NextPageToken
	last, err := f.client.ListPullRequestRemediationAttempts(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || len(last.Msg.Attempts) != 1 || last.Msg.Attempts[0].Id != string(first.ID) || last.Msg.NextPageToken != "" {
		t.Fatal("next remediation page", err)
	}
	request.PageSize = 2
	if _, err := f.client.ListPullRequestRemediationAttempts(context.Background(), ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("foreign page-size cursor accepted", err)
	}
	request.PageSize = 1
	resume := &pb.ResumePullRequestRemediationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(setID), ExpectedRevision: page.Msg.ProblemSet.Revision}}
	resumed, err := f.client.ResumePullRequestRemediation(context.Background(), ownerRequest(f.service.Identity, resume))
	if err != nil {
		t.Fatal(err)
	}
	var resumedSet domain.PRProblemSet
	if domain.Decode(resumed.Msg.ProblemSet.DocumentJson, &resumedSet) != nil || resumedSet.Remediation == nil || resumedSet.Remediation.Sequence != 2 || resumedSet.Remediation.LastResume == nil || resumedSet.Remediation.LastResume.RequestID != domain.ID(resume.Mutation.RequestId) {
		t.Fatal("resumption erased original attempt history")
	}
	if _, err := f.client.ListPullRequestRemediationAttempts(context.Background(), ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("changed history cursor accepted", err)
	}
	active := reserve(false)
	current, err := f.service.Store.Get(owner, domain.ProblemKind, setID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.ResumePullRequestRemediation(context.Background(), ownerRequest(f.service.Identity, &pb.ResumePullRequestRemediationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(setID), ExpectedRevision: current.Revision}})); err != nil {
		t.Fatal("active metadata blocked explicit allowance resumption", err)
	}
	// Current metadata replay may show a later active attempt. It cannot run
	// the old allowance mutation again or cancel the newly reserved owner.
	replay, err := f.client.ResumePullRequestRemediation(context.Background(), ownerRequest(f.service.Identity, resume))
	if err != nil || !replay.Msg.Replayed || domain.Decode(replay.Msg.ProblemSet.DocumentJson, &resumedSet) != nil || resumedSet.Remediation.ActiveAttemptID != active.ID || resumedSet.Remediation.Sequence != 3 {
		t.Fatal("resumption replay rewrote later work", err)
	}
	if _, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(profile.Profile)})); err != nil {
		t.Fatal(err)
	}
	f.restart()
	request.PageToken = ""
	page, err = f.client.ListPullRequestRemediationAttempts(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || len(page.Msg.Attempts) != 1 || page.Msg.Attempts[0].Id != string(active.ID) || remoteCalls != 2 {
		t.Fatal("historical reads required credentials or lost attempts", err)
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})
	if _, err := f.service.ListPullRequestRemediationAttempts(worker, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("Worker read remediation history", err)
	}
	if _, err := f.service.ResumePullRequestRemediation(worker, connect.NewRequest(resume)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("Worker resumed allowance", err)
	}
	if err := f.service.Store.Read(owner, func(tx *store.Tx) error {
		rows, err := tx.Jobs("", "", domain.JobQueued, "", store.MaxPage)
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			t.Fatal("history or resumption dispatched work")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
