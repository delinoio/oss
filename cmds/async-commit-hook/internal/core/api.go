package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1/achv1connect"
)

type API struct{ s *Service }

func randomToken() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) PairingCode() (string, error) {
	code := randomToken()
	_, e := s.Store.DB.Exec("INSERT INTO pairings(code_hash,expires) VALUES(?,?)", Hash([]byte(code)), time.Now().UTC().Add(5*time.Minute).Format(time.RFC3339Nano))
	return code, e
}
func (s *Service) Pair(code, name string) (string, string, error) {
	if len(code) != 64 || len(name) < 1 || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") {
		return "", "", E("pairing-invalid", "invalid pairing code or browser name", 2)
	}
	id := ID()
	token := randomToken()
	e := s.Store.Transaction(func(tx *sql.Tx) error {
		var expires string
		if err := tx.QueryRow("SELECT expires FROM pairings WHERE code_hash=?", Hash([]byte(code))).Scan(&expires); err != nil {
			return E("pairing-invalid", "pairing code expired, already consumed or unavailable; run ach ui", 2)
		}
		expiry, err := time.Parse(time.RFC3339Nano, expires)
		if err != nil || !time.Now().Before(expiry) {
			return E("pairing-expired", "pairing code expired; run ach ui", 2)
		}
		if _, err = tx.Exec("DELETE FROM pairings WHERE code_hash=?", Hash([]byte(code))); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO browsers(id,name,token_hash,created) VALUES(?,?,?,?)", id, name, Hash([]byte(token)), time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
	return id, token, e
}

type Browser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created string `json:"created_at"`
	Revoked bool   `json:"revoked"`
}

