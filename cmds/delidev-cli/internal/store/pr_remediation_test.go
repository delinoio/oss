package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// These are durable coordination/claim fixtures, not native execution or Git
// push evidence. Real dispatch retains its separate Worker/workspace checks.
type remediationStoreFixture struct {
	s           *Store
	exec        executionFixture
	set         Record
	project     domain.ID
	policy      domain.RemediationPolicy
	observation domain.RepositoryQueryResult
	problems    []domain.PRRemediationProblemRef
}

func newRemediationStoreFixture(t *testing.T, s *Store) *remediationStoreFixture {
	t.Helper()
	f := &remediationStoreFixture{s: s, exec: newExecutionFixture(t, s), project: domain.NewID(), policy: domain.DefaultRemediationPolicy(), observation: ciStoreObservationFixture()}
	f.policy.MergeConflict = true
	f.observation.Query.Operation = domain.RepositoryDetail
	f.observation.CI = nil
	f.observation.ObservedAt = time.Now().UTC().Add(-time.Second)
	f.set = collectCIStoreFixture(t, s, 0, f.observation, true)
	rows := readProblemFixture(t, s, f.set.ID)
	p, _ := Decode[domain.PRProblem](rows[0])
	f.problems = []domain.PRRemediationProblemRef{{ID: rows[0].ID, ContentVersion: p.ContentVersion}}
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.remediation-config", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.RepositoryKind, f.observation.RepositoryID, 0, "", "", domain.Repository{Name: "Fixture", Checkouts: []domain.Checkout{{MachineID: f.exec.machine, Path: "/tmp/synthetic-remediation-checkout"}}, GitHubOwner: "fixture-owner", GitHubName: "repo", IntegrationID: f.observation.ProfileID, Remediation: &f.policy}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.IntegrationKind, f.observation.ProfileID, 0, "", "", domain.Integration{IntegrationDefinition: domain.IntegrationDefinition{Name: "Fixture", Provider: domain.GitHubCom, TokenKind: domain.FineGrainedPAT, ResourceOwner: "fixture-owner"}, Connection: &domain.IntegrationConnection{GenerationID: f.observation.GenerationID, ConnectedAt: f.observation.ObservedAt}}); err != nil {
			return nil, err
		}
		return tx.Put(domain.ProjectKind, f.project, 0, "", "", domain.Project{Name: "Fixture", Repositories: []domain.ID{f.observation.RepositoryID}, PrimaryRepository: f.observation.RepositoryID})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *remediationStoreFixture) reserve(t *testing.T, mode domain.PRRemediationMode) (Record, error) {
	t.Helper()
	var attempt Record
	_, err := f.s.Mutate(notificationOwner(), domain.NewID(), "fixture.remediation-reserve", nil, func(tx *Tx) (any, error) {
		set, _, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return nil, err
		}
		attempt, err = tx.ReservePRRemediation(set.ID, set.Revision, mode, f.policy, f.problems)
		return struct{ ID domain.ID }{attempt.ID}, err
	})
	return attempt, err
}

