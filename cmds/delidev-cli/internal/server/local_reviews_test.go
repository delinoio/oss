package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type localReviewFixture struct {
	f      *firstDispatchFixture
	ctx    context.Context
	reader *connect.ServerStreamForClient[pb.WatchWorkspaceReadsResponse]
	diff   domain.WorkspaceDiff
}

func newLocalReviewFixture(t *testing.T) *localReviewFixture {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := newFirstDispatchFixtureWorkspaceProfile(t, domain.Codex, domain.ExecuteMode, executable, "", "fixture", domain.Worktree)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	reader, err := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if !reader.Receive() || !reader.Msg().Heartbeat {
		t.Fatal("reader unavailable", reader.Err())
	}
	var job domain.Job
	if err := domain.Decode(f.change.WorkspaceJob.DocumentJson, &job); err != nil {
		t.Fatal(err)
	}
	row, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(f.change.WorkspaceJob.Id))
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.Decode[domain.Job](row)
	if err != nil {
		t.Fatal(err)
	}
	var manifest workspace.Manifest
	if err := domain.Decode(job.Output, &manifest); err != nil {
		t.Fatal(err)
	}
	repo := manifest.Repositories[0]
	diff := domain.WorkspaceDiff{RepositoryID: repo.ID, Comparison: domain.DiffCreation, Path: ".", Base: domain.DiffCommit, BaseObject: repo.StartingCommit, HeadCommit: repo.StartingCommit, Patch: "diff --git a/file.txt b/file.txt\nindex abcdef0..abcdef1 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n", Untracked: []string{}}
	diff.Revision = diff.Digest()
	return &localReviewFixture{f: f, ctx: ctx, reader: reader, diff: diff}
}

