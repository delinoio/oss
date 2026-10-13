package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func budgetFixtureRequest(f *scheduleDispatchFixture, count int, url string) workspace.PrepareRequest {
	request := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: f.machine, Type: domain.Worktree, Repositories: []workspace.RepositorySpec{}}
	for i := 0; i < count; i++ {
		request.Repositories = append(request.Repositories, workspace.RepositorySpec{ID: domain.NewID(), SourceKind: workspace.RemoteCloneSource, RemoteURL: url, DirectoryName: fmt.Sprintf("repository-%03d", i), Base: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}, AutoFetch: true})
	}
	request.PrimaryRepository = request.Repositories[0].ID
	return request
}

func budgetFixtureURL(length int) string {
	prefix, suffix := "https://github.com/fixture/", ".git"
	return prefix + strings.Repeat("a", length-len(prefix)-len(suffix)) + suffix
}

func (f *scheduleDispatchFixture) requireBudget(t *testing.T, request workspace.PrepareRequest, input domain.SessionInput) error {
	t.Helper()
	return f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		return requireExecutionJobBudget(tx, domain.Session{AgentID: f.agent, MachineID: f.machine, Workspace: request.Type}, request, input)
	})
}

func TestExecutionJobBudgetRejectsReportedCombination(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	prompt := domain.SessionInput{Prompt: strings.Repeat("p", domain.MaxPromptBytes), Mode: domain.ExecuteMode}
	reported := budgetFixtureRequest(f, 100, budgetFixtureURL(4028))
	if err := f.requireBudget(t, reported, prompt); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("100 maximum-URL repositories with a maximum prompt were admitted", err)
	}
	// The same project remains dispatchable with a short prompt or one repository.
	if err := f.requireBudget(t, reported, domain.SessionInput{Prompt: "short", Mode: domain.ExecuteMode}); domain.SafeError(err).Code != domain.ResourceExhausted {
		// 100 maximum URLs alone are about 840 KiB, which stays below the limit.
		if err != nil {
			t.Fatal("maximum URLs with a short prompt were rejected", err)
		}
	}
	single := budgetFixtureRequest(f, 1, budgetFixtureURL(4028))
	if err := f.requireBudget(t, single, prompt); err != nil {
		t.Fatal("single repository with a maximum prompt was rejected", err)
	}
	small := budgetFixtureRequest(f, 100, "https://github.com/fixture/repo.git")
	if err := f.requireBudget(t, small, prompt); err != nil {
		t.Fatal("100 ordinary repositories with a maximum prompt were rejected", err)
	}
}

func TestExecutionJobBudgetBoundaryAroundPromptSize(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	request := budgetFixtureRequest(f, 80, budgetFixtureURL(4028))
	var budget domain.ExecutionJobBudget
	if err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		var err error
		budget, err = executionJobBudget(tx, domain.Session{AgentID: f.agent}, request, domain.SessionInput{Prompt: "", Mode: domain.ExecuteMode})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	room := domain.MaxExecutionJobDocumentBytes - budget.JobDocumentBytes()
	if room <= 0 || room >= domain.MaxPromptBytes {
		t.Fatalf("fixture does not straddle the prompt limit: room %d", room)
	}
	if err := f.requireBudget(t, request, domain.SessionInput{Prompt: strings.Repeat("p", room), Mode: domain.ExecuteMode}); err != nil {
		t.Fatal("prompt exactly at the remaining budget was rejected", err)
	}
	if err := f.requireBudget(t, request, domain.SessionInput{Prompt: strings.Repeat("p", room+1), Mode: domain.ExecuteMode}); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("prompt one byte above the remaining budget was admitted", err)
	}
}

func TestExecutionJobBudgetMeasuresEscapedPrompt(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	request := budgetFixtureRequest(f, 1, "https://github.com/fixture/repo.git")
	// 200,000 raw bytes pass the prompt text limit but escape to 1.2 MB.
	escaped := domain.SessionInput{Prompt: strings.Repeat("<", 200000), Mode: domain.ExecuteMode}
	if err := escaped.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := f.requireBudget(t, request, escaped); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("escaped prompt growth was ignored", err)
	}
	plain := domain.SessionInput{Prompt: strings.Repeat("p", 200000), Mode: domain.ExecuteMode}
	if err := f.requireBudget(t, request, plain); err != nil {
		t.Fatal("same-length unescaped prompt was rejected", err)
	}
}

