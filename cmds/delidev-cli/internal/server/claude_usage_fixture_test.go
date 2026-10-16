// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	usageFixtureAccess  = "sk-ant-oat01-UsageServerAccessTokenABCDEFGHIJKLMNOP0123456789"
	usageFixtureRefresh = "sk-ant-ort01-UsageServerRefreshTokenABCDEFGHIJKLMNOP0123456789"
	usageFixtureRotated = "sk-ant-ort01-UsageServerRotatedTokenABCDEFGHIJKLMNOP0123456789"
	usageFixtureCode    = "usage-approval#original-state"
)

func usageFixtureURL() string {
	q := url.Values{"client_id": {"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}, "response_type": {"code"}, "redirect_uri": {"https://platform.claude.com/oauth/code/callback"}, "scope": {"user:profile user:inference user:sessions:claude_code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {strings.Repeat("b", 32)}, "code": {"true"}}
	return "https://claude.com/cai/oauth/authorize?" + q.Encode()
}

// usageNativeRecord is the native credential record a login or refresh leaves in
// the usage profile.
func usageNativeRecord(access, refresh string, expires time.Time) []byte {
	raw, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": access, "refreshToken": refresh, "expiresAt": expires.UnixMilli(), "scopes": []string{"user:inference", "user:profile", "user:sessions:claude_code"}, "subscriptionType": "max"}})
	return raw
}

// fakeUsageStorage is the native credential storage of the fake usage profile.
type fakeUsageStorage struct {
	mu                             sync.Mutex
	record                         []byte
	presentErr, readErr, removeErr error
	removals, reads                int
	removeHook                     func()
	foreign                        bool
}

func (s *fakeUsageStorage) set(record []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record = bytes.Clone(record)
}
func (s *fakeUsageStorage) Present(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.record) != 0, s.presentErr
}
func (s *fakeUsageStorage) Read(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.readErr != nil {
		return nil, s.readErr
	}
	if len(s.record) == 0 {
		return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureCredentials, 0)
	}
	return bytes.Clone(s.record), nil
}
func (s *fakeUsageStorage) Remove(context.Context) error {
	s.mu.Lock()
	hook := s.removeHook
	s.removals++
	if s.removeErr != nil {
		defer s.mu.Unlock()
		return s.removeErr
	}
	s.record = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

// fakeUsageProcess is a native child of the usage profile.
type fakeUsageProcess struct {
	url                  string
	method               domain.SubscriptionLoginMethod
	progressErr, waitErr error
	closeErr             error
	ready, finish        chan struct{}
	codes                chan []byte
	submitErr            error
	closed               atomic.Bool
	submitCalls          atomic.Int32
	closeCalls           atomic.Int32
}

func newFakeUsageProcess() *fakeUsageProcess {
	return &fakeUsageProcess{url: usageFixtureURL(), method: domain.SubscriptionBrowserCode, ready: make(chan struct{}), finish: make(chan struct{}), codes: make(chan []byte, 4)}
}
func (p *fakeUsageProcess) Progress() (claude.AuthLoginProgress, error) {
	if p.progressErr != nil {
		return claude.AuthLoginProgress{}, p.progressErr
	}
	select {
	case <-p.ready:
		return claude.AuthLoginProgress{URL: p.url, Method: p.method}, nil
	default:
		return claude.AuthLoginProgress{}, nil
	}
}
func (p *fakeUsageProcess) Submit(code []byte) error {
	p.submitCalls.Add(1)
	p.codes <- bytes.Clone(code)
	clear(code)
	return p.submitErr
}
func (p *fakeUsageProcess) Done() <-chan struct{} { return p.finish }
func (p *fakeUsageProcess) Wait() error           { return p.waitErr }
func (p *fakeUsageProcess) Close() error {
	p.closeCalls.Add(1)
	p.closed.Store(true)
	return p.closeErr
}

// fakeUsageNative is the exclusive usage profile of one account.
type fakeUsageNative struct {
	mu            sync.Mutex
	store         *fakeUsageStorage
	login         *fakeUsageProcess
	identity      claude.UsageIdentity
	identityErr   error
	startErr      error
	refresh       func(claude.UsageRefreshInput) (*fakeUsageProcess, error)
	refreshInputs []claude.UsageRefreshInput
	opens, closes atomic.Int32
	loginStarts   atomic.Int32
}

func newFakeUsageNative() *fakeUsageNative {
	return &fakeUsageNative{store: &fakeUsageStorage{}, login: newFakeUsageProcess(), identity: claude.UsageIdentity{Email: "alex@example.com", Organization: "Example Org", OrgID: "org-fixture", Plan: "max"}}
}
func (n *fakeUsageNative) Storage() claudeUsageStorage { return n.store }
func (n *fakeUsageNative) StartLogin(context.Context) (claudeUsageLoginProcess, error) {
	n.loginStarts.Add(1)
	if n.startErr != nil {
		return nil, n.startErr
	}
	return n.login, nil
}
func (n *fakeUsageNative) StartRefresh(_ context.Context, input claude.UsageRefreshInput) (claudeUsageLoginProcess, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.refreshInputs = append(n.refreshInputs, claude.UsageRefreshInput{RefreshToken: bytes.Clone(input.RefreshToken), Scopes: append([]string(nil), input.Scopes...), ClientID: input.ClientID})
	if n.refresh == nil {
		return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureRefreshUnsupported, 0)
	}
	process, err := n.refresh(input)
	if err != nil {
		return nil, err
	}
	return process, nil
}
func (n *fakeUsageNative) Identity(context.Context) (claude.UsageIdentity, error) {
	return n.identity, n.identityErr
}
func (n *fakeUsageNative) Close() error { n.closes.Add(1); return nil }

