package runmoor

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/actions/scaleset"
	"github.com/hashicorp/go-retryablehttp"
)

type Remote interface {
	Check(context.Context, Pool) error
	Ensure(context.Context, PoolState, func() error) (int, error)
	Session(context.Context, PoolState, string) (RemoteSession, error)
	JIT(context.Context, PoolState, Runner) (int, string, error)
	Remove(context.Context, PoolState, Runner) error
	Find(context.Context, PoolState, Runner) (bool, error)
	DeletePool(context.Context, PoolState) error
}
type RemoteSession interface {
	ID() string
	InitialDemand() int
	Poll(context.Context, int, int) (*scaleset.RunnerScaleSetMessage, error)
	Ack(context.Context, int) error
	Acquire(context.Context, []int64) error
	Close(context.Context) error
}
type GitHub struct {
	client      *scaleset.Client
	sessionHTTP func() *retryablehttp.Client
}

func (g *GitHub) Check(ctx context.Context, p Pool) error {
	group := p.RunnerGroup
	if group == "" {
		group = "default"
	}
	return remoteCall(ctx, func(ctx context.Context) error {
		v, e := g.client.GetRunnerGroupByName(ctx, group)
		if e != nil {
			return e
		}
		if v == nil {
			return problem(ErrAuth, "Runner group is unavailable.", "Check the group's permissions.")
		}
		return nil
	})
}

type requestTrace struct {
	status  int
	retryAt time.Time
}
type traceKey struct{}

