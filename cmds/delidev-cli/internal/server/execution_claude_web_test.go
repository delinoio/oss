package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestClaudeWebServerRetainsOriginalResultsAtomicallyAndReplays(t *testing.T) {
	for _, name := range []domain.ClaudeWebName{domain.ClaudeWebSearch, domain.ClaudeWebFetch} {
		problems := []string{"", "unavailable", "invalid_tool_input"}
		if name == domain.ClaudeWebFetch {
			problems = append(problems, "url_not_allowed")
		}
		for _, problem := range problems {
			t.Run(string(name)+problem, func(t *testing.T) {
				f := newClaudePublicationFixture(t, domain.ExecuteMode)
				f.publish(t, f.event(domain.ExecutionThreadBound, 1))
				f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
				seq := uint64(2)
				u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_web", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
				publish := func() {
					t.Helper()
					seq++
					e := f.event(domain.ExecutionClaudeMessageObserved, seq)
					e.ClaudeMessage = &u
					req := f.publish(t, e)
					if res, err := f.call(req); err != nil || !res.Msg.Replayed {
						t.Fatal("receipt was not idempotent", err)
					}
				}
				publish()
				i := uint32(0)
				input := `{"query":"Original"}`
				if name == domain.ClaudeWebFetch {
					input = `{"url":"https://fixture.invalid"}`
				}
				call := domain.ClaudeTextBlock{Kind: domain.ClaudeWebCall, Web: &domain.ClaudeWebBlock{NativeID: "srvtool_web", Name: name, Call: &domain.ClaudeWebCallContent{InitialInput: input}}}
				u.Mutation, u.Index, u.Block = domain.ClaudeBlockStart, &i, &call
				publish()
				u.Mutation = domain.ClaudeBlockComplete
				publish()
				u.Mutation, u.Block = domain.ClaudeBlockStop, nil
				publish()
				i = 1
				r := &domain.ClaudeWebResult{Problem: domain.ClaudeWebProblem(problem)}
				kind := domain.ClaudeWebSearchResult
				if name == domain.ClaudeWebFetch {
					kind = domain.ClaudeWebFetchResult
				}
				if problem == "" {
					if name == domain.ClaudeWebSearch {
						r.Search = []domain.ClaudeWebSearchSource{{URL: "https://fixture.invalid", Title: "Original source"}}
					} else {
						body := "Original document"
						r.Fetch = &domain.ClaudeWebFetchContent{URL: "https://fixture.invalid", Document: domain.ClaudeWebDocument{Source: "text", Media: "text/plain", Text: &body}}
					}
				}
				result := domain.ClaudeTextBlock{Kind: kind, Web: &domain.ClaudeWebBlock{NativeID: "srvtool_web", Name: name, Result: r}}
				foreign := result
				foreign.Web = &domain.ClaudeWebBlock{NativeID: "foreign", Name: name, Result: r}
				bad := u
				bad.Mutation, bad.Block = domain.ClaudeBlockStart, &foreign
				e := f.event(domain.ExecutionClaudeMessageObserved, seq+1)
				e.ClaudeMessage = &bad
				if _, err := f.call(f.requestEvent(t, e)); err == nil {
					t.Fatal("foreign native result accepted")
				}
				u.Mutation, u.Block = domain.ClaudeBlockStart, &result
				publish()
				u.Mutation = domain.ClaudeBlockComplete
				publish()
				u.Mutation, u.Block = domain.ClaudeBlockStop, nil
				publish()
				u.Mutation, u.Index = domain.ClaudeMessageStop, nil
				publish()
				row, err := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
				if err != nil {
					t.Fatal(err)
				}
				value, err := store.Decode[domain.ExecutionMessage](row)
				if err != nil || value.Claude == nil || len(value.Claude.Blocks) != 2 || value.Claude.Blocks[1].Block.Web.Result.Problem != domain.ClaudeWebProblem(problem) || value.State != domain.MessageComplete {
					t.Fatal("original result lost", err)
				}
				if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
					eligible, err := tx.ClaudeRootContentContinuation(f.input.ExecutionID)
					if err != nil {
						return err
					}
					if eligible {
						t.Fatal("public web history invented native continuation")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				// A second provider message cannot manufacture a fresh native call identity.
				u.ID, u.NativeID, u.Mutation = domain.NewID(), "msg_replacement", domain.ClaudeMessageStart
				publish()
				i = 0
				u.Mutation, u.Index, u.Block = domain.ClaudeBlockStart, &i, &call
				e = f.event(domain.ExecutionClaudeMessageObserved, seq+1)
				e.ClaudeMessage = &u
				if _, err := f.call(f.requestEvent(t, e)); err == nil {
					t.Fatal("native call duplicated across messages")
				}
			})
		}
	}
}