// usageTransport scripts the usage endpoint.
type usageTransport struct {
	mu        sync.Mutex
	requests  []*http.Request
	tokens    []string
	responses []usageResponse
	err       error
	delay     chan struct{}
}
type usageResponse struct {
	status int
	body   string
	header http.Header
}

func (t *usageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.requests = append(t.requests, r)
	t.tokens = append(t.tokens, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	var next usageResponse
	if len(t.responses) != 0 {
		next = t.responses[0]
		if len(t.responses) > 1 {
			t.responses = t.responses[1:]
		}
	} else {
		next = usageResponse{status: 200, body: `{"five_hour":{"utilization":25,"resets_at":"2026-10-16T12:00:00Z"},"seven_day":{"utilization":50,"resets_at":"2026-10-20T12:00:00Z"}}`}
	}
	err, delay := t.err, t.delay
	t.mu.Unlock()
	if delay != nil {
		select {
		case <-delay:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	if err != nil {
		return nil, err
	}
	header := next.header
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: next.status, Header: header, Body: io.NopCloser(strings.NewReader(next.body)), Request: r}, nil
}
func (t *usageTransport) count() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.requests) }

// usageFixture is a connected server-token Claude account with a fake usage
// profile and a scripted usage endpoint.
type usageFixture struct {
	*managedFixture
	native    *fakeUsageNative
	transport *usageTransport
}

func newUsageFixture(t *testing.T) *usageFixture {
	t.Helper()
	m := newManagedClaudeFixture(t)
	m.connect()
	f := &usageFixture{managedFixture: m, native: newFakeUsageNative(), transport: &usageTransport{}}
	f.service.claudeUsageOpen = func(context.Context, nativeScope, domain.ID, *slog.Logger) (claudeUsageNative, error) {
		f.native.opens.Add(1)
		return f.native, nil
	}
	f.service.claudeUsageStorageOpen = func(context.Context, nativeScope) (claudeUsageStorage, func() error, error) {
		return f.native.store, func() error { return nil }, nil
	}
	f.service.claudeUsageTransport = f.transport
	return f
}

func (f *usageFixture) owner() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

func (f *usageFixture) usage() *domain.ClaudeLimitState {
	f.t.Helper()
	_, a := f.record()
	if a.Subscription == nil {
		return nil
	}
	return a.Subscription.ClaudeUsage
}

func (f *usageFixture) start(observed string) (*pb.StartClaudeUsageResponse, error) {
	r, _ := f.record()
	response, err := f.client.StartClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.StartClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, ObservedUsageGeneration: observed}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *usageFixture) progress(operation, generation string) (*pb.GetClaudeUsageProgressResponse, error) {
	response, err := f.client.GetClaudeUsageProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetClaudeUsageProgressRequest{AccountId: string(f.input.AccountID), OperationId: operation, UsageGeneration: generation}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *usageFixture) awaitUsageState(operation string, want pb.ClaudeUsageLoginState) *pb.GetClaudeUsageProgressResponse {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		progress, err := f.progress(operation, operation)
		if err != nil {
			f.t.Fatal(err)
		}
		if progress.State == want {
			return progress
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %v, saw %v", want, progress.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *usageFixture) runLogin() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.runClaudeUsageLogin(f.owner(), f.input.AccountID)
	}()
	return done
}

func (f *usageFixture) submit(operation, code string) error {
	r, _ := f.record()
	_, err := f.client.SubmitClaudeUsageCode(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SubmitClaudeUsageCodeRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: operation, UsageGeneration: operation, Code: []byte(code)}))
	return err
}