func newHTTPClient() *retryablehttp.Client {
	c := retryablehttp.NewClient()
	c.RetryMax = 0
	c.Logger = nil
	c.HTTPClient.Timeout = 65 * time.Second
	c.HTTPClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	c.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if t, ok := ctx.Value(traceKey{}).(*requestTrace); ok && resp != nil {
			t.status = resp.StatusCode
			t.retryAt = retryTime(resp, time.Now())
		}
		return false, nil
	}
	// The SDK replaces CheckRetry during its admin-token exchange. Observe safe
	// status metadata here as well so permission/rate-limit classification does
	// not depend on that mutable upstream callback.
	c.ResponseLogHook = func(_ retryablehttp.Logger, resp *http.Response) {
		if resp != nil && resp.Request != nil {
			if t, ok := resp.Request.Context().Value(traceKey{}).(*requestTrace); ok {
				t.status = resp.StatusCode
				t.retryAt = retryTime(resp, time.Now())
			}
		}
	}
	return c
}
func retryTime(resp *http.Response, now time.Time) time.Time {
	var at time.Time
	if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n >= 0 {
		at = now.Add(time.Duration(n) * time.Second)
	} else if d, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
		at = d
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if n, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			d := time.Unix(n, 0)
			if d.After(at) {
				at = d
			}
		}
	}
	return at
}
func NewGitHub(conn Connection) (Remote, error) { return newGitHub(conn, newHTTPClient()) }
func newGitHub(conn Connection, httpClient *retryablehttp.Client) (*GitHub, error) {
	if os.Getenv("GITHUB_ACTIONS_FORCE_GHES") != "" {
		return nil, problem(ErrConfig, "GITHUB_ACTIONS_FORCE_GHES is incompatible with GitHub.com-only operation.", "Unset this variable before starting Runmoor.")
	}
	secret, err := ResolveSecret(conn.Credential)
	if err != nil {
		return nil, err
	}
	info := scaleset.SystemInfo{System: "runmoor", Version: Version, CommitSHA: Revision, Subsystem: "manager"}
	var c *scaleset.Client
	opts := []scaleset.HTTPOption{scaleset.WithRetryableHTTPClint(httpClient)}
	if conn.Auth == App {
		c, err = scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{GitHubConfigURL: conn.Target, GitHubAppAuth: scaleset.GitHubAppAuth{ClientID: conn.ClientID, InstallationID: conn.InstallationID, PrivateKey: secret}, SystemInfo: info}, opts...)
	} else {
		c, err = scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{GitHubConfigURL: conn.Target, PersonalAccessToken: secret, SystemInfo: info}, opts...)
	}
	if err != nil {
		return nil, problem(ErrAuth, "GitHub credentials could not initialize the client.", "Correct the selected credential reference and resume the pool.")
	}
	return &GitHub{client: c, sessionHTTP: func() *retryablehttp.Client {
		copyClient := newHTTPClient()
		copyClient.HTTPClient.Transport = httpClient.HTTPClient.Transport.(*http.Transport).Clone()
		return copyClient
	}}, nil
}
func remoteCall(ctx context.Context, fn func(context.Context) error) error {
	t := &requestTrace{}
	err := fn(context.WithValue(ctx, traceKey{}, t))
	if err == nil {
		return nil
	}
	if p, ok := err.(*Problem); ok {
		return p
	}
	if errors.Is(err, scaleset.JobStillRunningError) {
		return problem(ErrBusy, "GitHub assigned a job before idle retirement completed.", "Leave this runner running until the job finishes.")
	}
	if errors.Is(err, scaleset.RunnerNotFoundError) {
		return &Problem{Code: ErrRetry, Message: "Runner registration no longer exists.", Recovery: "Reconcile the owned execution environment.", HTTPStatus: 404}
	}
	code := ErrRetry
	message := "GitHub request failed temporarily."
	recovery := "Wait for reconnection; inspect status if the condition persists."
	if t.status == 401 || (t.status == 403 && t.retryAt.IsZero()) {
		code = ErrAuth
		message = "GitHub rejected authentication or permissions."
		recovery = "Correct the credential and target permissions, then resume the affected pool."
	}
	return &Problem{Code: code, Message: message, Recovery: recovery, HTTPStatus: t.status, RetryAt: t.retryAt}
}
func (g *GitHub) Ensure(ctx context.Context, p PoolState, markCreate func() error) (int, error) {
	groupName := p.Spec.RunnerGroup
	if groupName == "" {
		groupName = "default"
	}
	var group *scaleset.RunnerGroup
	err := remoteCall(ctx, func(ctx context.Context) error {
		var e error
		group, e = g.client.GetRunnerGroupByName(ctx, groupName)
		return e
	})
	if err != nil {
		return 0, err
	}
	if group == nil {
		return 0, problem(ErrAuth, "Requested runner group is unavailable.", "Check the group's existence and GitHub access policy.")
	}
	var existing *scaleset.RunnerScaleSet
	err = remoteCall(ctx, func(ctx context.Context) error {
		var e error
		existing, e = g.client.GetRunnerScaleSet(ctx, group.ID, p.Spec.ScaleSet)
		return e
	})
	if err != nil {
		return 0, err
	}
	if existing != nil {
		if !ownsScaleSet(existing, p) || (p.ScaleSetID == 0 && !p.CreatePending) {
			return 0, problem(ErrOwnership, "An existing scale set cannot be proven to belong to this installation.", "Choose a different scale-set name or restore this installation's original state backup.")
		}
		return existing.ID, nil
	}
	if p.ScaleSetID != 0 {
		return 0, problem(ErrOwnership, "The recorded GitHub scale set is missing.", "Drain this pool and configure a new scale-set identity; do not reuse lost ownership records.")
	}
	if err = markCreate(); err != nil {
		return 0, err
	}
	labels := []scaleset.Label{{Name: p.Spec.ScaleSet, Type: "System"}, {Name: p.OwnerLabel, Type: "User"}}
	for _, l := range p.Spec.Labels {
		if !strings.EqualFold(l, p.Spec.ScaleSet) {
			labels = append(labels, scaleset.Label{Name: l, Type: "User"})
		}
	}
	var created *scaleset.RunnerScaleSet
	err = remoteCall(ctx, func(ctx context.Context) error {
		var e error
		created, e = g.client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{Name: p.Spec.ScaleSet, RunnerGroupID: group.ID, Labels: labels, RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true}})
		return e
	})
	if err != nil {
		return 0, err
	}
	if created == nil || !ownsScaleSet(created, p) {
		return 0, problem(ErrOwnership, "Created scale-set ownership could not be verified.", "Keep the pending ownership record and inspect GitHub before retrying.")
	}
	return created.ID, nil
}
func ownsScaleSet(v *scaleset.RunnerScaleSet, p PoolState) bool {
	if v.Name != p.Spec.ScaleSet || (p.ScaleSetID != 0 && v.ID != p.ScaleSetID) {
		return false
	}
	for _, l := range v.Labels {
		if l.Name == p.OwnerLabel {
			return true
		}
	}
	return false
}
func (g *GitHub) Session(ctx context.Context, p PoolState, owner string) (RemoteSession, error) {
	var s *scaleset.MessageSessionClient
	err := remoteCall(ctx, func(ctx context.Context) error {
		var e error
		s, e = g.client.MessageSessionClient(ctx, p.ScaleSetID, owner, scaleset.WithRetryableHTTPClint(g.sessionHTTP()))
		return e
	})
	if err != nil {
		return nil, err
	}
	return &githubSession{s}, nil
}
func (g *GitHub) JIT(ctx context.Context, p PoolState, r Runner) (int, string, error) {
	var jit *scaleset.RunnerScaleSetJitRunnerConfig
	err := remoteCall(ctx, func(ctx context.Context) error {
		var e error
		jit, e = g.client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{Name: r.Name, WorkFolder: "_work"}, p.ScaleSetID)
		return e
	})
	if err != nil {
		return 0, "", err
	}
	if jit == nil || jit.Runner == nil || jit.Runner.Name != r.Name || jit.Runner.RunnerScaleSetID != p.ScaleSetID || jit.EncodedJITConfig == "" {
		return 0, "", problem(ErrOwnership, "JIT registration returned inconsistent runner ownership.", "Inspect the pool before resuming.")
	}
	return jit.Runner.ID, jit.EncodedJITConfig, nil
}
func (g *GitHub) lookup(ctx context.Context, p PoolState, r Runner) (*scaleset.RunnerReference, error) {
	var v *scaleset.RunnerReference
	err := remoteCall(ctx, func(ctx context.Context) error {
		var e error
		if r.GitHubID != 0 {
			v, e = g.client.GetRunner(ctx, r.GitHubID)
		} else {
			v, e = g.client.GetRunnerByName(ctx, r.Name)
		}
		return e
	})
	if q, ok := err.(*Problem); ok && q.HTTPStatus == 404 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if v != nil && (v.Name != r.Name || v.RunnerScaleSetID != p.ScaleSetID || (r.GitHubID != 0 && v.ID != r.GitHubID)) {
		return nil, problem(ErrOwnership, "Runner registration ownership does not match local state.", "Quarantine this execution and inspect its ownership.")
	}
	return v, nil
}
func (g *GitHub) Find(ctx context.Context, p PoolState, r Runner) (bool, error) {
	v, err := g.lookup(ctx, p, r)
	return v != nil, err
}
func (g *GitHub) Remove(ctx context.Context, p PoolState, r Runner) error {
	v, err := g.lookup(ctx, p, r)
	if err != nil || v == nil {
		return err
	}
	err = remoteCall(ctx, func(ctx context.Context) error { return g.client.RemoveRunner(ctx, int64(v.ID)) })
	if q, ok := err.(*Problem); ok && q.HTTPStatus == 404 {
		return nil
	}
	return err
}
func (g *GitHub) DeletePool(ctx context.Context, p PoolState) error {
	var v *scaleset.RunnerScaleSet
	err := remoteCall(ctx, func(ctx context.Context) error {
		var e error
		v, e = g.client.GetRunnerScaleSetByID(ctx, p.ScaleSetID)
		return e
	})
	if q, ok := err.(*Problem); ok && q.HTTPStatus == 404 {
		return nil
	}
	if err != nil {
		return err
	}
	if v == nil || !ownsScaleSet(v, p) {
		return problem(ErrOwnership, "Scale-set ownership cannot be verified for removal.", "Preserve state and inspect the remote scale set.")
	}
	return remoteCall(ctx, func(ctx context.Context) error { return g.client.DeleteRunnerScaleSet(ctx, p.ScaleSetID) })
}

