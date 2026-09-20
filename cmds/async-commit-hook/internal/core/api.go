package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/async-commit-hook/internal/webassets"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1/achv1connect"
)

type API struct{ s *Service }

// Handler serves the immutable UI and local RPCs from one loopback origin.
// Local processes are trusted; browser isolation is enforced before dispatch.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	path, h := achv1connect.NewLocalServiceHandler(&API{s}, connect.WithReadMaxBytes(1024*1024), connect.WithSendMaxBytes(8*1024*1024))
	mux.Handle(path, h)
	assets := webassets.Handler()
	host := "127.0.0.1:" + strconv.Itoa(s.Personal.APIPort)
	origin := "http://" + host
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Ach-Correlation-Id", ID())
		deny := func(code string, status int) {
			s.Log.Warn("http.request_denied", "code", code)
			http.Error(w, code, status)
		}
		if r.Host != host {
			deny("host-denied", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.URL.Path, path) {
			assets.ServeHTTP(w, r)
			return
		}
		// No CORS grants or token exceptions: every RPC, including version and
		// retired pairing, requires the exact same origin and a non-simple header.
		if values := r.Header.Values("Origin"); len(values) != 1 || values[0] != origin {
			deny("origin-denied", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			deny("method-denied", http.StatusMethodNotAllowed)
			return
		}
		if values := r.Header.Values("X-Ach-Api-Version"); len(values) != 1 || values[0] != "1" {
			// Connect clients discard plain HTTP error bodies. Preserve this
			// diagnostic on the wire so a stale tab can explain how to recover.
			s.Log.Warn("http.request_denied", "code", "incompatible-version")
			if err := connect.NewErrorWriter().Write(w, r, connect.NewError(connect.CodeAborted, errors.New("incompatible-version"))); err != nil {
				s.Log.Warn("http.error_write_failed", "code", "incompatible-version")
			}
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Service) Serve(ctx context.Context, daemon bool, ready func()) error {
	var lock *Lock
	var e error
	if daemon {
		lock, e = TryLock(filepath.Join(s.Paths.Control, "daemon.lock"))
		if e != nil {
			return e
		}
		if lock == nil {
			return nil
		}
		defer lock.Close()
	}
	listener, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", s.Personal.APIPort))
	if e != nil {
		return E("port-conflict", "cannot bind configured API port; stop the conflicting server or change personal api_port while ach is stopped", 3)
	}
	defer listener.Close()
	s.Log.Info("http.listening", "port", s.Personal.APIPort, "daemon", daemon)
	defer s.Log.Info("http.stopped", "port", s.Personal.APIPort)
	kind := "viewer"
	if daemon {
		kind = "daemon"
	}
	component, leave, e := s.Enter(kind)
	if e != nil {
		return e
	}
	defer leave()
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if ready != nil {
		ready()
	}
	if daemon {
		workDone := make(chan error, 1)
		go func() { workDone <- s.Work(ctx, true, component.ID) }()
		select {
		case e = <-workDone:
		case e = <-done:
			if errors.Is(e, http.ErrServerClosed) {
				e = nil
			}
			_ = s.Stop(true)
			<-workDone
		}
	} else {
		select {
		case <-ctx.Done():
		case e = <-done:
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func apiError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, context.Canceled)
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)
	}
	var ce *Error
	if !errors.As(e, &ce) {
		return connect.NewError(connect.CodeInternal, errors.New("runner-error: inspect local diagnostics"))
	}
	code := connect.CodeFailedPrecondition
	if ce.Exit == 2 {
		code = connect.CodeInvalidArgument
	}
	if ce.Exit == 3 {
		code = connect.CodeInternal
	}
	out := connect.NewError(code, errors.New(ce.Code+": "+ce.Message))
	detail, _ := connect.NewErrorDetail(&pb.Diagnostic{Code: ce.Code, Message: ce.Message, Hint: ce.Hint})
	if detail != nil {
		out.AddDetail(detail)
	}
	return out
}
func diagnostics(in []Diagnostic) []*pb.Diagnostic {
	out := []*pb.Diagnostic{}
	for _, d := range in {
		out = append(out, &pb.Diagnostic{Code: d.Code, Message: d.Message, Hint: d.Hint})
	}
	return out
}
func wireFailures(in []Failure) []*pb.Failure {
	out := []*pb.Failure{}
	for _, f := range in {
		out = append(out, &pb.Failure{Id: f.ID, Check: f.Check, Test: f.Test, Command: f.Command, Message: f.Message, File: f.File, Line: failureLine(f.Line), LogId: f.LogID})
	}
	return out
}
func wireState(s State) pb.ExecutionState {
	return pb.ExecutionState(pb.ExecutionState_value["EXECUTION_STATE_"+strings.ToUpper(string(s))])
}
func (a *API) run(r Run, detail bool) *pb.Run {
	g := Gate{Reason: "open this execution to inspect evidence"}
	if detail {
		g = a.s.GateRun(r)
	}
	count := uint32(len(r.Checks))
	out := &pb.Run{Id: r.ID, Sequence: uint64(r.Sequence), RepositoryId: r.RepositoryID, WorktreeId: r.WorktreeID, Branch: r.Branch, Commit: r.Commit, State: wireState(r.State), Os: r.OS, Arch: r.Arch, CreatedAt: r.CreatedAt.Format(time.RFC3339Nano), ParentId: r.ParentID, GatePassed: g.Passed, GateReason: g.Reason, CheckCount: &count}
	if r.FinishedAt != nil {
		out.FinishedAt = r.FinishedAt.Format(time.RFC3339Nano)
	}
	if r.AcknowledgedAt != nil {
		out.AcknowledgedAt = r.AcknowledgedAt.Format(time.RFC3339Nano)
	}
	if !detail {
		return out
	}
	out.Diagnostics = diagnostics(r.Diagnostics)
	for _, c := range r.Checks {
		v := &pb.Check{Id: c.ID, Name: c.Name, State: wireState(c.State), Optional: c.Optional, Shell: string(c.Shell), InheritedFrom: c.InheritedFrom, Diagnostics: diagnostics(c.Diagnostics), Failures: wireFailures(c.Failures), Command: r.Config.Checks[c.Name].Command}
		if c.ExitCode != nil {
			exit := int32(*c.ExitCode)
			v.ExitCode = &exit
		}
		for _, report := range c.Reports {
			v.Reports = append(v.Reports, &pb.Report{Id: report.ID, Name: report.Name, Size: uint64(report.Size)})
		}
		out.Checks = append(out.Checks, v)
	}
	return out
}
func (a *API) worktree(ctx context.Context, id string) (string, error) {
	if !ValidID(id) {
		return "", E("invalid-worktree-id", "expected UUID v7", 2)
	}
	var path, repository string
	e := a.s.Store.DB.QueryRowContext(ctx, "SELECT path,repository_id FROM worktrees WHERE id=?", id).Scan(&path, &repository)
	if e == nil {
		common, root, _, discoverErr := Discover(ctx, path)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if discoverErr == nil {
			r, w, registeredErr := a.s.Store.Registered(common, root)
			if registeredErr == nil && r.ID == repository && w.ID == id {
				return path, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", E("worktree-unavailable", "registered worktree is unavailable", 2)
}
func (a *API) GetVersion(context.Context, *connect.Request[pb.GetVersionRequest]) (*connect.Response[pb.GetVersionResponse], error) {
	return connect.NewResponse(&pb.GetVersionResponse{ApiVersion: 1, Version: Version}), nil
}

// Pair remains only as a v1 wire tombstone. Local UI access no longer pairs browsers.
func (a *API) Pair(context.Context, *connect.Request[pb.PairRequest]) (*connect.Response[pb.PairResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("pairing-removed: open the local URL printed by ach ui"))
}

func registryLabel(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	end := limit - len("…")
	for !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "…"
}

func (a *API) ListRepositories(ctx context.Context, req *connect.Request[pb.ListRepositoriesRequest]) (*connect.Response[pb.ListRepositoriesResponse], error) {
	repos, next, e := a.s.Store.RepositoryPage(ctx, req.Msg.Cursor, int(req.Msg.Limit))
	if e != nil {
		return nil, apiError(e)
	}
	out := &pb.ListRepositoriesResponse{NextCursor: next}
	for _, r := range repos {
		v := &pb.Repository{Id: r.ID, Name: registryLabel(r.Name)}
		for _, w := range r.Worktrees {
			v.Worktrees = append(v.Worktrees, &pb.Worktree{Id: w.ID, Path: registryLabel(w.Path), Branch: registryLabel(w.Branch), BranchId: branchIdentity(w.ID, w.Branch), Available: w.Available})
		}
		out.Repositories = append(out.Repositories, v)
	}
	return connect.NewResponse(out), nil
}
func (a *API) ListBranches(ctx context.Context, r *connect.Request[pb.ListBranchesRequest]) (*connect.Response[pb.ListBranchesResponse], error) {
	path, e := a.worktree(ctx, r.Msg.WorktreeId)
	if e != nil {
		return nil, apiError(e)
	}
	branches, cursor, e := BranchPage(ctx, path, r.Msg.Cursor, int(r.Msg.Limit))
	if e != nil {
		return nil, apiError(e)
	}
	out := &pb.ListBranchesResponse{NextCursor: cursor}
	for _, v := range branches {
		out.Branches = append(out.Branches, &pb.Branch{Name: strings.ToValidUTF8(v.Name, "\uFFFD"), Commit: v.Commit, Id: branchIdentity(r.Msg.WorktreeId, v.Name)})
	}
	return connect.NewResponse(out), nil
}
func (a *API) ListCommits(ctx context.Context, r *connect.Request[pb.ListCommitsRequest]) (*connect.Response[pb.ListCommitsResponse], error) {
	path, e := a.worktree(ctx, r.Msg.WorktreeId)
	if e != nil {
		return nil, apiError(e)
	}
	ref, e := branchSelection(r.Msg.WorktreeId, r.Msg.BranchId, r.Msg.Ref, true)
	if e != nil {
		return nil, apiError(e)
	}
	commits, e := Commits(ctx, path, ref, int(r.Msg.Offset))
	if e != nil {
		return nil, apiError(e)
	}
	out := &pb.ListCommitsResponse{}
	for _, v := range commits {
		out.Commits = append(out.Commits, &pb.Commit{Id: v.ID, Parents: v.Parents, Subject: v.Subject})
	}
	return connect.NewResponse(out), nil
}
func (a *API) GetChanges(ctx context.Context, r *connect.Request[pb.GetChangesRequest]) (*connect.Response[pb.GetChangesResponse], error) {
	path, e := a.worktree(ctx, r.Msg.WorktreeId)
	if e != nil {
		return nil, apiError(e)
	}
	ref, e := branchSelection(r.Msg.WorktreeId, r.Msg.BranchId, r.Msg.Ref, true)
	if e != nil {
		return nil, apiError(e)
	}
	v, e := Diff(ctx, path, ref, r.Msg.Base)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.GetChangesResponse{Base: v.Base, Head: v.Head, MergeBase: v.MergeBase, Diff: v.Diff, Truncated: v.Truncated}), nil
}
func (a *API) ListRuns(_ context.Context, r *connect.Request[pb.ListRunsRequest]) (*connect.Response[pb.ListRunsResponse], error) {
	branch, e := branchSelection(r.Msg.WorktreeId, r.Msg.BranchId, r.Msg.Branch, false)
	if e != nil {
		return nil, apiError(e)
	}
	page, e := a.s.Store.ListFiltered(r.Msg.RepositoryId, r.Msg.WorktreeId, branch, r.Msg.Detached, r.Msg.Inbox, r.Msg.Cursor, int(r.Msg.Limit))
	if e != nil {
		return nil, apiError(e)
	}
	out := &pb.ListRunsResponse{NextCursor: page.NextCursor}
	for _, v := range page.Runs {
		out.Runs = append(out.Runs, a.run(v, false))
	}
	return connect.NewResponse(out), nil
}
func (a *API) GetRun(_ context.Context, r *connect.Request[pb.GetRunRequest]) (*connect.Response[pb.GetRunResponse], error) {
	run, e := a.s.Store.Run(r.Msg.RunId)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.GetRunResponse{Run: a.run(run, true)}), nil
}
func (a *API) GetLogs(_ context.Context, r *connect.Request[pb.GetLogsRequest]) (*connect.Response[pb.GetLogsResponse], error) {
	limit := int(r.Msg.Limit)
	if limit == 0 {
		limit = 65536
	}
	page, e := a.s.Logs(r.Msg.RunId, r.Msg.CheckId, r.Msg.Offset, limit)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.GetLogsResponse{Text: page.Text, NextOffset: page.NextOffset, Complete: page.Complete}), nil
}
func (a *API) GetFailures(_ context.Context, r *connect.Request[pb.GetFailuresRequest]) (*connect.Response[pb.GetFailuresResponse], error) {
	run, e := a.s.Store.Run(r.Msg.RunId)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.GetFailuresResponse{Failures: wireFailures(Failures(run))}), nil
}
func (a *API) Compare(_ context.Context, r *connect.Request[pb.CompareRequest]) (*connect.Response[pb.CompareResponse], error) {
	v, e := a.s.Compare(r.Msg.RunId, r.Msg.PreviousId)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.CompareResponse{Available: v.Available, Reason: v.Reason, RunId: v.RunID, PreviousId: v.PreviousID, NewFailures: wireFailures(v.New), ContinuingFailures: wireFailures(v.Continuing), ResolvedFailures: wireFailures(v.Resolved)}), nil
}
func (a *API) Acknowledge(_ context.Context, r *connect.Request[pb.AcknowledgeRequest]) (*connect.Response[pb.AcknowledgeResponse], error) {
	if e := a.s.Store.Ack(r.Msg.RunId); e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.AcknowledgeResponse{}), nil
}
func (a *API) Rerun(_ context.Context, r *connect.Request[pb.RerunRequest]) (*connect.Response[pb.RerunResponse], error) {
	v, e := a.s.Rerun(r.Msg.RunId, r.Msg.FailedOnly)
	if e != nil {
		return nil, apiError(e)
	}
	response := &pb.RerunResponse{RunId: v.RunID}
	if e = a.s.Start(a.s.Personal.Mode); e != nil {
		// Acceptance is already committed. A transport error here would hide the
		// receipt and encourage the browser to create another explicit attempt.
		response.StartupDiagnostic = &pb.Diagnostic{
			Code:    "startup-failed",
			Message: "The rerun was accepted, but the runner could not start.",
			Hint:    "Inspect ach doctor, fix the startup problem, then use ach daemon start to resume queued requests.",
		}
		code := "runner-error"
		var typed *Error
		if errors.As(e, &typed) {
			code = typed.Code
		}
		a.s.Log.Warn("rerun.startup_failed", "run_id", v.RunID, "code", code)
	}
	return connect.NewResponse(response), nil
}
func (a *API) Cancel(_ context.Context, r *connect.Request[pb.CancelRequest]) (*connect.Response[pb.CancelResponse], error) {
	if e := a.s.Store.Cancel(r.Msg.RunId, Cancelled); e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.CancelResponse{}), nil
}

func (a *API) GetReport(_ context.Context, r *connect.Request[pb.GetReportRequest]) (*connect.Response[pb.GetReportResponse], error) {
	page, err := a.s.Report(r.Msg.RunId, r.Msg.ReportId, r.Msg.Offset, int(r.Msg.Limit))
	if err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&pb.GetReportResponse{Text: page.Text, NextOffset: page.NextOffset, Complete: page.Complete}), nil
}
