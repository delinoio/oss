package worker

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeCitationCount(n *uint64) domain.ClaudeProgressCount {
	if n == nil {
		return ""
	}
	return domain.ClaudeProgressCount(strconv.FormatUint(*n, 10))
}
func claudeDisplayCitation(n claude.NativeCitation) (domain.ClaudeCitation, error) {
	v := domain.ClaudeCitation{Kind: domain.ClaudeCitationKind(n.Kind), Text: n.Text, Title: cloneClaudeTaskField(n.Title)}
	expected := claude.NativeCitation{Kind: n.Kind, Text: n.Text, Title: n.Title, Native: n.Native}
	switch n.Kind {
	case claude.CharacterCitation, claude.PageCitation, claude.BlockCitation:
		expected.DocumentIndex, expected.Start, expected.End, expected.FileID = n.DocumentIndex, n.Start, n.End, n.FileID
		v.Document = &domain.ClaudeDocumentCitation{Index: claudeCitationCount(n.DocumentIndex), Start: claudeCitationCount(n.Start), End: claudeCitationCount(n.End)}
		var fields map[string]json.RawMessage
		if len(n.Native) != 0 && json.Unmarshal(n.Native, &fields) != nil {
			return v, publicationUncertain()
		}
		if n.FileID != nil || fields["file_id"] != nil {
			v.Document.File = &domain.ClaudeCitationFile{Value: cloneClaudeTaskField(n.FileID)}
		}
	case claude.SearchCitation:
		expected.SearchIndex, expected.Start, expected.End, expected.Source = n.SearchIndex, n.Start, n.End, n.Source
		if n.Source == nil {
			return v, publicationUncertain()
		}
		v.Search = &domain.ClaudeSearchLocation{Index: claudeCitationCount(n.SearchIndex), Start: claudeCitationCount(n.Start), End: claudeCitationCount(n.End), Source: *n.Source}
	case claude.WebCitation:
		expected.URL = n.URL
		if n.URL == nil {
			return v, publicationUncertain()
		}
		v.Web = &domain.ClaudeWebLocation{URL: *n.URL}
	default:
		return v, publicationUncertain()
	}
	if !reflect.DeepEqual(n, expected) || v.Validate() != nil {
		return v, publicationUncertain()
	}
	return v, nil
}
func claudeDisplayCitations(n *claude.NativeCitations) (*domain.ClaudeCitationCollection, error) {
	if n == nil {
		return nil, nil
	}
	v := &domain.ClaudeCitationCollection{Null: n.Null}
	if n.Entries != nil {
		v.Entries = make([]domain.ClaudeCitation, 0, len(n.Entries))
		for _, entry := range n.Entries {
			citation, err := claudeDisplayCitation(entry)
			if err != nil {
				return nil, err
			}
			v.Entries = append(v.Entries, citation)
		}
	}
	if v.Validate() != nil {
		return nil, publicationUncertain()
	}
	return v, nil
}