func (f *usageFixture) confirm(operation string) (*pb.ConfirmClaudeUsageLinkResponse, error) {
	r, _ := f.record()
	response, err := f.client.ConfirmClaudeUsageLink(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ConfirmClaudeUsageLinkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: operation, UsageGeneration: operation}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *usageFixture) cancel(operation string) (*pb.CancelClaudeUsageResponse, error) {
	r, _ := f.record()
	response, err := f.client.CancelClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: operation, UsageGeneration: operation}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *usageFixture) disconnect(generation string) (*pb.DisconnectClaudeUsageResponse, error) {
	r, _ := f.record()
	response, err := f.client.DisconnectClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.DisconnectClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, UsageGeneration: generation}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// link runs one complete login to the confirmed link and returns the new
// usage generation. The native bundle expires an hour from now unless set.
func (f *usageFixture) link() string {
	f.t.Helper()
	started, err := f.start("")
	if err != nil {
		f.t.Fatal(err)
	}
	done := f.runLogin()
	close(f.native.login.ready)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	if err := f.submit(started.OperationId, usageFixtureCode); err != nil {
		f.t.Fatal(err)
	}
	select {
	case <-f.native.login.codes:
	case <-time.After(10 * time.Second):
		f.t.Fatal("approval code never reached the original child")
	}
	f.native.store.set(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, time.Now().Add(time.Hour)))
	close(f.native.login.finish)
	awaitServerFixture(f.t, done)
	f.awaitUsageState(started.OperationId, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CONFIRMING)
	if _, err := f.confirm(started.OperationId); err != nil {
		f.t.Fatal(err)
	}
	return started.OperationId
}

// usageRefs lists the unremoved claude-usage references of the account.
func (f *usageFixture) usageRefs() []credentials.Ref {
	f.secrets.mu.Lock()
	defer f.secrets.mu.Unlock()
	var refs []credentials.Ref
	for ref := range f.secrets.values {
		if ref.Owner == f.input.AccountID && ref.Purpose == credentials.ClaudeUsage {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (f *usageFixture) executionRefs() []credentials.Ref {
	f.secrets.mu.Lock()
	defer f.secrets.mu.Unlock()
	var refs []credentials.Ref
	for ref := range f.secrets.values {
		if ref.Owner == f.input.AccountID && ref.Purpose == credentials.AccountLogin {
			refs = append(refs, ref)
		}
	}
	return refs
}

// mutateUsage rewrites the stored usage state directly (test seeding only).
func (f *usageFixture) mutateUsage(change func(*domain.ClaudeLimitState)) {
	f.t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-usage", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		change(claudeUsageStateOf(&a))
		return nil, putClaudeUsage(tx, r, a)
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// seedBundle replaces the linked connection's stored credential with a bundle
// that expires at the given time and returns its reference.
func (f *usageFixture) seedBundle(expires time.Time) domain.ID {
	f.t.Helper()
	usage := f.usage()
	bundle, err := subscriptionParseNative(usageNativeRecord(usageFixtureAccess, usageFixtureRefresh, expires))
	if err != nil {
		f.t.Fatal(err)
	}
	credential := domain.NewID()
	envelope, err := subscriptionSealUsage(f.service.Identity.ServerID, f.input.AccountID, usage.Generation, credential, bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	ref := credentials.Ref{Owner: f.input.AccountID, ID: credential, Purpose: credentials.ClaudeUsage}
	f.secrets.mu.Lock()
	delete(f.secrets.values, credentials.Ref{Owner: f.input.AccountID, ID: usage.CredentialID, Purpose: credentials.ClaudeUsage})
	f.secrets.values[ref] = envelope
	f.secrets.mu.Unlock()
	f.mutateUsage(func(s *domain.ClaudeLimitState) { s.CredentialID = credential })
	return credential
}

// runRead accepts an explicit refresh and runs the read to completion.
func (f *usageFixture) refreshOnce(manual bool) claudeUsageReadOutcome {
	f.t.Helper()
	var outcome claudeUsageReadOutcome
	unlock, err := f.service.lockAccounts(f.owner())
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(f.owner(), domain.NewID(), "fixture.claude-usage.accept", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		outcome, err = f.service.acceptClaudeUsageRead(tx, r, a, domain.NewID(), domain.Principal{Type: domain.OwnerDevice}, time.Now().UTC(), manual)
		return nil, err
	})
	unlock()
	if err != nil {
		f.t.Fatal(err)
	}
	if outcome == claudeUsageReadAccepted {
		f.service.runClaudeUsageRead(f.owner(), f.input.AccountID)
	}
	return outcome
}

var errUsageFixture = errors.New("fixture")

func subscriptionParseNative(raw []byte) (subscription.ClaudeUsageBundle, error) {
	return subscription.ParseClaudeUsageNative(raw, time.Now().UTC())
}

func subscriptionSealUsage(server, account, generation, credential domain.ID, bundle subscription.ClaudeUsageBundle) ([]byte, error) {
	return subscription.SealClaudeUsage(server, account, generation, credential, bundle, time.Now().UTC())
}
