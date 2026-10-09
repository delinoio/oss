package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeDisplayWeb(block *claude.NativeContentBlock) (domain.ClaudeTextBlock, error) {
	fail := func() (domain.ClaudeTextBlock, error) { return domain.ClaudeTextBlock{}, publicationUncertain() }
	if block == nil || block.Text != nil || block.Thinking != nil || block.Tool != nil || block.Media != nil || block.Citations != nil {
		return fail()
	}
	v := domain.ClaudeTextBlock{Kind: domain.ClaudeTextKind(block.Kind)}
	if n := block.ServerTool; n != nil {
		if block.ServerResult != nil || block.Kind != claude.ServerToolUseBlock || n.Name != claude.ServerWebSearch && n.Name != claude.ServerWebFetch {
			return fail()
		}
		caller, err := claudeDirectCaller(n.CalledBy)
		if err != nil {
			return fail()
		}
		v.Web = &domain.ClaudeWebBlock{NativeID: n.ID, Caller: caller, Name: domain.ClaudeWebName(n.Name), Call: &domain.ClaudeWebCallContent{InitialInput: string(n.Input)}}
	} else if n := block.ServerResult; n != nil {
		caller, err := claudeDirectCaller(n.CalledBy)
		if err != nil {
			return fail()
		}
		if n.Kind != block.Kind || n.Kind != claude.WebSearchResultBlock && n.Kind != claude.WebFetchResultBlock || n.Name != claude.ServerWebSearch && n.Name != claude.ServerWebFetch || n.Detail != nil || n.Execution != nil || n.Edit != nil || n.Advice != nil || n.ToolReferences != nil {
			return fail()
		}
		if n.Problem != "" && (n.Search != nil || n.Document != nil || n.URL != nil || n.RetrievedAt != nil) || n.Name == claude.ServerWebSearch && (n.Document != nil || n.URL != nil || n.RetrievedAt != nil) || n.Name == claude.ServerWebFetch && n.Search != nil {
			return fail()
		}
		r := &domain.ClaudeWebResult{Problem: domain.ClaudeWebProblem(n.Problem)}
		v.Web = &domain.ClaudeWebBlock{NativeID: n.ID, Caller: caller, Name: domain.ClaudeWebName(n.Name), Result: r}
		if n.Problem == "" && n.Name == claude.ServerWebSearch {
			r.Search = make([]domain.ClaudeWebSearchSource, 0, len(n.Search))
			for _, source := range n.Search {
				r.Search = append(r.Search, domain.ClaudeWebSearchSource{URL: source.URL, Title: source.Title, PageAge: cloneClaudeTaskField(source.PageAge)})
			}
		} else if n.Problem == "" && n.Name == claude.ServerWebFetch {
			if n.URL == nil || n.Document == nil {
				return fail()
			}
			d := n.Document
			doc := domain.ClaudeWebDocument{Source: domain.ClaudeWebDocumentSource(d.Source.Kind), Media: domain.ClaudeWebMediaType(d.Source.Media), Title: cloneClaudeTaskField(d.Title), Context: cloneClaudeTaskField(d.Context), Citations: d.Citations}
			if d.Source.Kind == claude.TextMedia {
				doc.Text = cloneClaudeTaskField(d.Source.Data)
			} else if d.Source.Kind == claude.ContentMedia {
				doc.Text = cloneClaudeTaskField(d.Source.Text)
			}
			r.Fetch = &domain.ClaudeWebFetchContent{URL: *n.URL, RetrievedAt: cloneClaudeTaskField(n.RetrievedAt), Document: doc}
		}
	} else {
		return fail()
	}
	if v.Validate() != nil {
		return fail()
	}
	return v, nil
}