// These are controlled Worker reports for database fault tests. The CLI suite
// separately proves the same path with actual Git and an ordinary Worker.
func reviewWithObservation[T any](t *testing.T, f *localReviewFixture, invoke func() (T, error), beforeReport func()) (T, error) {
	t.Helper()
	type reply struct {
		value T
		err   error
	}
	done := make(chan reply, 1)
	go func() { v, e := invoke(); done <- reply{v, e} }()
	if !f.reader.Receive() {
		t.Fatal("missing requested observation", f.reader.Err())
	}
	var read workspace.ReadRequest
	if err := domain.Decode(f.reader.Msg().RequestJson, &read); err != nil {
		t.Fatal(err)
	}
	if read.Query.RepositoryID != f.diff.RepositoryID || read.Query.Comparison != f.diff.Comparison || read.Query.Path != f.diff.Path {
		t.Fatal("unexpected comparison")
	}
	if beforeReport != nil {
		beforeReport()
	}
	raw, _ := json.Marshal(domain.WorkspaceReadResult{Diff: &f.diff})
	_, err := f.f.workerClient.ReportWorkspaceRead(f.ctx, ownerRequest(f.f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.f.machine.Id, InstanceId: f.f.workerInstance, ReadId: string(read.ID), DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		return r.value, r.err
	case <-f.ctx.Done():
		t.Fatal("review operation did not finish")
		var zero T
		return zero, f.ctx.Err()
	}
}

func createLocalReviewFixtureComment(t *testing.T, f *localReviewFixture) *pb.Resource {
	t.Helper()
	v := domain.CreateReviewComment{Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: f.diff.RepositoryID, Path: f.diff.Path, Comparison: f.diff.Comparison}, DiffRevision: f.diff.Revision, Selection: domain.ReviewSelection{Path: "file.txt", Kind: domain.ReviewLineAnchor, Side: domain.ReviewNewSide, Start: 1, End: 1}, Body: "Preserved original request"}
	raw, _ := json.Marshal(v)
	r, err := reviewWithObservation(t, f, func() (*connect.Response[pb.CreateLocalReviewCommentResponse], error) {
		return sessionClient(f.f.accountFixture).CreateLocalReviewComment(f.ctx, ownerRequest(f.f.identity, &pb.CreateLocalReviewCommentRequest{RequestId: string(domain.NewID()), SessionId: f.f.change.Session.Id, DocumentJson: raw}))
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg.Comment
}

func TestLocalReviewSubmissionRollsBackLinksQueueAndEvents(t *testing.T) {
	f := newLocalReviewFixture(t)
	comment := createLocalReviewFixtureComment(t, f)
	session := domain.ID(f.f.change.Session.Id)
	setCount := func(count uint32) {
		t.Helper()
		_, err := f.f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.review-capacity", count, func(tx *store.Tx) (any, error) {
			r, v, e := sessionRecord(tx, session)
			if e != nil {
				return nil, e
			}
			v.PendingInputs = count
			return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, v)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setCount(domain.MaxPendingInputs)
	filter := store.Filter{Kind: domain.ReviewKind, SessionID: session, Limit: 200}
	_, before, err := f.f.service.Store.Snapshot(f.ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(domain.SubmitReviewComments{Mode: domain.ExecuteMode, Comments: []domain.ReviewCommentRef{{ID: domain.ID(comment.Id), Revision: comment.Revision}}})
	request := &pb.SubmitLocalReviewRequest{RequestId: string(domain.NewID()), SessionId: string(session), DocumentJson: raw}
	invoke := func() (*connect.Response[pb.SubmitLocalReviewResponse], error) {
		return sessionClient(f.f.accountFixture).SubmitLocalReview(f.ctx, ownerRequest(f.f.identity, request))
	}
	_, err = reviewWithObservation(t, f, invoke, nil)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("queue bound ignored", err)
	}
	rows, after, err := f.f.service.Store.Snapshot(f.ctx, filter)
	if err != nil || len(rows) != 1 || before != after || rows[0].Revision != comment.Revision {
		t.Fatal("failed submission leaked records/events", err)
	}
	retained, err := store.Decode[domain.LocalReview](rows[0])
	if err != nil || retained.Comment.LastSubmissionID != "" {
		t.Fatal("failed submission retained a link", err)
	}
	setCount(1)
	accepted, err := reviewWithObservation(t, f, invoke, nil)
	if err != nil || accepted.Msg.Replayed || accepted.Msg.Submission.Id != request.RequestId {
		t.Fatal("failed request reserved or duplicated acceptance", err)
	}
}

func TestLocalReviewSubmissionRejectsConcurrentCommentEdit(t *testing.T) {
	f := newLocalReviewFixture(t)
	comment := createLocalReviewFixtureComment(t, f)
	raw, _ := json.Marshal(domain.SubmitReviewComments{Mode: domain.PlanMode, Comments: []domain.ReviewCommentRef{{ID: domain.ID(comment.Id), Revision: comment.Revision}}})
	_, err := reviewWithObservation(t, f, func() (*connect.Response[pb.SubmitLocalReviewResponse], error) {
		return sessionClient(f.f.accountFixture).SubmitLocalReview(f.ctx, ownerRequest(f.f.identity, &pb.SubmitLocalReviewRequest{RequestId: string(domain.NewID()), SessionId: f.f.change.Session.Id, DocumentJson: raw}))
	}, func() {
		_, err := sessionClient(f.f.accountFixture).EditLocalReviewComment(f.ctx, ownerRequest(f.f.identity, &pb.EditLocalReviewCommentRequest{Mutation: &pb.Mutation{Id: comment.Id, ExpectedRevision: comment.Revision, RequestId: string(domain.NewID())}, SessionId: f.f.change.Session.Id, Body: "Concurrent edited body"}))
		if err != nil {
			t.Fatal(err)
		}
	})
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("concurrent edit was submitted", err)
	}
	rows, _, err := f.f.service.Store.Snapshot(f.ctx, store.Filter{Kind: domain.ReviewKind, SessionID: domain.ID(f.f.change.Session.Id), Limit: 200})
	if err != nil || len(rows) != 1 {
		t.Fatal("partial submission", err)
	}
}

func TestLocalReviewMutationsRejectWorkerCredentials(t *testing.T) {
	f := newFirstDispatchFixture(t)
	client := sessionClient(f.accountFixture)
	ctx := context.Background()
	_, err := client.CreateLocalReviewComment(ctx, ownerRequest(f.workerIdentity, &pb.CreateLocalReviewCommentRequest{}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	_, err = client.EditLocalReviewComment(ctx, ownerRequest(f.workerIdentity, &pb.EditLocalReviewCommentRequest{}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	_, err = client.DeleteLocalReviewComment(ctx, ownerRequest(f.workerIdentity, &pb.DeleteLocalReviewCommentRequest{}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	_, err = client.SubmitLocalReview(ctx, ownerRequest(f.workerIdentity, &pb.SubmitLocalReviewRequest{}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
}

func TestLocalReviewSubmissionRechecksArchiveBeforeCommit(t *testing.T) {
	f := newLocalReviewFixture(t)
	comment := createLocalReviewFixtureComment(t, f)
	raw, _ := json.Marshal(domain.SubmitReviewComments{Mode: domain.ExecuteMode, Comments: []domain.ReviewCommentRef{{ID: domain.ID(comment.Id), Revision: comment.Revision}}})
	_, err := reviewWithObservation(t, f, func() (*connect.Response[pb.SubmitLocalReviewResponse], error) {
		return sessionClient(f.f.accountFixture).SubmitLocalReview(f.ctx, ownerRequest(f.f.identity, &pb.SubmitLocalReviewRequest{RequestId: string(domain.NewID()), SessionId: f.f.change.Session.Id, DocumentJson: raw}))
	}, func() {
		_, err := f.f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.review-archive", nil, func(tx *store.Tx) (any, error) {
			r, v, err := sessionRecord(tx, domain.ID(f.f.change.Session.Id))
			if err != nil {
				return nil, err
			}
			v.Archive = domain.Archived
			return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, v)
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("archived during read but input accepted", err)
	}
	rows, _, err := f.f.service.Store.Snapshot(f.ctx, store.Filter{Kind: domain.ReviewKind, SessionID: domain.ID(f.f.change.Session.Id), Limit: 200})
	if err != nil || len(rows) != 1 || rows[0].Revision != comment.Revision {
		t.Fatal("archive race changed review", err)
	}
}

func TestLocalReviewRejectsUnavailableObservationEvenWithStaleConsent(t *testing.T) {
	f := newLocalReviewFixture(t)
	comment := createLocalReviewFixtureComment(t, f)
	f.reader.Close()
	raw, _ := json.Marshal(domain.SubmitReviewComments{Mode: domain.ExecuteMode, AllowStale: true, Comments: []domain.ReviewCommentRef{{ID: domain.ID(comment.Id), Revision: comment.Revision}}})
	// A bounded request can race stream teardown; neither an unavailable reader
	// nor a deadline establishes freshness or permits acceptance without a read.
	ctx, cancel := context.WithTimeout(f.ctx, 150*time.Millisecond)
	defer cancel()
	_, err := sessionClient(f.f.accountFixture).SubmitLocalReview(ctx, ownerRequest(f.f.identity, &pb.SubmitLocalReviewRequest{RequestId: string(domain.NewID()), SessionId: f.f.change.Session.Id, DocumentJson: raw}))
	if err == nil {
		t.Fatal("unavailable observation treated as stale consent")
	}
	rows, _, err := f.f.service.Store.Snapshot(f.ctx, store.Filter{Kind: domain.ReviewKind, SessionID: domain.ID(f.f.change.Session.Id), Limit: 200})
	if err != nil || len(rows) != 1 || rows[0].Revision != comment.Revision {
		t.Fatal("unavailable read changed review", err)
	}
}

func TestLocalReviewCapacityDoesNotPreventCommentRemoval(t *testing.T) {
	f := newLocalReviewFixture(t)
	comment := createLocalReviewFixtureComment(t, f)
	session := domain.ID(f.f.change.Session.Id)
	var value domain.LocalReview
	if err := domain.Decode(comment.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	_, err := f.f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.review-limit", nil, func(tx *store.Tx) (any, error) {
		for i := 1; i < 1000; i++ {
			if err := tx.CheckReviewCapacity(session); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.ReviewKind, domain.NewID(), 0, session, domain.ID(comment.ProjectId), value); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.f.service.Store.Read(f.ctx, func(tx *store.Tx) error { return tx.CheckReviewCapacity(session) })
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("session review limit ignored", err)
	}
	_, err = sessionClient(f.f.accountFixture).DeleteLocalReviewComment(f.ctx, ownerRequest(f.f.identity, &pb.DeleteLocalReviewCommentRequest{SessionId: string(session), Mutation: &pb.Mutation{Id: comment.Id, ExpectedRevision: comment.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.service.Store.Read(f.ctx, func(tx *store.Tx) error { return tx.CheckReviewCapacity(session) }); err != nil {
		t.Fatal("deletion did not release review capacity", err)
	}
}
