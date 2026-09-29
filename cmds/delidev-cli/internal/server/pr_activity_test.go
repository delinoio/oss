package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPRActivityRPCMetadataReceiptsChronologyAndReadIndependence(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Activity fixture")), "activity-private-pat-sentinel")
	repo := f.repository(domain.ID(profile.Profile.Id))
	calls := 0
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		calls++
		return retainedFeedbackObservation(q), nil
	})
	request := &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17"}
	collected, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	var set domain.PRProblemSet
	if domain.Decode(collected.Msg.ProblemSet.DocumentJson, &set) != nil {
		t.Fatal("set")
	}
	problems, err := f.client.ListPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, &pb.ListPullRequestProblemsRequest{RemoteRepositoryId: set.Target.RemoteRepositoryID, PullRequestId: set.Target.PullRequestID}))
	if err != nil {
		t.Fatal(err)
	}
	p := problems.Msg.Problems[0]
	var original domain.PRProblem
	if domain.Decode(p.DocumentJson, &original) != nil {
		t.Fatal("problem")
	}
	dismiss := &pb.DismissPullRequestProblemRequest{Mutation: acctMutation(p, domain.NewID()), ContentVersion: original.ContentVersion}
	dismissed, err := f.client.DismissPullRequestProblem(context.Background(), ownerRequest(f.service.Identity, dismiss))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.DismissPullRequestProblem(context.Background(), ownerRequest(f.service.Identity, dismiss)); err != nil {
		t.Fatal(err)
	}
	if replay, err := f.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.service.Identity, request)); err != nil || !replay.Msg.Replayed {
		t.Fatal("collection replay", err)
	}
	client := delidevv1connect.NewActivityServiceClient(f.httpServer.Client(), f.url)
	query := &pb.ListActivityRequest{PageSize: 1}
	var entries []*pb.ActivityEntry
	for {
		page, err := client.ListActivity(context.Background(), ownerRequest(f.service.Identity, query))
		if err != nil || len(page.Msg.Entries) != 1 || len(page.Msg.Capabilities) != 1 || page.Msg.Capabilities[0] != pb.ActivityCapability_ACTIVITY_CAPABILITY_PR_HANDLING_V1 {
			t.Fatal("activity page", err)
		}
		entries = append(entries, page.Msg.Entries...)
		query.PageToken = page.Msg.NextPageToken
		if query.PageToken == "" {
			break
		}
	}
	if len(entries) != 3 || calls != 2 {
		t.Fatal("activity replay/reads duplicated events or queried provider", len(entries), calls)
	}
	d := entries[0]
	if d.Kind != pb.ActivityKind_ACTIVITY_KIND_PR_PROBLEM_DISMISSED || d.PullRequest == nil || d.PullRequest.SourceId != p.Id || d.SourceRevision != dismissed.Msg.Problem.Revision || d.PullRequest.RequestId != dismiss.Mutation.RequestId || d.PullRequest.ActorType != pb.ActivityPRActorType_ACTIVITY_PR_ACTOR_TYPE_OWNER || len(d.PullRequest.Problems) != 1 || d.PullRequest.Problems[0].ContentVersion != original.ContentVersion || d.SessionId != "" || d.ExecutionId != "" {
		t.Fatal("dismissal lost original metadata")
	}
	for _, entry := range entries {
		raw, _ := protojson.Marshal(entry)
		for _, private := range []string{"activity-private-pat-sentinel", original.Feedback.Body, set.Target.Title} {
			if private != "" && strings.Contains(string(raw), private) {
				t.Fatal("activity leaked source content")
			}
		}
	}
	current, err := f.service.Store.Get(context.Background(), domain.ProblemKind, domain.ID(p.Id))
	if err != nil || current.Revision != dismissed.Msg.Problem.Revision {
		t.Fatal("activity read changed local handling", err)
	}
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.activity-attempt", nil, func(tx *store.Tx) (any, error) {
		r, _, err := tx.GetPRProblemSet(domain.ID(collected.Msg.ProblemSet.Id))
		if err != nil {
			return nil, err
		}
		other := problems.Msg.Problems[1]
		var v domain.PRProblem
		if domain.Decode(other.DocumentJson, &v) != nil {
			t.Fatal("other problem")
		}
		return tx.ReservePRRemediation(r.ID, r.Revision, domain.PRRemediationManual, domain.DefaultRemediationPolicy(), []domain.PRRemediationProblemRef{{ID: domain.ID(other.Id), ContentVersion: v.ContentVersion}})
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListActivity(context.Background(), ownerRequest(f.service.Identity, &pb.ListActivityRequest{}))
	if err != nil || len(page.Msg.Entries) != 4 || page.Msg.Entries[0].PullRequest.AttemptState != pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_RESERVED || page.Msg.Entries[0].PullRequest.Mode != pb.ActivityPRMode_ACTIVITY_PR_MODE_MANUAL {
		t.Fatal("attempt projection", err)
	}
	for _, kind := range []pb.ActivityKind{pb.ActivityKind_ACTIVITY_KIND_PR_PROBLEM_OBSERVED, pb.ActivityKind_ACTIVITY_KIND_PR_PROBLEM_DISMISSED} {
		found := false
		for _, entry := range page.Msg.Entries {
			found = found || entry.Kind == kind
		}
		if !found {
			t.Fatal("later attempt erased earlier activity", kind)
		}
	}
	var verified store.Record
	_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.explicit-handling-verification", nil, func(tx *store.Tx) (any, error) {
		// This is controlled explicit-verifier evidence, not a native remediation
		// acceptance claim. No production verifier or inferred-success path exists.
		var err error
		verified, err = tx.RetainPRHandlingVerification(domain.ID(collected.Msg.ProblemSet.Id), []domain.PRRemediationProblemRef{{ID: domain.ID(p.Id), ContentVersion: original.ContentVersion}}, strings.Repeat("e", 64))
		return verified, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the fixture source's original verification time; it is not derived
	// from a reservation, later read, or successful native execution.
	page, err = client.ListActivity(context.Background(), ownerRequest(f.service.Identity, &pb.ListActivityRequest{}))
	if err != nil {
		t.Fatal("verification source", err)
	}
	found := false
	for _, entry := range page.Msg.Entries {
		if entry.Id == string(verified.ID) {
			found = entry.Kind == pb.ActivityKind_ACTIVITY_KIND_PR_VERIFIED_HANDLED && entry.PullRequest.VerificationId == string(verified.ID) && entry.PullRequest.Problems[0].ContentVersion == original.ContentVersion
		}
	}
	if !found {
		t.Fatal("explicit verification was confused with dismissal or attempt success")
	}
	_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.repeat-verifier-proof", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.RetainPRHandlingVerification(domain.ID(collected.Msg.ProblemSet.Id), []domain.PRRemediationProblemRef{{ID: domain.ID(p.Id), ContentVersion: original.ContentVersion}}, strings.Repeat("e", 64))
		if err == nil && row.ID != verified.ID {
			t.Fatal("verification replay replaced original identity")
		}
		return row, err
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err = client.ListActivity(context.Background(), ownerRequest(f.service.Identity, &pb.ListActivityRequest{}))
	if err != nil || len(page.Msg.Entries) != 5 {
		t.Fatal("verification proof duplicated activity", err)
	}
	raw, _ := protojson.Marshal(page.Msg)
	if strings.Contains(string(raw), strings.Repeat("e", 64)) {
		t.Fatal("private verifier proof commitment entered activity")
	}
	err = f.service.Store.Read(owner, func(tx *store.Tx) error {
		proof, err := store.Decode[domain.PRHandlingVerification](verified)
		if err != nil {
			return err
		}
		v := domain.PRActivity{Version: 1, Type: domain.PRActivityRecord, Action: domain.PRActivityVerifiedHandled, SourceID: domain.ID(p.Id), VerificationID: domain.ID(p.Id), SourceRevision: 1, SetID: proof.SetID, RemoteRepositoryID: set.Target.RemoteRepositoryID, PullRequestID: set.Target.PullRequestID, Number: set.Target.Number, Owner: set.Target.Owner, Name: set.Target.Name, Problems: proof.Problems, Actor: proof.Actor}
		if v.Validate() != nil {
			t.Fatal("forged verification fixture must otherwise be structurally valid")
		}
		body, _ := json.Marshal(v)
		fake := store.Record{ID: domain.ID(p.Id), Kind: domain.ProblemKind, Revision: 1, Data: body}
		if err := validatePRActivitySource(tx, fake, v); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("ordinary activity could pretend to be dedicated verification", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