func (f *remediationStoreFixture) bind(t *testing.T, attempt Record) Record {
	t.Helper()
	session, input := f.exec.session(t, domain.DispatchReady)
	var saved Record
	_, err := f.s.Mutate(notificationOwner(), domain.NewID(), "fixture.remediation-bind", nil, func(tx *Tx) (any, error) {
		sr, _ := tx.Get(domain.SessionKind, session)
		s, _ := Decode[domain.Session](sr)
		s.Workspace, s.ProjectID = domain.Worktree, f.project
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, f.project, s); err != nil {
			return nil, err
		}
		ir, _ := tx.Get(domain.QueueKind, input)
		if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, f.project, ir.Data); err != nil {
			return nil, err
		}
		var err error
		saved, err = tx.BindPRRemediation(attempt.ID, attempt.Revision, session, input)
		return struct{ ID domain.ID }{saved.ID}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func (f *remediationStoreFixture) start(t *testing.T, attempt Record, request domain.ID, observed domain.RepositoryQueryResult) (Record, Result, error) {
	t.Helper()
	var saved Record
	result, err := f.s.Mutate(notificationOwner(), request, "fixture.remediation-start", struct{ Attempt domain.ID }{attempt.ID}, func(tx *Tx) (any, error) {
		_, v, err := tx.GetPRRemediationAttempt(attempt.ID)
		if err != nil {
			return nil, err
		}
		sr, err := tx.Get(domain.SessionKind, v.SessionID)
		if err != nil {
			return nil, err
		}
		ir, err := tx.Get(domain.QueueKind, v.InputID)
		if err != nil {
			return nil, err
		}
		claim, err := tx.ClaimInitialExecution(v.SessionID, sr.Revision, v.InputID, ir.Revision)
		if err != nil {
			return nil, err
		}
		saved, err = tx.StartPRRemediation(attempt.ID, attempt.Revision, claim.ID, map[domain.PRProblemKind]domain.RepositoryQueryResult{domain.PRMergeConflictProblem: observed})
		return struct{ ID domain.ID }{saved.ID}, err
	})
	return saved, result, err
}

func (f *remediationStoreFixture) finish(t *testing.T, attempt Record, verified bool, mutations ...func(*domain.Job)) Record {
	t.Helper()
	var saved Record
	_, err := f.s.Mutate(notificationOwner(), domain.NewID(), "fixture.remediation-finish", nil, func(tx *Tx) (any, error) {
		_, v, err := tx.GetPRRemediationAttempt(attempt.ID)
		if err != nil {
			return nil, err
		}
		if verified {
			sr, _ := tx.Get(domain.SessionKind, v.SessionID)
			s, _ := Decode[domain.Session](sr)
			claim := s.InitialExecution
			ir, _ := tx.Get(domain.QueueKind, v.InputID)
			queued, _ := Decode[domain.QueuedInput](ir)
			input := domain.ExecutionJobInput{Version: 1, SessionID: v.SessionID, MachineID: f.exec.machine, ExecutionID: v.ExecutionID, InputID: v.InputID, ThreadRequestID: domain.NewID(), TurnRequestID: queued.NativeRequestID, Configuration: claim.Configuration, ConfigurationDigest: claim.ConfigurationDigest, AccountID: claim.InitialAccountID, ConnectionID: claim.ConnectionID, Input: domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}, Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
			if input.Validate() != nil {
				t.Fatal("fixture assignment", input.Validate())
			}
			done := domain.ExecutionCompletion{Version: 1, ExecutionID: v.ExecutionID, InputID: v.InputID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
			inputRaw, _ := json.Marshal(input)
			outputRaw, _ := json.Marshal(done)
			fixtureJob := domain.Job{Type: domain.ExecuteSessionJob, MachineID: f.exec.machine, State: domain.JobSucceeded, Input: inputRaw, Output: outputRaw, AcceptedAt: tx.now, FinishedAt: &tx.now}
			for _, mutate := range mutations {
				mutate(&fixtureJob)
			}
			job, err := tx.PutJob(domain.NewID(), 0, v.SessionID, f.project, fixtureJob)
			if err != nil {
				return nil, err
			}
			s.Execution = &domain.ExecutionProgress{JobID: job.ID, ExecutionID: v.ExecutionID, InputID: v.InputID, NativeThreadID: string(done.NativeThreadID), NativeTurnID: string(done.NativeTurnID), LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
			s.ActiveExecutionID, s.Dispatch, s.Outcome = "", domain.DispatchReady, domain.ExecutionSucceeded
			if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, f.project, s); err != nil {
				return nil, err
			}
		}
		saved, err = tx.FinishPRRemediation(attempt.ID, attempt.Revision)
		return struct{ ID domain.ID }{saved.ID}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func (f *remediationStoreFixture) chain(t *testing.T) domain.PRRemediationChain {
	t.Helper()
	r, err := f.s.Get(context.Background(), domain.ProblemKind, f.set.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decode[domain.PRProblemSet](r)
	if err != nil || v.Validate() != nil || v.Remediation == nil {
		t.Fatal("chain", err)
	}
	return *v.Remediation
}

func TestPRRemediationBudgetSurvivesRestartReplacementHeadsAndManualWork(t *testing.T) {
	s, root := openTest(t)
	f := newRemediationStoreFixture(t, s)
	var original domain.ID
	for i := uint32(1); i <= 3; i++ {
		a, err := f.reserve(t, domain.PRRemediationAutomatic)
		if err != nil {
			t.Fatal(err)
		}
		if f.chain(t).AutomaticAttempts != i-1 {
			t.Fatal("reservation consumed attempt")
		}
		if i == 1 {
			original = f.chain(t).ID
		}
		a = f.bind(t, a)
		request := domain.NewID()
		a, _, err = f.start(t, a, request, f.observation)
		if err != nil {
			t.Fatal(err)
		}
		_, result, err := f.start(t, a, request, f.observation)
		if err != nil || !result.Replayed {
			t.Fatal("claim receipt", err)
		}
		if f.chain(t).AutomaticAttempts != i {
			t.Fatal("retry double charged")
		}
		f.finish(t, a, true)
		if f.chain(t).ID != original || f.chain(t).ActiveAttemptID != "" {
			t.Fatal("chain replacement or stuck ownership")
		}
		if i == 2 {
			s.Close()
			s, err = Open(notificationOwner(), root)
			if err != nil {
				t.Fatal(err)
			}
			f.s, f.exec.store = s, s
			f.observation.Items[0].HeadSHA = strings.Repeat("c", 40)
			set, _ := s.Get(context.Background(), domain.ProblemKind, f.set.ID)
			f.set = collectCIStoreFixture(t, s, set.Revision, f.observation, true)
			rows := readProblemFixture(t, s, f.set.ID)
			p, _ := Decode[domain.PRProblem](rows[len(rows)-1])
			f.problems = []domain.PRRemediationProblemRef{{ID: rows[len(rows)-1].ID, ContentVersion: p.ContentVersion}}
		}
	}
	defer s.Close()
	a, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil || a.ID != "" {
		t.Fatal("fourth automatic attempt", a.ID, err)
	}
	c := f.chain(t)
	if c.AutomaticAttempts != 3 || c.Limit == nil || c.Limit.Limit != 3 {
		t.Fatal("limit cause not retained")
	}
	manual, err := f.reserve(t, domain.PRRemediationManual)
	if err != nil {
		t.Fatal(err)
	}
	manual = f.bind(t, manual)
	manual, _, err = f.start(t, manual, domain.NewID(), f.observation)
	if err != nil {
		t.Fatal(err)
	}
	f.finish(t, manual, true)
	if f.chain(t).AutomaticAttempts != 3 || f.chain(t).Limit == nil {
		t.Fatal("manual action reset automatic budget")
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.remediation-resume", nil, func(tx *Tx) (any, error) {
		r, _, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return nil, err
		}
		return tx.ResumePRRemediation(r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	c = f.chain(t)
	if c.ID != original || c.AutomaticAttempts != 3 || c.ResumeBaseline != 3 || c.LastResume == nil || c.Limit != nil {
		t.Fatal("explicit resume erased history")
	}
	if a, err = f.reserve(t, domain.PRRemediationAutomatic); err != nil || a.ID == "" {
		t.Fatal("resume did not permit next window", err)
	}
}

func TestPRRemediationUncertaintyKeepsSingleOwnerAndCannotBeResumed(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	f := newRemediationStoreFixture(t, s)
	a, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.reserve(t, domain.PRRemediationManual); err == nil {
		t.Fatal("duplicate active PR")
	}
	a = f.bind(t, a)
	a, _, err = f.start(t, a, domain.NewID(), f.observation)
	if err != nil {
		t.Fatal(err)
	}
	a = f.finish(t, a, false)
	v, _ := Decode[domain.PRRemediationAttempt](a)
	if v.State != domain.PRRemediationUncertain || f.chain(t).ActiveAttemptID != a.ID {
		t.Fatal("unverified cleanup freed PR")
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.bad-resume", nil, func(tx *Tx) (any, error) {
		r, _, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return nil, err
		}
		return tx.ResumePRRemediation(r.ID, r.Revision)
	})
	if err == nil {
		t.Fatal("resume overrode uncertain ownership")
	}
	if _, err = f.reserve(t, domain.PRRemediationAutomatic); err == nil {
		t.Fatal("uncertain attempt allowed replacement")
	}
	f.finish(t, a, true)
	if f.chain(t).ActiveAttemptID != "" || f.chain(t).AutomaticAttempts != 1 {
		t.Fatal("reconciliation reset chain")
	}
}

func TestPRRemediationFreshGateRollsBackOrdinaryClaimAndRouting(t *testing.T) {
	for _, scenario := range []string{"bypass", "stale", "unknown", "head", "dismissed", "profile", "policy", "paused", "archive", "input-edit", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			defer s.Close()
			f := newRemediationStoreFixture(t, s)
			a, err := f.reserve(t, domain.PRRemediationAutomatic)
			if err != nil {
				t.Fatal(err)
			}
			a = f.bind(t, a)
			v, _ := Decode[domain.PRRemediationAttempt](a)
			o := f.observation
			switch scenario {
			case "stale":
				o.ObservedAt = time.Now().Add(-time.Minute)
			case "unknown":
				o.Items[0].Mergeable = nil
			case "head":
				o.Items[0].HeadSHA = strings.Repeat("d", 40)
			case "malformed":
				o.Items = nil
			case "dismissed", "profile", "policy", "paused", "archive", "input-edit":
				_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.changed-remediation", nil, func(tx *Tx) (any, error) {
					switch scenario {
					case "dismissed":
						p, value, err := tx.GetPRProblem(f.problems[0].ID)
						if err != nil {
							return nil, err
						}
						return tx.DismissPRProblem(p.ID, p.Revision, value.ContentVersion)
					case "profile":
						r, _ := tx.Get(domain.IntegrationKind, o.ProfileID)
						p, _ := Decode[domain.Integration](r)
						p.Connection.GenerationID = domain.NewID()
						return tx.Put(r.Kind, r.ID, r.Revision, "", "", p)
					case "policy":
						r, _ := tx.Get(domain.RepositoryKind, o.RepositoryID)
						p, _ := Decode[domain.Repository](r)
						p.Remediation.MergeConflict = false
						return tx.Put(r.Kind, r.ID, r.Revision, "", "", p)
					case "input-edit":
						r, _ := tx.Get(domain.QueueKind, v.InputID)
						p, _ := Decode[domain.QueuedInput](r)
						p.Prompt = "Changed selected prompt"
						p.ContentRevision++
						return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, p)
					default:
						r, _ := tx.Get(domain.SessionKind, v.SessionID)
						p, _ := Decode[domain.Session](r)
						if scenario == "paused" {
							p.Dispatch = domain.DispatchPaused
						} else {
							p.Archive = domain.Archived
						}
						return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, p)
					}
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "bypass" {
				_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.bypass-gate", nil, func(tx *Tx) (any, error) {
					sr, _ := tx.Get(domain.SessionKind, v.SessionID)
					ir, _ := tx.Get(domain.QueueKind, v.InputID)
					return tx.ClaimInitialExecution(v.SessionID, sr.Revision, v.InputID, ir.Revision)
				})
			} else {
				_, _, err = f.start(t, a, domain.NewID(), o)
			}
			if err == nil {
				t.Fatal("invalid gate accepted")
			}
			retained := readExecutionSession(t, s, v.SessionID)
			if retained.InitialExecution != nil || retained.ActiveExecutionID != "" || f.chain(t).AutomaticAttempts != 0 {
				t.Fatal("failed gate partially committed execution")
			}
		})
	}
}