func TestExecutionJobBudgetCountsAppliedInstructions(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	request := budgetFixtureRequest(f, 30, budgetFixtureURL(4028))
	input := domain.SessionInput{Prompt: strings.Repeat("p", domain.MaxPromptBytes), Mode: domain.ExecuteMode}
	if err := f.requireBudget(t, request, input); err != nil {
		t.Fatal("fixture should fit before instructions are applied", err)
	}
	template := domain.NewID()
	f.mutate(t, func(tx *store.Tx) error {
		if _, err := tx.Put(domain.TemplateKind, template, 0, "", "", domain.Template{Name: "Large", Contents: strings.Repeat("i", 250<<10)}); err != nil {
			return err
		}
		r, err := tx.Get(domain.AgentKind, f.agent)
		if err != nil {
			return err
		}
		agent, err := store.Decode[domain.Agent](r)
		if err != nil {
			return err
		}
		agent.Templates = []domain.ID{template}
		_, err = tx.Put(domain.AgentKind, f.agent, r.Revision, "", "", agent)
		return err
	})
	// Templates are frozen once as templates and once as joined instructions.
	if err := f.requireBudget(t, request, input); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("applied instructions were not counted", err)
	}
	if err := f.requireBudget(t, budgetFixtureRequest(f, 1, "https://github.com/fixture/repo.git"), domain.SessionInput{Prompt: "short", Mode: domain.ExecuteMode}); err != nil {
		t.Fatal("instructions rejected a small job", err)
	}
}

func TestScheduleOccurrenceRejectsOversizedAssignmentBeforePreparation(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	repositories := []domain.ID{}
	f.mutate(t, func(tx *store.Tx) error {
		for i := 0; i < 100; i++ {
			id := domain.NewID()
			repository := domain.Repository{RemoteURL: budgetFixtureURL(4028), Name: fmt.Sprintf("Large repository %03d", i), Checkouts: []domain.Checkout{}, Base: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}
			if _, err := tx.Put(domain.RepositoryKind, id, 0, "", "", repository); err != nil {
				return err
			}
			repositories = append(repositories, id)
		}
		r, err := tx.Get(domain.ProjectKind, f.project)
		if err != nil {
			return err
		}
		project, err := store.Decode[domain.Project](r)
		if err != nil {
			return err
		}
		project.Repositories, project.PrimaryRepository = repositories, repositories[0]
		if _, err := tx.Put(domain.ProjectKind, f.project, r.Revision, "", "", project); err != nil {
			return err
		}
		sr, err := tx.Get(domain.ScheduleKind, f.schedule)
		if err != nil {
			return err
		}
		var schedule domain.Schedule
		if err := json.Unmarshal(sr.Data, &schedule); err != nil {
			return err
		}
		schedule.Definition.Prompt = strings.Repeat("p", domain.MaxPromptBytes)
		schedule.ConfigurationRevision++
		_, err = tx.PutSchedule(f.schedule, sr.Revision, schedule)
		return err
	})
	_, occurrence := f.accept(t, domain.ManualOccurrence)
	if occurrence.State != domain.OccurrenceFailed || occurrence.SessionID != "" || occurrence.Problem == nil || occurrence.Problem.Code != domain.ResourceExhausted {
		t.Fatalf("oversized assignment was not rejected at selection: %+v", occurrence)
	}
	jobs, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.JobKind, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if decoded, err := store.Decode[domain.Job](job); err == nil && decoded.Type == domain.PrepareWorkspaceJob {
			t.Fatal("a workspace preparation job was queued for a rejected assignment")
		}
	}
}

func TestCheckedExecutionAssignmentRechecksSerializedBytes(t *testing.T) {
	c := newContinuationFixture(t, domain.ExecutionSucceeded)
	candidate := c.input
	candidate.Continuation = nil
	assignment := func(prompt string) error {
		candidate.Input.Prompt = prompt
		return c.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			sr, session, err := sessionRecord(tx, candidate.SessionID)
			if err != nil {
				return err
			}
			_, machine, err := activeMachine(tx, candidate.MachineID)
			if err != nil {
				return err
			}
			_, err = checkedExecutionAssignment(tx, sr, session, machine, candidate)
			return err
		})
	}
	if err := assignment("ordinary prompt"); err != nil {
		t.Fatal("ordinary assignment was rejected", err)
	}
	// 200,000 raw bytes validate as prompt text but escape to 1.2 MB.
	if err := assignment(strings.Repeat("<", 200000)); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized serialized assignment reached dispatch publication", err)
	}
	// Existing smaller immutable jobs stay readable: nothing was written above.
	if err := assignment(strings.Repeat("p", domain.MaxPromptBytes)); err != nil {
		t.Fatal("maximum unescaped prompt was rejected", err)
	}
}
