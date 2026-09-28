package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func retainedFeedbackObservation(q domain.RepositoryQuery) gh.RepositoryQueryObservation {
	v := linkObservation(q)
	if q.Operation != domain.RepositoryFeedback {
		return v
	}
	at := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	entries := []domain.PRFeedback{
		{Kind: domain.PRReviewBody, ID: "71", NodeID: "REVIEW_71", Body: "Approved original feedback", NativeState: "APPROVED", PublishedAt: at, ReviewSubmittedAt: &at, URL: v.Items[0].URL + "#pullrequestreview-71"},
		{Kind: domain.PRConversationComment, ID: "73", NodeID: "COMMENT_73", Body: "Original <script>inert</script>", PublishedAt: at, URL: v.Items[0].URL + "#issuecomment-73"},
	}
	for i := range entries {
		entries[i].ContentVersion = entries[i].Version()
	}
	v.Feedback = &domain.PullRequestFeedback{BaseSHA: v.Items[0].BaseSHA, HeadSHA: v.Items[0].HeadSHA, Entries: entries, Threads: []domain.PRFeedbackThread{}}
	return v
}

func TestPRProblemsRPCPersistsDismissesReplaysAndInvalidatesPages(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Problems")), "problem-fixture-only-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	calls := 0
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		calls++
		return retainedFeedbackObservation(q), nil
	})
	request := &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17"}
	refresh, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || calls != 2 {
		t.Fatal("collection", err, calls)
	}
	var set domain.PRProblemSet
	if domain.Decode(refresh.Msg.ProblemSet.DocumentJson, &set) != nil || set.Validate() != nil {
		t.Fatal("invalid set")
	}
	list := &pb.ListPullRequestProblemsRequest{RemoteRepositoryId: set.Target.RemoteRepositoryID, PullRequestId: set.Target.PullRequestID, PageSize: 1}
	page, err := f.client.ListPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, list))
	if err != nil || len(page.Msg.Problems) != 1 || page.Msg.NextPageToken == "" {
		t.Fatal("first page", err)
	}
	first := page.Msg.Problems[0]
	var value domain.PRProblem
	if domain.Decode(first.DocumentJson, &value) != nil {
		t.Fatal("problem")
	}
	dismissal := &pb.DismissPullRequestProblemRequest{Mutation: &pb.Mutation{Id: first.Id, ExpectedRevision: first.Revision, RequestId: string(domain.NewID())}, ContentVersion: value.ContentVersion}
	dismissed, err := f.client.DismissPullRequestProblem(context.Background(), ownerRequest(f.service.Identity, dismissal))
	if err != nil {
		t.Fatal(err)
	}
	list.PageToken = page.Msg.NextPageToken
	if _, err := f.client.ListPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, list)); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("stale page accepted", err)
	}
	list.PageToken = ""
	// A token deletion cannot delete retained evidence or repeat collection on replay.
	if _, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(profile.Profile)})); err != nil {
		t.Fatal(err)
	}
	f.restart()
	replay, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.ProblemSet.Revision <= refresh.Msg.ProblemSet.Revision || calls != 2 {
		t.Fatal("refresh replay repeated or lost handling", err)
	}
	replayDismiss, err := f.client.DismissPullRequestProblem(context.Background(), ownerRequest(f.service.Identity, dismissal))
	if err != nil || !replayDismiss.Msg.Replayed || replayDismiss.Msg.Problem.Revision != dismissed.Msg.Problem.Revision {
		t.Fatal("dismiss replay", err)
	}
	page, err = f.client.ListPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, list))
	if err != nil || len(page.Msg.Problems) != 1 {
		t.Fatal("offline history", err)
	}
	list.PageToken = page.Msg.NextPageToken
	list.PullRequestId = "99"
	if _, err := f.client.ListPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, list)); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	changed := &pb.RefreshPullRequestProblemsRequest{RequestId: request.RequestId, RepositoryId: request.RepositoryId, Number: "18"}
	if _, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed receipt accepted", err)
	}
	// Owner/client handlers reject Workers before any lookup or mutation.
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})
	if _, err := f.service.DismissPullRequestProblem(worker, connect.NewRequest(dismissal)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker dismissal", err)
	}
	if _, err := f.service.ListPullRequestProblems(worker, connect.NewRequest(list)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker history", err)
	}
	if _, err := f.service.RefreshPullRequestProblems(worker, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker collect", err)
	}
	// Receipts store references, never the retained body or token.
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, _, err := tx.GetPRProblem(domain.ID(first.Id))
		if err == nil && !strings.Contains(string(r.Data), value.Feedback.Body) {
			t.Error("original body lost")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPRProblemCollectionRejectsLateSelectionChangesAndPartialObservations(t *testing.T) {
	for _, mode := range []string{"repository", "remote-identity", "partial"} {
		t.Run(mode, func(t *testing.T) {
			f := newIntegrationFixture(t)
			profile := f.replace(profileMutation(f.save("Problems")), "problem-fixture-only-token")
			repo := f.repository(domain.ID(profile.Profile.Id))
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
				v := retainedFeedbackObservation(q)
				if q.Operation == domain.RepositoryFeedback {
					switch mode {
					case "repository":
						_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.change.repo", repo.Id, func(tx *store.Tx) (any, error) {
							row, err := tx.Get(domain.RepositoryKind, domain.ID(repo.Id))
							if err != nil {
								return nil, err
							}
							value, err := store.Decode[domain.Repository](row)
							if err != nil {
								return nil, err
							}
							value.Name = "Changed"
							return tx.Put(domain.RepositoryKind, row.ID, row.Revision, "", "", value)
						})
						if err != nil {
							t.Error(err)
						}
					case "remote-identity":
						v.Items[0].ID = "99"
					case "partial":
						v.Feedback.Entries[1].ContentVersion = strings.Repeat("0", 64)
					}
				}
				return v, nil
			})
			_, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17"}))
			if err == nil {
				t.Fatal("invalid observation persisted")
			}
			rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.ProblemKind, Limit: 50})
			if err != nil || len(rows) != 0 {
				t.Fatal("partial inventory committed", err)
			}
		})
	}
}