func (s *Service) Browsers() ([]Browser, error) {
	rows, e := s.Store.DB.Query("SELECT id,name,created,revoked FROM browsers ORDER BY created")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Browser{}
	for rows.Next() {
		var b Browser
		if e = rows.Scan(&b.ID, &b.Name, &b.Created, &b.Revoked); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Service) Revoke(id string) error {
	if !ValidID(id) {
		return E("invalid-browser-id", "expected UUID v7", 2)
	}
	_, e := s.Store.DB.Exec("UPDATE browsers SET revoked=1 WHERE id=?", id)
	return e
}
func (s *Service) Authenticate(header string) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if len(token) != 64 {
		return false
	}
	var n int
	e := s.Store.DB.QueryRow("SELECT count(*) FROM browsers WHERE token_hash=? AND revoked=0", Hash([]byte(token))).Scan(&n)
	return e == nil && n == 1
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	path, h := achv1connect.NewLocalServiceHandler(&API{s}, connect.WithReadMaxBytes(1024*1024), connect.WithSendMaxBytes(8*1024*1024))
	mux.Handle(path, h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin == "https://ach.delino.io" || origin == "http://localhost:46308" || origin == "http://127.0.0.1:46308"
		host := "127.0.0.1:" + strconv.Itoa(s.Personal.APIPort)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Ach-Correlation-Id", ID())
		if !allowed || r.Host != host {
			http.Error(w, "origin-or-host-denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", "X-Ach-Correlation-Id")
		if r.Method == http.MethodOptions {
			if r.Header.Get("Access-Control-Request-Method") != "POST" {
				http.Error(w, "method-denied", 405)
				return
			}
			for _, v := range strings.Split(strings.ToLower(r.Header.Get("Access-Control-Request-Headers")), ",") {
				switch strings.TrimSpace(v) {
				case "content-type", "authorization", "connect-protocol-version", "connect-timeout-ms", "x-ach-api-version", "":
				default:
					http.Error(w, "header-denied", 403)
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,Connect-Protocol-Version,Connect-Timeout-Ms,X-Ach-Api-Version")
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method-denied", 405)
			return
		}
		if v := r.Header.Get("X-Ach-Api-Version"); v != "" && v != "1" {
			http.Error(w, "incompatible-version", http.StatusConflict)
			return
		}
		public := r.URL.Path == achv1connect.LocalServiceGetVersionProcedure || r.URL.Path == achv1connect.LocalServicePairProcedure
		if !public && !s.Authenticate(r.Header.Get("Authorization")) {
			http.Error(w, "browser-authorization-required", http.StatusUnauthorized)
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
		out = append(out, &pb.Failure{Id: f.ID, Check: f.Check, Test: f.Test, Command: f.Command, Message: f.Message, File: f.File, Line: int32(f.Line), LogId: f.LogID})
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
	out := &pb.Run{Id: r.ID, Sequence: uint64(r.Sequence), RepositoryId: r.RepositoryID, WorktreeId: r.WorktreeID, Branch: r.Branch, Commit: r.Commit, State: wireState(r.State), Os: r.OS, Arch: r.Arch, CreatedAt: r.CreatedAt.Format(time.RFC3339Nano), ParentId: r.ParentID, Diagnostics: diagnostics(r.Diagnostics), GatePassed: g.Passed, GateReason: g.Reason}
	if r.FinishedAt != nil {
		out.FinishedAt = r.FinishedAt.Format(time.RFC3339Nano)
	}
	if r.AcknowledgedAt != nil {
		out.AcknowledgedAt = r.AcknowledgedAt.Format(time.RFC3339Nano)
	}
	for _, c := range r.Checks {
		v := &pb.Check{Id: c.ID, Name: c.Name, State: wireState(c.State), Optional: c.Optional, Shell: string(c.Shell), InheritedFrom: c.InheritedFrom, Diagnostics: diagnostics(c.Diagnostics), Failures: wireFailures(c.Failures), Command: r.Config.Checks[c.Name].Command}
		if c.ExitCode != nil {
			exit := int32(*c.ExitCode)
			v.ExitCode = &exit
		}
		for _, report := range c.Reports {
			v.Reports = append(v.Reports, &pb.Report{Id: report.ID, Name: report.Name, Size: uint64(report.Size)})
		}
		if !detail {
			v.Diagnostics = nil
			v.Failures = nil
			v.Reports = nil
			v.Command = ""
		}
		out.Checks = append(out.Checks, v)
	}
	return out
}
func (a *API) worktree(id string) (string, error) {
	if !ValidID(id) {
		return "", E("invalid-worktree-id", "expected UUID v7", 2)
	}
	var path, repository string
	e := a.s.Store.DB.QueryRow("SELECT path,repository_id FROM worktrees WHERE id=?", id).Scan(&path, &repository)
	if e == nil {
		common, root, _, discoverErr := Discover(context.Background(), path)
		if discoverErr == nil {
			r, w, registeredErr := a.s.Store.Registered(common, root)
			if registeredErr == nil && r.ID == repository && w.ID == id {
				return path, nil
			}
		}
	}
	return "", E("worktree-unavailable", "registered worktree is unavailable", 2)
}
func (a *API) GetVersion(context.Context, *connect.Request[pb.GetVersionRequest]) (*connect.Response[pb.GetVersionResponse], error) {
	return connect.NewResponse(&pb.GetVersionResponse{ApiVersion: 1, Version: Version}), nil
}
func (a *API) Pair(_ context.Context, r *connect.Request[pb.PairRequest]) (*connect.Response[pb.PairResponse], error) {
	id, token, e := a.s.Pair(r.Msg.Code, r.Msg.BrowserName)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.PairResponse{BrowserId: id, Token: token}), nil
}
func (a *API) ListRepositories(context.Context, *connect.Request[pb.ListRepositoriesRequest]) (*connect.Response[pb.ListRepositoriesResponse], error) {
	repos, e := a.s.Store.Repositories()
	if e != nil {
		return nil, apiError(e)
	}
	out := &pb.ListRepositoriesResponse{}
	for _, r := range repos {
		v := &pb.Repository{Id: r.ID, Name: strings.ToValidUTF8(r.Name, "\uFFFD")}
		for _, w := range r.Worktrees {
			v.Worktrees = append(v.Worktrees, &pb.Worktree{Id: w.ID, Path: strings.ToValidUTF8(w.Path, "\uFFFD"), Branch: strings.ToValidUTF8(w.Branch, "\uFFFD"), Available: w.Available})
		}
		out.Repositories = append(out.Repositories, v)
	}
	return connect.NewResponse(out), nil
}
func (a *API) ListBranches(ctx context.Context, r *connect.Request[pb.ListBranchesRequest]) (*connect.Response[pb.ListBranchesResponse], error) {
	path, e := a.worktree(r.Msg.WorktreeId)
	if e != nil {
		return nil, apiError(e)
	}
	branches, e := Branches(ctx, path)
	if e != nil {
		return nil, apiError(e)
	}
	out := &pb.ListBranchesResponse{}
	for _, v := range branches {
		out.Branches = append(out.Branches, &pb.Branch{Name: strings.ToValidUTF8(v.Name, "\uFFFD"), Commit: v.Commit})
	}
	return connect.NewResponse(out), nil
}
func (a *API) ListCommits(ctx context.Context, r *connect.Request[pb.ListCommitsRequest]) (*connect.Response[pb.ListCommitsResponse], error) {
	path, e := a.worktree(r.Msg.WorktreeId)
	if e != nil {
		return nil, apiError(e)
	}
	commits, e := Commits(ctx, path, r.Msg.Ref, int(r.Msg.Offset))
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
	path, e := a.worktree(r.Msg.WorktreeId)
	if e != nil {
		return nil, apiError(e)
	}
	v, e := Diff(ctx, path, r.Msg.Ref, r.Msg.Base)
	if e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.GetChangesResponse{Base: v.Base, Head: v.Head, MergeBase: v.MergeBase, Diff: v.Diff, Truncated: v.Truncated}), nil
}
func (a *API) ListRuns(_ context.Context, r *connect.Request[pb.ListRunsRequest]) (*connect.Response[pb.ListRunsResponse], error) {
	page, e := a.s.Store.ListFiltered(r.Msg.RepositoryId, r.Msg.WorktreeId, r.Msg.Branch, r.Msg.Detached, r.Msg.Inbox, r.Msg.Cursor, int(r.Msg.Limit))
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
	if e = a.s.Start(a.s.Personal.Mode); e != nil {
		return nil, apiError(e)
	}
	return connect.NewResponse(&pb.RerunResponse{RunId: v.RunID}), nil
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