func TestPRRemediationCancelNeedsRemovedQueueAndHigherPolicyKeepsCount(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	f := newRemediationStoreFixture(t, s)
	a, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	a = f.bind(t, a)
	v, _ := Decode[domain.PRRemediationAttempt](a)
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.premature-cancel", nil, func(tx *Tx) (any, error) { return tx.CancelPRRemediation(a.ID, a.Revision) })
	if err == nil {
		t.Fatal("cancel left runnable input")
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.cancel", nil, func(tx *Tx) (any, error) {
		r, _ := tx.Get(domain.QueueKind, v.InputID)
		q, _ := Decode[domain.QueuedInput](r)
		q.Delivery = domain.InputRemoved
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, q); err != nil {
			return nil, err
		}
		return tx.CancelPRRemediation(a.ID, a.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.chain(t).AutomaticAttempts != 0 || f.chain(t).ActiveAttemptID != "" {
		t.Fatal("cancellation charged or stuck")
	}
	// Seed a valid exhausted historical counter to isolate policy admission;
	// the complete three-claim lifecycle is covered above.
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.exhausted-budget", nil, func(tx *Tx) (any, error) {
		r, p, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return nil, err
		}
		p.Remediation.Sequence = 3
		p.Remediation.AutomaticAttempts = 3
		return tx.publishPRProblemSet(r, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if a, err = f.reserve(t, domain.PRRemediationAutomatic); err != nil || a.ID != "" {
		t.Fatal(err)
	}
	f.policy.AttemptLimit = 4
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.higher-policy", nil, func(tx *Tx) (any, error) {
		r, _ := tx.Get(domain.RepositoryKind, f.observation.RepositoryID)
		p, _ := Decode[domain.Repository](r)
		p.Remediation = &f.policy
		saved, err := tx.Put(r.Kind, r.ID, r.Revision, "", "", p)
		f.observation.RepositoryRevision = strconv.FormatUint(saved.Revision, 10)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err = f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil || a.ID == "" {
		t.Fatal("higher limit", err)
	}
	if f.chain(t).AutomaticAttempts != 3 || f.chain(t).ResumeBaseline != 0 {
		t.Fatal("policy edit reset count")
	}
}

func TestPRRemediationV19MigrationPreservesOriginalEvidence(t *testing.T) {
	s, root := openTest(t)
	f := newRemediationStoreFixture(t, s)
	before, _ := s.Get(context.Background(), domain.ProblemKind, f.set.ID)
	if _, err := s.db.Exec("DROP TABLE backup_deletions; DROP TABLE pr_remediation_attempts; PRAGMA user_version=19;"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err := Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, _ := s.Get(context.Background(), domain.ProblemKind, f.set.ID)
	if before.Revision != after.Revision || string(before.Data) != string(after.Data) {
		t.Fatal("migration rewrote original set")
	}
	files, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(files) != 1 {
		t.Fatal("backup", err)
	}
	db, err := sql.Open("sqlite", databaseURI(files[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 19 {
		t.Fatal("original backup", version, err)
	}
}

func TestPRRemediationConcurrentReservationsAndWorkerAuthority(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	f := newRemediationStoreFixture(t, s)
	var wait sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); _, err := f.reserve(t, domain.PRRemediationAutomatic); results <- err }()
	}
	wait.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if domain.SafeError(err).Code != domain.Conflict {
			t.Fatal(err)
		}
	}
	if accepted != 1 || f.chain(t).Sequence != 1 || f.chain(t).AutomaticAttempts != 0 {
		t.Fatal("concurrent reservations did not share one owner")
	}
	active := f.chain(t).ActiveAttemptID
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: f.exec.machine})
	if err := s.Read(worker, func(tx *Tx) error { _, _, err := tx.GetPRRemediationAttempt(active); return err }); err == nil {
		t.Fatal("Worker read private coordination")
	}
}

