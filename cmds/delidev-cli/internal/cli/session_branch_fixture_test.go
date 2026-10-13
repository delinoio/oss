// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const (
	branchFixtureRepository = "019a1000-0000-7000-8000-000000000001"
	branchFixtureSession    = "019a1000-0000-7000-8000-000000000002"
	branchFixtureCommit     = "1111111111111111111111111111111111111111"
	branchFixtureHead       = "2222222222222222222222222222222222222222"
	branchFixturePatch      = "diff --git a/tracked.txt b/tracked.txt\nindex 1111111..2222222 100644\n--- a/tracked.txt\n+++ b/tracked.txt\n@@ -1 +1 @@\n-base\n+changed\n"
)

// branchRead records one workspace or review-context request received by the
// loopback fixture, with the deadline the CLI propagated to it.
type branchRead struct {
	procedure string
	query     domain.WorkspaceReadQuery
	remaining time.Duration
}

type branchFixture struct {
	t *testing.T
	// root is the CLI data directory carrying the paired client credential.
	root string
	mu   sync.Mutex
	// reads lists every observed request in arrival order.
	reads []branchRead
	// options builds the git-diff-options document; nil returns the default
	// inventory containing only an available local branch named main.
	options func() (*domain.WorkspaceReadResult, error)
	// diff builds the final observation; nil returns a valid branch diff for
	// the requested base. The request context is passed so fixtures can delay.
	diff func(ctx context.Context, q domain.WorkspaceReadQuery) (*domain.WorkspaceDiff, error)
}

func branchMainChoice() domain.WorkspaceDiffChoice {
	return domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Available: true}
}

func branchOptionsResult(choices ...domain.WorkspaceDiffChoice) *domain.WorkspaceReadResult {
	options := &domain.WorkspaceDiffOptions{Version: 1, RepositoryID: domain.ID(branchFixtureRepository), Choices: choices}
	for _, choice := range choices {
		if choice.Available && options.Default == (domain.Reference{}) {
			options.Default = choice.Reference
		}
	}
	return &domain.WorkspaceReadResult{DiffOptions: options}
}

func branchValidDiff(q domain.WorkspaceReadQuery) *domain.WorkspaceDiff {
	diff := &domain.WorkspaceDiff{Comparison: q.Comparison, RepositoryID: q.RepositoryID, Path: q.Path, Untracked: []string{}}
	if q.Comparison == domain.DiffBranch {
		diff.Base, diff.BaseObject, diff.HeadCommit = domain.DiffCommit, branchFixtureCommit, branchFixtureHead
		diff.BaseRef, diff.BaseCommit, diff.MergeBase, diff.Patch = q.BaseRef, branchFixtureCommit, branchFixtureCommit, branchFixturePatch
	} else {
		diff.Base, diff.BaseObject, diff.HeadCommit = domain.DiffCommit, branchFixtureHead, branchFixtureHead
	}
	diff.Revision = diff.Digest()
	return diff
}

func newBranchFixture(t *testing.T) *branchFixture {
	t.Helper()
	f := &branchFixture{t: t}
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
		return connect.NewResponse(&pb.GetStatusResponse{}), nil
	}))
	mux.Handle(delidevv1connect.SessionServiceReadSessionWorkspaceProcedure, connect.NewUnaryHandler(delidevv1connect.SessionServiceReadSessionWorkspaceProcedure, func(ctx context.Context, req *connect.Request[pb.ReadSessionWorkspaceRequest]) (*connect.Response[pb.ReadSessionWorkspaceResponse], error) {
		q := f.record(ctx, delidevv1connect.SessionServiceReadSessionWorkspaceProcedure, req.Msg.QueryJson)
		var result *domain.WorkspaceReadResult
		var err error
		if q.Operation == domain.WorkspaceGitDiffOptions {
			result, err = f.optionsDocument()
		} else {
			var diff *domain.WorkspaceDiff
			if diff, err = f.diffDocument(ctx, q); err == nil {
				result = &domain.WorkspaceReadResult{Diff: diff}
			}
		}
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(result)
		return connect.NewResponse(&pb.ReadSessionWorkspaceResponse{DocumentJson: raw}), nil
	}))
	mux.Handle(delidevv1connect.SessionServiceReadSessionReviewContextProcedure, connect.NewUnaryHandler(delidevv1connect.SessionServiceReadSessionReviewContextProcedure, func(ctx context.Context, req *connect.Request[pb.ReadSessionReviewContextRequest]) (*connect.Response[pb.ReadSessionReviewContextResponse], error) {
		q := f.record(ctx, delidevv1connect.SessionServiceReadSessionReviewContextProcedure, req.Msg.QueryJson)
		diff, err := f.diffDocument(ctx, q)
		if err != nil {
			return nil, err
		}
		// Files are derived by the CLI from the validated patch; send none.
		raw, _ := json.Marshal(domain.ReviewContext{Diff: *diff, Files: []domain.ReviewFile{}})
		return connect.NewResponse(&pb.ReadSessionReviewContextResponse{DocumentJson: raw}), nil
	}))
	peer := httptest.NewServer(mux)
	t.Cleanup(peer.Close)
	f.root = identityClientScope(t, peer.URL, domain.ClientDevice)
	return f
}

func (f *branchFixture) record(ctx context.Context, procedure string, raw []byte) domain.WorkspaceReadQuery {
	var q domain.WorkspaceReadQuery
	if err := json.Unmarshal(raw, &q); err != nil {
		f.t.Error("fixture received an invalid query", err)
	}
	read := branchRead{procedure: procedure, query: q}
	if deadline, ok := ctx.Deadline(); ok {
		read.remaining = time.Until(deadline)
	}
	f.mu.Lock()
	f.reads = append(f.reads, read)
	f.mu.Unlock()
	return q
}

func (f *branchFixture) optionsDocument() (*domain.WorkspaceReadResult, error) {
	if f.options != nil {
		return f.options()
	}
	return branchOptionsResult(branchMainChoice()), nil
}

func (f *branchFixture) diffDocument(ctx context.Context, q domain.WorkspaceReadQuery) (*domain.WorkspaceDiff, error) {
	if f.diff != nil {
		return f.diff(ctx, q)
	}
	return branchValidDiff(q), nil
}

func (f *branchFixture) snapshot() []branchRead {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]branchRead(nil), f.reads...)
}

// run executes the CLI against the fixture and decodes its JSON envelope.
func (f *branchFixture) run(ctx context.Context, args ...string) (int, map[string]any) {
	f.t.Helper()
	var out, stderr bytes.Buffer
	code := Run(ctx, append([]string{"--data-dir", f.root}, args...), IO{In: strings.NewReader(""), Out: &out, Err: &stderr})
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		f.t.Fatal("invalid envelope", err, out.String(), stderr.String())
	}
	return code, envelope
}

func branchArgs(command string, extra ...string) []string {
	base := []string{"--id", branchFixtureSession, "--repository-id", branchFixtureRepository}
	var args []string
	switch command {
	case "diff", "review-context":
		args = []string{"session", command}
	default:
		args = []string{"session", "files", strings.TrimPrefix(command, "files ")}
	}
	return append(append(args, base...), extra...)
}

func envelopeError(envelope map[string]any) domain.Code {
	if problem, ok := envelope["error"].(map[string]any); ok {
		code, _ := problem["code"].(string)
		return domain.Code(code)
	}
	return ""
}