type githubSession struct {
	s *scaleset.MessageSessionClient
}

func (s *githubSession) ID() string { return s.s.Session().SessionID.String() }
func (s *githubSession) InitialDemand() int {
	v := s.s.Session().Statistics
	if v == nil {
		return 0
	}
	return v.TotalAssignedJobs
}
func (s *githubSession) Poll(ctx context.Context, last, capacity int) (*scaleset.RunnerScaleSetMessage, error) {
	var m *scaleset.RunnerScaleSetMessage
	err := remoteCall(ctx, func(ctx context.Context) error { var e error; m, e = s.s.GetMessage(ctx, last, capacity); return e })
	return m, err
}
func (s *githubSession) Ack(ctx context.Context, id int) error {
	return remoteCall(ctx, func(ctx context.Context) error { return s.s.DeleteMessage(ctx, id) })
}
func (s *githubSession) Acquire(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return remoteCall(ctx, func(ctx context.Context) error { _, e := s.s.AcquireJobs(ctx, ids); return e })
}
func (s *githubSession) Close(ctx context.Context) error {
	return remoteCall(ctx, func(ctx context.Context) error { return s.s.Close(ctx) })
}
func retryDelay(attempt int, err error) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	d := time.Second * time.Duration(1<<attempt)
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
	if p, ok := err.(*Problem); ok && !p.RetryAt.IsZero() {
		if wait := time.Until(p.RetryAt); wait > d {
			d = wait
		}
	}
	return d
}
func waitContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