func TestPRRemediationCompletionCannotReleaseChangedOriginalAuthority(t *testing.T) {
	for _, scenario := range []string{"state", "time", "account", "connection", "prompt", "completion"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			defer s.Close()
			f := newRemediationStoreFixture(t, s)
			a, err := f.reserve(t, domain.PRRemediationAutomatic)
			if err != nil {
				t.Fatal(err)
			}
			a = f.bind(t, a)
			a, _, err = f.start(t, a, domain.NewID(), f.observation)
			if err != nil {
				t.Fatal(err)
			}
			a = f.finish(t, a, true, func(j *domain.Job) {
				switch scenario {
				case "state":
					j.State = domain.JobFailed
				case "time":
					before := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
					j.FinishedAt = &before
				case "completion":
					var done domain.ExecutionCompletion
					_ = domain.Decode(j.Output, &done)
					done.LastSequence++
					j.Output, _ = json.Marshal(done)
				default:
					var input domain.ExecutionJobInput
					_ = domain.Decode(j.Input, &input)
					switch scenario {
					case "account":
						for _, account := range input.Configuration.Accounts {
							if account.ID != input.AccountID {
								input.AccountID = account.ID
								break
							}
						}
					case "connection":
						input.ConnectionID = domain.NewID()
					case "prompt":
						input.Input.Prompt = "Different original input"
					}
					j.Input, _ = json.Marshal(input)
				}
			})
			v, _ := Decode[domain.PRRemediationAttempt](a)
			if v.State != domain.PRRemediationUncertain || f.chain(t).ActiveAttemptID != a.ID {
				t.Fatal("foreign completion released PR")
			}
		})
	}
}