func TestPRProblemCollectorCannotOverwriteConcurrentDismissal(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Problems")), "problem-fixture-only-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		return retainedFeedbackObservation(q), nil
	})
	refresh := func() error {
		_, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17"}))
		return err
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	var original store.Record
	err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		v := retainedFeedbackObservation(domain.RepositoryQuery{Number: "17"})
		set, _, err := tx.FindPRProblemSet(v.Repository.Provider, v.Repository.ID, v.Items[0].ID)
		if err != nil {
			return err
		}
		rows, _, err := tx.ListPRProblems(set.ID, "", 1)
		if err == nil {
			original = rows[0]
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := store.Decode[domain.PRProblem](original)
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		if q.Operation == domain.RepositoryFeedback {
			_, err := f.service.DismissPullRequestProblem(ctx, connect.NewRequest(&pb.DismissPullRequestProblemRequest{Mutation: &pb.Mutation{Id: string(original.ID), ExpectedRevision: original.Revision, RequestId: string(domain.NewID())}, ContentVersion: value.ContentVersion}))
			if err != nil {
				t.Error(err)
			}
		}
		return retainedFeedbackObservation(q), nil
	})
	if err := refresh(); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale collector overwrote newer local handling", err)
	}
	row, err := f.service.Store.Get(ctx, domain.ProblemKind, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, _ := store.Decode[domain.PRProblem](row)
	if retained.State != domain.PRProblemDismissed {
		t.Fatal("dismissal lost")
	}
}

func TestPRProblemCollectionFamiliesAreIndependentAndRequestBound(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Independent problems")), "problem-fixture-only-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	calls := 0
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		calls++
		value := retainedFeedbackObservation(q)
		no := false
		value.Items[0].Mergeable = &no
		if q.Operation == domain.RepositoryCI {
			// No applicable rules and no native results is an observed non-trigger,
			// while an independently verified merge conflict remains collectable.
			item := value.Items[0]
			value.CI = &domain.PullRequestCI{Rules: domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: []domain.ActiveRepositoryRule{}, Digest: domain.ActiveRulesDigest(nil)}, Head: domain.CIRollup{CommitSHA: item.HeadSHA, TotalCount: "0", Contexts: []domain.CIContext{}}, NativeMergeability: "CONFLICTING"}
			value.CI.Result = value.CI.Evaluate(item)
		}
		return value, nil
	})
	original := &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17"}
	first, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, original))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []pb.PullRequestProblemCollectionKind{pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CI, pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CONFLICT} {
		request := &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17", Kind: kind}
		reply, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, request))
		if err != nil || reply.Msg.ProblemSet.Id != first.Msg.ProblemSet.Id {
			t.Fatal("kind created another PR owner", err)
		}
	}
	var set domain.PRProblemSet
	if err = f.service.Store.Read(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), func(tx *store.Tx) error {
		_, value, err := tx.GetPRProblemSet(domain.ID(first.Msg.ProblemSet.Id))
		set = value
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if set.Feedback == nil || set.CI == nil || set.CI.State != domain.CINotRequired || set.Conflict == nil || set.Conflict.State != domain.PRConflictPresent {
		t.Fatal("mixed independent prerequisites")
	}
	list, err := f.client.ListPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, &pb.ListPullRequestProblemsRequest{RemoteRepositoryId: set.Target.RemoteRepositoryID, PullRequestId: set.Target.PullRequestID, PageSize: 20}))
	if err != nil || len(list.Msg.Problems) != 3 {
		t.Fatal("lost independent feedback", err)
	}
	for _, row := range list.Msg.Problems {
		var v domain.PRProblem
		if domain.Decode(row.DocumentJson, &v) != nil || v.Validate() != nil || !v.Current {
			t.Fatal("inconsistent mixed history")
		}
	}
	count := calls
	wrongKind := &pb.RefreshPullRequestProblemsRequest{RequestId: original.RequestId, RepositoryId: repo.Id, Number: "17", Kind: pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CONFLICT}
	if _, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, wrongKind)); connect.CodeOf(err) != connect.CodeAborted || calls != count {
		t.Fatal("original request changed collection family", err)
	}
	invalid := &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17", Kind: 99}
	if _, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, invalid)); connect.CodeOf(err) != connect.CodeInvalidArgument || calls != count {
		t.Fatal("unknown enum reached GitHub", err)
	}
	// Existing omitted-kind requests remain exact replay after the new enum.
	replay, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, original))
	if err != nil || !replay.Msg.Replayed || calls != count {
		t.Fatal("legacy feedback receipt invalidated", err)
	}
}
