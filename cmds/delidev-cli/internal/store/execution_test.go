package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// These fixtures exercise the database commit primitive only. A ready test
// session and account are not native compatibility or live workspace evidence.
type executionFixture struct {
	store    *Store
	agent    domain.ID
	model    domain.ID
	provider domain.ID
	machine  domain.ID
	template domain.ID
	accounts []domain.ID
}

func newExecutionFixture(t *testing.T, s *Store) executionFixture {
	t.Helper()
	f := executionFixture{s, domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), []domain.ID{domain.NewID(), domain.NewID()}}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.execution-configuration", f.agent, func(tx *Tx) (any, error) {
		put := func(kind domain.Kind, id domain.ID, body any) {
			if _, err := tx.Put(kind, id, 0, "", "", body); err != nil {
				t.Fatal(err)
			}
		}
		put(domain.ProviderKind, f.provider, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
		put(domain.ModelKind, f.model, domain.Model{Name: "Fixture", NativeID: "native-fixture", ProviderID: f.provider, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})
		put(domain.MachineKind, f.machine, domain.Machine{Name: "Fixture", OS: "linux", Architecture: "arm64"})
		put(domain.TemplateKind, f.template, domain.Template{Name: "Fixture", Contents: "Initial additive text."})
		links := []domain.WeightedAccount{}
		for _, id := range f.accounts {
			put(domain.AccountKind, id, domain.Account{Alias: "Fixture", ProviderID: f.provider, Type: domain.APIAccount, Enabled: true, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: time.Now().UTC()}})
			links = append(links, domain.WeightedAccount{ID: id, Weight: 1})
		}
		policy := domain.RoundRobin
		put(domain.AgentKind, f.agent, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: f.model, Effort: "high", Accounts: links, Routing: &policy, Templates: []domain.ID{f.template}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}})
		return f.agent, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f executionFixture) session(t *testing.T, dispatch domain.DispatchState) (domain.ID, domain.ID) {
	t.Helper()
	session, input, preparation := domain.NewID(), domain.NewID(), domain.NewID()
	_, err := f.store.Mutate(context.Background(), domain.NewID(), "fixture.execution-session", session, func(tx *Tx) (any, error) {
		if _, err := tx.PutJob(preparation, 0, session, "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobSucceeded, MachineID: f.machine, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		prompt := "private input"
		if _, err := tx.Put(domain.QueueKind, input, 0, session, "", domain.QueuedInput{Sequence: 1, ContentRevision: 1, Prompt: prompt, Mode: domain.ExecuteMode, Delivery: domain.InputQueued}); err != nil {
			return nil, err
		}
		return tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "Fixture", AgentID: f.agent, MachineID: f.machine, Workspace: domain.GeneralChat, Source: domain.ManualSession, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: dispatch, LastInputSequence: 1, PendingInputs: 1, PendingInputBytes: uint64(len(prompt)), Preparation: &domain.SessionPreparation{JobID: preparation, State: domain.PreparationReady}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return session, input
}

func (f executionFixture) claim(request, session, input domain.ID) (Result, error) {
	identity := struct{ Session, Input domain.ID }{session, input}
	return f.store.Mutate(context.Background(), request, "fixture.claim-execution", identity, func(tx *Tx) (any, error) {
		_, err := tx.ClaimInitialExecution(session, 1, input, 1)
		// Production receipts likewise retain references only, not duplicate
		// prompts/templates or stale accepted state that could resurrect data.
		return identity, err
	})
}

func readExecutionSession(t *testing.T, s *Store, id domain.ID) domain.Session {
	t.Helper()
	r, err := s.Get(context.Background(), domain.SessionKind, id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decode[domain.Session](r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInitialExecutionSnapshotIsAtomicAndImmutableAcrossRestart(t *testing.T) {
	s, root := openTest(t)
	f := newExecutionFixture(t, s)
	session, input := f.session(t, domain.DispatchReady)
	editTemplate := func(contents string) {
		t.Helper()
		_, err := f.store.Mutate(context.Background(), domain.NewID(), "fixture.template-edit", contents, func(tx *Tx) (any, error) {
			r, err := tx.Get(domain.TemplateKind, f.template)
			if err != nil {
				return nil, err
			}
			return tx.Put(r.Kind, r.ID, r.Revision, "", "", domain.Template{Name: "Fixture", Contents: contents})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	editTemplate("Edited after creation, before dispatch.")
	abort := errors.New("fixture preflight evidence does not match")
	request := domain.NewID()
	_, err := s.Mutate(context.Background(), request, "fixture.abort-dispatch", session, func(tx *Tx) (any, error) {
		if _, err := tx.ClaimInitialExecution(session, 1, input, 1); err != nil {
			return nil, err
		}
		return nil, abort
	})
	if err == nil {
		t.Fatal("injected publication failure was accepted")
	}
	if current := readExecutionSession(t, s, session); current.InitialExecution != nil || current.ActiveExecutionID != "" || current.Dispatch != domain.DispatchReady {
		t.Fatal("aborted dispatch partially froze configuration")
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		r, _, err := tx.Routing(f.agent)
		if r.ID != "" {
			t.Fatal("aborted selection advanced routing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	request = domain.NewID()
	if _, err := f.claim(request, session, input); err != nil {
		t.Fatal(err)
	}
	accepted := readExecutionSession(t, s, session)
	if accepted.InitialExecution == nil || accepted.InitialExecution.Configuration.Instructions != "Edited after creation, before dispatch." || accepted.InitialExecution.Configuration.Templates[0].Revision != 2 || accepted.InitialExecution.InitialAccountID != f.accounts[0] || accepted.Outcome != domain.ExecutionNotStarted || accepted.Dispatch != domain.DispatchClaimed || accepted.PendingInputs != 1 {
		t.Fatal("first dispatch lost snapshot, actual selection or claim-only semantics")
	}
	ir, err := s.Get(context.Background(), domain.QueueKind, input)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := Decode[domain.QueuedInput](ir)
	if err != nil || queued.Delivery != domain.InputClaimed || queued.ExecutionID != accepted.ActiveExecutionID || queued.NativeRequestID.Validate() != nil {
		t.Fatal("input claim and native request identity did not commit")
	}
	before, _ := json.Marshal(accepted.InitialExecution)
	editTemplate("A later edit must not replace retained instructions.")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.store = reopened
	result, err := f.claim(request, session, input)
	if err != nil || !result.Replayed {
		t.Fatalf("claim receipt did not survive restart: %v", err)
	}
	after, _ := json.Marshal(readExecutionSession(t, reopened, session).InitialExecution)
	if string(after) != string(before) {
		t.Fatal("later template edit or retry rewrote immutable execution")
	}
	if _, err := f.claim(domain.NewID(), session, input); err == nil {
		t.Fatal("another request replaced an established first execution")
	}
}

func TestConcurrentInitialExecutionPersistsRotationWithoutPreviewReservations(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	const count = 12
	ids := [][2]domain.ID{}
	for range count {
		session, input := f.session(t, domain.DispatchReady)
		ids = append(ids, [2]domain.ID{session, input})
	}
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for _, pair := range ids {
		wg.Go(func() {
			_, err := f.claim(domain.NewID(), pair[0], pair[1])
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	counts := map[domain.ID]int{}
	for _, pair := range ids {
		counts[readExecutionSession(t, s, pair[0]).InitialExecution.InitialAccountID]++
	}
	if counts[f.accounts[0]] != count/2 || counts[f.accounts[1]] != count/2 {
		t.Fatal("concurrent dispatch lost persistent rotation updates")
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		before, state, err := tx.Routing(f.agent)
		if err != nil {
			return err
		}
		_, agent, err := decodeEntity[domain.Agent](tx, domain.AgentKind, f.agent)
		if err != nil {
			return err
		}
		_, model, err := decodeEntity[domain.Model](tx, domain.ModelKind, f.model)
		if err != nil {
			return err
		}
		accounts := map[domain.ID]domain.Account{}
		for _, id := range f.accounts {
			_, a, err := decodeEntity[domain.Account](tx, domain.AccountKind, id)
			if err != nil {
				return err
			}
			accounts[id] = a
		}
		for range 3 {
			if _, _, err := domain.RouteAccount(f.agent, agent, model, nil, accounts, domain.Priority, state, time.Now().UTC()); err != nil {
				return err
			}
		}
		after, _, err := tx.Routing(f.agent)
		if before.Revision != count || after.Revision != before.Revision || string(before.Data) != string(after.Data) {
			t.Fatal("preview reserved routing or dispatch wrote multiple owners")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInitialExecutionRefusesBlockedSessionsAndChangedEligibility(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	for _, state := range []domain.DispatchState{domain.DispatchBlocked, domain.DispatchPaused, domain.DispatchClaimed} {
		session, input := f.session(t, state)
		_, err := f.claim(domain.NewID(), session, input)
		assertCode(t, err, domain.Conflict)
	}
	session, input := f.session(t, domain.DispatchReady)
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.accounts-revoked", f.accounts, func(tx *Tx) (any, error) {
		for _, id := range f.accounts {
			r, a, err := decodeEntity[domain.Account](tx, domain.AccountKind, id)
			if err != nil {
				return nil, err
			}
			a.Health = domain.AccountRevoked
			if _, err := tx.Put(r.Kind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
		}
		return f.accounts, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.claim(domain.NewID(), session, input)
	assertCode(t, err, domain.MissingInput)
	if readExecutionSession(t, s, session).InitialExecution != nil {
		t.Fatal("revoked accounts froze a session configuration")
	}
}

func TestInitialExecutionClaimCannotSkipOrRaceInputEdits(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	session, input := f.session(t, domain.DispatchReady)
	second := domain.NewID()
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.second-input", second, func(tx *Tx) (any, error) {
		return tx.Put(domain.QueueKind, second, 0, session, "", domain.QueuedInput{Sequence: 2, ContentRevision: 1, Prompt: "next", Mode: domain.PlanMode, Delivery: domain.InputQueued})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.claim(domain.NewID(), session, second)
	assertCode(t, err, domain.Conflict)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Go(func() {
		_, err := f.claim(domain.NewID(), session, input)
		results <- err
	})
	wg.Go(func() {
		_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.edit-before-claim", input, func(tx *Tx) (any, error) {
			r, queued, err := decodeEntity[domain.QueuedInput](tx, domain.QueueKind, input)
			if err != nil {
				return nil, err
			}
			if queued.Delivery != domain.InputQueued {
				return nil, domain.Fail(domain.Conflict, "Input was claimed.", "Preserve accepted delivery.")
			}
			queued.Prompt = "edited prompt"
			queued.ContentRevision++
			return tx.Put(r.Kind, r.ID, 1, r.SessionID, r.ProjectID, queued)
		})
		results <- err
	})
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			assertCode(t, err, domain.Conflict)
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatal("input edit and stale claim were both accepted")
	}
}

func TestInitialExecutionRechecksProjectRestrictions(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	session, input := f.session(t, domain.DispatchReady)
	projectID := domain.NewID()
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.restricted-project", projectID, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProjectKind, projectID, 0, "", "", domain.Project{Name: "Restricted", Accounts: domain.Restriction{Configured: true}}); err != nil {
			return nil, err
		}
		r, current, err := decodeEntity[domain.Session](tx, domain.SessionKind, session)
		if err != nil {
			return nil, err
		}
		current.ProjectID = projectID
		current.Workspace = domain.Worktree
		return tx.Put(r.Kind, r.ID, r.Revision, r.ID, projectID, current)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.restricted-claim", session, func(tx *Tx) (any, error) {
		return tx.ClaimInitialExecution(session, 2, input, 1)
	})
	assertCode(t, err, domain.MissingInput)
	if readExecutionSession(t, s, session).InitialExecution != nil {
		t.Fatal("project restrictions were bypassed by earlier readiness")
	}
}

func TestContradictoryRoutingOwnersRequireRecovery(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.legacy-routing", f.agent, func(tx *Tx) (any, error) {
		for range 2 {
			if _, err := tx.Put(domain.RoutingKind, domain.NewID(), 0, "", "", domain.AgentRouting{AgentID: f.agent}); err != nil {
				return nil, err
			}
		}
		return f.agent, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session, input := f.session(t, domain.DispatchReady)
	_, err = f.claim(domain.NewID(), session, input)
	assertCode(t, err, domain.RecoveryRequired)
}
