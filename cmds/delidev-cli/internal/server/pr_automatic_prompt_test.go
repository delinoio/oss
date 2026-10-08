// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func automaticPromptFixture(t *testing.T, bodies []string) *automaticPRFixture {
	t.Helper()
	f := newAutomaticPRFixture(t)
	f.savePolicy(t, func(p *domain.RemediationPolicy) {
		p.ReviewFeedback = true
		p.ReviewerSelectors = []domain.ReviewerSelector{{Kind: domain.ReviewerUser, ID: "23", NodeID: "U_23"}}
	})
	automaticPagedFeedback(f, 0, len(bodies))
	previous := f.service.githubQueries
	f.service.githubQueries = queryFunc(func(ctx context.Context, token []byte, owner, name string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		v, err := previous.QueryRepository(ctx, token, owner, name, q)
		if err == nil && v.Reviewers != nil {
			// This fixture has only selected authors, unlike the mixed denied/eligible pagination fixture.
			v.Reviewers.Actors = v.Reviewers.Actors[1:]
			for i := range v.Reviewers.Feedback.Entries {
				e := &v.Reviewers.Feedback.Entries[i]
				e.Body = bodies[i]
				e.ContentVersion = e.Version()
				v.Reviewers.Applications[i].ContentVersion = e.ContentVersion
			}
		}
		return v, err
	})
	if err := f.service.collectAutomaticPRKind(f.owner, f.link, domain.PRFeedbackProblem); err != nil {
		t.Fatal(err)
	}
	return f
}
func automaticPromptOriginals(t *testing.T, f *automaticPRFixture) (domain.PRFixRequest, []domain.PRProblem, domain.PRGitTarget, map[domain.PRProblemKind]domain.RepositoryQueryResult) {
	t.Helper()
	link, _ := store.Decode[domain.SessionPullRequest](f.link)
	target, observed, err := f.service.automaticPRObservations(f.owner, link, []domain.PRProblemKind{domain.PRFeedbackProblem})
	if err != nil {
		t.Fatal(err)
	}
	var input domain.PRFixRequest
	var problems []domain.PRProblem
	err = f.service.Store.Read(f.owner, func(tx *store.Tx) error {
		row, _, err := tx.FindPRProblemSet(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
		if err != nil {
			return err
		}
		input = domain.PRFixRequest{SetID: row.ID, SetRevision: row.Revision, ProjectID: f.project, RepositoryID: f.repo}
		rows, _, err := tx.ListPRProblems(row.ID, "", 50)
		if err != nil {
			return err
		}
		for _, r := range rows {
			p, err := store.Decode[domain.PRProblem](r)
			if err != nil {
				return err
			}
			problems = append(problems, p)
			input.Problems = append(input.Problems, domain.PRFixProblem{ID: r.ID, Revision: r.Revision, ContentVersion: p.ContentVersion})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return input, problems, target, observed
}
func TestAutomaticPRPromptSelectsCompleteFittingOriginals(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		bodies []string
		want   int
		code   domain.Code
	}{
		{name: "five-60KiB", bodies: []string{strings.Repeat("a", 60<<10), strings.Repeat("b", 60<<10), strings.Repeat("c", 60<<10), strings.Repeat("d", 60<<10), strings.Repeat("e", 60<<10)}, want: 4},
		{name: "unfit-first-later-fit", bodies: []string{strings.Repeat("<", 60<<10), "Complete later evidence"}, want: 1},
		{name: "all-unfit", bodies: []string{strings.Repeat("<", 60<<10), strings.Repeat("&", 60<<10)}, code: domain.ResourceExhausted},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := automaticPromptFixture(t, scenario.bodies)
			input, originals, _, _ := automaticPromptOriginals(t, f)
			before := f.calls.Load()
			err := f.service.requestAutomaticPRFix(f.owner, f.link, map[domain.PRProblemKind]bool{domain.PRFeedbackProblem: true})
			if scenario.code != "" {
				if domain.SafeError(err).Code != scenario.code {
					t.Fatal("wrong no-fit refusal", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if f.calls.Load()-before != 2 {
				t.Fatal("source observations repeated")
			}
			rows, _ := f.attempts(t)
			if scenario.want == 0 {
				if len(rows) != 0 {
					t.Fatal("empty attempt accepted")
				}
				return
			}
			if len(rows) != 1 {
				t.Fatal("missing fitting attempt", len(rows))
			}
			attempt, err := store.Decode[domain.PRRemediationAttempt](rows[0])
			if err != nil || len(attempt.Problems) != scenario.want {
				t.Fatal("wrong fitting subset", err)
			}
			queue, err := f.service.Store.Get(f.owner, domain.QueueKind, attempt.InputID)
			if err != nil {
				t.Fatal(err)
			}
			queued, err := store.Decode[domain.QueuedInput](queue)
			if err != nil || len(queued.Prompt) > 256<<10 {
				t.Fatal("oversized accepted prompt", err)
			}
			var selected []domain.PRProblem
			for _, ref := range attempt.Problems {
				for i, p := range originals {
					if input.Problems[i].ID == ref.ID {
						selected = append(selected, p)
					}
				}
			}
			fix := domain.PRFixExecution{AttemptID: rows[0].ID, Target: *attempt.GitTarget, Strategy: attempt.Policy.ConflictStrategy}
			exact, err := domain.PRFixPrompt(fix, selected)
			if err != nil || queued.Prompt != exact {
				t.Fatal("complete evidence formatter changed", err)
			}
			for i, p := range originals {
				r, err := f.service.Store.Get(f.owner, domain.ProblemKind, input.Problems[i].ID)
				current, e := store.Decode[domain.PRProblem](r)
				if err != nil || e != nil || current.State != domain.PRProblemUnhandled || current.ContentVersion != p.ContentVersion || current.Feedback.Body != p.Feedback.Body {
					t.Fatal("selection handled or truncated original")
				}
			}
			// Active original ownership still blocks a second request without provider reads.
			before = f.calls.Load()
			if err := f.service.requestAutomaticPRFix(f.owner, f.link, map[domain.PRProblemKind]bool{domain.PRFeedbackProblem: true}); err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != before {
				t.Fatal("active chain exclusion lost")
			}
		})
	}
}
func TestAutomaticPRPromptExactMultibyteBoundaryAndManualOverflow(t *testing.T) {
	f := automaticPromptFixture(t, []string{strings.Repeat("界", 16000), strings.Repeat("界", 16000), strings.Repeat("界", 16000), strings.Repeat("界", 16000), strings.Repeat("界", 16000)})
	input, problems, target, observed := automaticPromptOriginals(t, f)
	preview := domain.PRFixExecution{AttemptID: domain.NewID(), Target: target, Strategy: f.policy.ConflictStrategy}
	baseline, err := domain.PRFixPrompt(preview, problems)
	if err != nil {
		t.Fatal(err)
	}
	last := len(problems) - 1
	feedback := *problems[last].Feedback
	feedback.Body += strings.Repeat("x", (256<<10)-len(baseline))
	feedback.ContentVersion = feedback.Version()
	problems[last].Feedback = &feedback
	problems[last].ContentVersion = feedback.ContentVersion
	exact, err := domain.PRFixPrompt(preview, problems)
	if err != nil || len(exact) != 256<<10 {
		t.Fatal("exact UTF-8 wrapper boundary", len(exact), err)
	}
	feedback.Body += "x"
	feedback.ContentVersion = feedback.Version()
	problems[last].ContentVersion = feedback.ContentVersion
	if _, err := domain.PRFixPrompt(preview, problems); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("one-byte overflow accepted", err)
	}
	// The ordinary manual acceptance still rejects a complete oversized selection.
	oversized := automaticPromptFixture(t, []string{strings.Repeat("a", 60<<10), strings.Repeat("b", 60<<10), strings.Repeat("c", 60<<10), strings.Repeat("d", 60<<10), strings.Repeat("e", 60<<10)})
	input, _, target, observed = automaticPromptOriginals(t, oversized)
	manual := observed[domain.PRFeedbackProblem]
	manual.Query.Operation = domain.RepositoryDetail
	manual.Feedback, manual.Reviewers = nil, nil
	observed[domain.PRFeedbackProblem] = manual
	selected, err := oversized.service.selectPRFixSession(oversized.owner, input, target, oversized.policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = oversized.service.acceptPRFix(oversized.owner, domain.NewID(), "fixture.manual-oversize", input, input, target, observed, selected, oversized.policy, domain.PRRemediationManual, nil)
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("manual overflow changed", err)
	}
	rows, _ := oversized.attempts(t)
	if len(rows) != 0 {
		t.Fatal("manual overflow published attempt")
	}
}
func TestAutomaticPRPromptAtomicOriginalChangesReject(t *testing.T) {
	for _, change := range []string{"version", "head"} {
		t.Run(change, func(t *testing.T) {
			f := automaticPromptFixture(t, []string{"Complete original evidence"})
			input, problems, target, observed := automaticPromptOriginals(t, f)
			if _, err := domain.PRFixPrompt(domain.PRFixExecution{AttemptID: domain.NewID(), Target: target, Strategy: f.policy.ConflictStrategy}, problems); err != nil {
				t.Fatal(err)
			}
			selected, err := f.service.selectPRFixSession(f.owner, input, target, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.changed-original", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.ProblemKind, input.Problems[0].ID)
				if err != nil {
					return nil, err
				}
				p, err := store.Decode[domain.PRProblem](r)
				if err != nil {
					return nil, err
				}
				if change == "head" {
					p.Observation.HeadSHA = strings.Repeat("f", 40)
				} else {
					feedback := *p.Feedback
					feedback.Body += " changed"
					feedback.ContentVersion = feedback.Version()
					p.Feedback, p.ContentVersion = &feedback, feedback.ContentVersion
				}
				return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, p)
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.service.acceptPRFix(f.owner, domain.NewID(), "fixture.stale-preview", input, input, target, observed, selected, f.policy, domain.PRRemediationAutomatic, &f.link)
			if domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("changed original admitted", err)
			}
			rows, _ := f.attempts(t)
			if len(rows) != 0 {
				t.Fatal("stale preview published attempt")
			}
		})
	}
}

// Controlled durable completion exercises the ordinary independent settlement
// gates, not native inference or a real Git push.
func TestAutomaticPRPromptCompletedAttemptLeavesRemainderForNextPoll(t *testing.T) {
	f := automaticPromptFixture(t, []string{strings.Repeat("a", 60<<10), strings.Repeat("b", 60<<10), strings.Repeat("c", 60<<10), strings.Repeat("d", 60<<10), strings.Repeat("e", 60<<10)})
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.SessionStrategy = domain.DedicatedSession })
	kinds := map[domain.PRProblemKind]bool{domain.PRFeedbackProblem: true}
	if err := f.service.requestAutomaticPRFix(f.owner, f.link, kinds); err != nil {
		t.Fatal(err)
	}
	attempts, _ := f.attempts(t)
	if len(attempts) != 1 {
		t.Fatal("first attempt absent")
	}
	first := attempts[0]
	_, _, _, observations := automaticPromptOriginals(t, f)
	_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.fitting-attempt-start", nil, func(tx *store.Tx) (any, error) {
		_, a, err := tx.GetPRRemediationAttempt(first.ID)
		if err != nil {
			return nil, err
		}
		sr, err := tx.Get(domain.SessionKind, a.SessionID)
		if err != nil {
			return nil, err
		}

		// Only synthetic workspace completion is installed here; no manager,
		// native Git or real account executes in this durable settlement test.
		session, err := store.Decode[domain.Session](sr)
		if err != nil {
			return nil, err
		}
		preparationRow, err := tx.Get(domain.JobKind, session.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		preparation, err := store.Decode[domain.Job](preparationRow)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		preparation.State = domain.JobSucceeded
		preparation.FinishedAt = &now
		preparation.Output = json.RawMessage(`{}`)
		if _, err := tx.PutJob(preparationRow.ID, preparationRow.Revision, sr.ID, f.project, preparation); err != nil {
			return nil, err
		}
		session.Preparation.State = domain.PreparationReady
		session.Dispatch = domain.DispatchReady
		sr, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, f.project, session)
		if err != nil {
			return nil, err
		}
		qr, err := tx.Get(domain.QueueKind, a.InputID)
		if err != nil {
			return nil, err
		}
		claim, err := tx.ClaimInitialExecution(a.SessionID, sr.Revision, a.InputID, qr.Revision)
		if err != nil {
			return nil, err
		}
		first, err = tx.StartPRRemediation(first.ID, first.Revision, claim.ID, observations)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.fitting-attempt-finish", nil, func(tx *store.Tx) (any, error) {
		_, a, err := tx.GetPRRemediationAttempt(first.ID)
		if err != nil {
			return nil, err
		}
		sr, err := tx.Get(domain.SessionKind, a.SessionID)
		if err != nil {
			return nil, err
		}
		session, err := store.Decode[domain.Session](sr)
		if err != nil {
			return nil, err
		}
		qr, err := tx.Get(domain.QueueKind, a.InputID)
		if err != nil {
			return nil, err
		}
		queued, err := store.Decode[domain.QueuedInput](qr)
		if err != nil {
			return nil, err
		}
		claim := session.InitialExecution
		selection, err := tx.PRFixSelection(first.ID)
		if err != nil {
			return nil, err
		}
		input := domain.ExecutionJobInput{Version: 1, SessionID: a.SessionID, MachineID: session.MachineID, ExecutionID: a.ExecutionID, InputID: a.InputID, ThreadRequestID: domain.NewID(), TurnRequestID: queued.NativeRequestID, Configuration: claim.Configuration, ConfigurationDigest: claim.ConfigurationDigest, AccountID: claim.InitialAccountID, ConnectionID: claim.ConnectionID, Input: domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}, Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`), Remediation: &selection}
		if err := input.Validate(); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		done := domain.ExecutionCompletion{Version: 1, ExecutionID: a.ExecutionID, InputID: a.InputID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, PRPush: &domain.PRPushProof{Version: 1, AttemptID: first.ID, ExecutionID: a.ExecutionID, SelectionDigest: selection.Digest(), State: domain.PRPushVerified, PreviousHead: selection.Target.HeadSHA, ResultHead: strings.Repeat("c", 40), ObservedAt: now}}
		inputRaw, _ := json.Marshal(input)
		outputRaw, _ := json.Marshal(done)
		job, err := tx.PutJob(domain.NewID(), 0, a.SessionID, f.project, domain.Job{Type: domain.ExecuteSessionJob, MachineID: session.MachineID, State: domain.JobSucceeded, Input: inputRaw, Output: outputRaw, AcceptedAt: now, FinishedAt: &now})
		if err != nil {
			return nil, err
		}
		session.Execution = &domain.ExecutionProgress{JobID: job.ID, ExecutionID: a.ExecutionID, InputID: a.InputID, NativeThreadID: string(done.NativeThreadID), NativeTurnID: string(done.NativeTurnID), LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
		session.ActiveExecutionID, session.Dispatch, session.Outcome = "", domain.DispatchReady, domain.ExecutionSucceeded
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, f.project, session); err != nil {
			return nil, err
		}
		_, err = tx.FinishPRRemediation(first.ID, first.Revision)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts, set := f.attempts(t)
	firstValue, err := store.Decode[domain.PRRemediationAttempt](attempts[0])
	if err != nil || firstValue.State != domain.PRRemediationFinished || set.Remediation.ActiveAttemptID != "" || set.Remediation.AutomaticAttempts != 1 {
		t.Fatal("original settlement incomplete", err)
	}
	if err := f.service.requestAutomaticPRFix(f.owner, f.link, kinds); err != nil {
		t.Fatal(err)
	}
	attempts, set = f.attempts(t)
	if len(attempts) != 2 || set.Remediation.AutomaticAttempts != 1 {
		t.Fatal("later poll lost original chain limits")
	}
	next, err := store.Decode[domain.PRRemediationAttempt](attempts[0])
	if err != nil || next.State != domain.PRRemediationBound || len(next.Problems) != 1 {
		t.Fatal("remainder did not progress", err)
	}
	for _, prior := range firstValue.Problems {
		if prior.ID == next.Problems[0].ID {
			t.Fatal("handled original replayed")
		}
	}
	row, err := f.service.Store.Get(f.owner, domain.ProblemKind, next.Problems[0].ID)
	problem, e := store.Decode[domain.PRProblem](row)
	if err != nil || e != nil || problem.State != domain.PRProblemUnhandled || problem.Feedback.Body != strings.Repeat("e", 60<<10) {
		t.Fatal("complete remainder changed")
	}
}
