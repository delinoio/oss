package query

import (
	"strings"
	"testing"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"google.golang.org/protobuf/proto"
)

func TestParseAndBuilderEditPreserveUnknownClauses(t *testing.T) {
	t.Parallel()
	original, err := Parse(`is:open assignee:@me future:"opaque value" label:bug`)
	if err != nil {
		t.Fatal(err)
	}
	if original.RawQuery != `is:pr is:open assignee:@me future:"opaque value" label:bug` {
		t.Fatalf("canonical raw query = %q", original.RawQuery)
	}
	edited := &deckv1.ViewQuery{Builder: &deckv1.QueryBuilder{
		Clauses: []*deckv1.QueryClause{
			{
				Clause: &deckv1.QueryClause_State{State: &deckv1.StateQualifier{
					State: deckv1.PullRequestState_PULL_REQUEST_STATE_CLOSED,
				}},
			},
			{
				Clause: &deckv1.QueryClause_Assignee{Assignee: &deckv1.AssigneeQualifier{
					Assignee: &deckv1.QueryIdentity{
						Kind: deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_VIEWER,
					},
				}},
			},
		},
	}}
	updated, err := Apply(original, edited)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"is:closed", "assignee:@me", `future:"opaque value"`, "is:pr"} {
		if !strings.Contains(updated.RawQuery, expected) {
			t.Fatalf("updated raw query %q omitted %q", updated.RawQuery, expected)
		}
	}
	if strings.Contains(updated.RawQuery, "is:open") || strings.Contains(updated.RawQuery, "label:bug") {
		t.Fatalf("recognized clauses were not replaced: %q", updated.RawQuery)
	}
	roundTrip, err := Parse(updated.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.RawQuery != updated.RawQuery {
		t.Fatalf("round trip changed query: %q != %q", roundTrip.RawQuery, updated.RawQuery)
	}
}

func TestParseEnforcesProviderQueryLimit(t *testing.T) {
	t.Parallel()
	prefix := "is:pr label:"
	atLimit := prefix + strings.Repeat("a", maxQueryBytes-len(prefix))
	parsed, err := Parse(atLimit)
	if err != nil {
		t.Fatalf("parse query at provider limit: %v", err)
	}
	if len(parsed.RawQuery) != maxQueryBytes {
		t.Fatalf("canonical query length = %d", len(parsed.RawQuery))
	}
	if _, err := Parse(atLimit + "a"); err == nil {
		t.Fatal("query over provider limit was accepted")
	}

	withoutPullRequest := "label:" +
		strings.Repeat("a", maxQueryBytes-len("label:"))
	if _, err := Parse(withoutPullRequest); err == nil {
		t.Fatal("canonical query over provider limit was accepted")
	}
}

func TestResolveViewerUsesCurrentViewerWithoutChangingDefinition(t *testing.T) {
	t.Parallel()
	definition, err := Parse("is:pr author:@me assignee:@me future:@me")
	if err != nil {
		t.Fatal(err)
	}
	first, err := ResolveViewer(definition.RawQuery, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveViewer(definition.RawQuery, "monalisa")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.Contains(first, "author:octocat") ||
		!strings.Contains(second, "author:monalisa") {
		t.Fatalf("viewer resolutions: first=%q second=%q", first, second)
	}
	if !strings.Contains(first, "future:@me") || !strings.Contains(second, "future:@me") {
		t.Fatalf("unknown relative clause was rewritten: first=%q second=%q", first, second)
	}
	if !strings.Contains(definition.RawQuery, "author:@me") {
		t.Fatalf("persisted definition changed: %q", definition.RawQuery)
	}
}

func TestRawEditRemainsAuthoritative(t *testing.T) {
	t.Parallel()
	existing, err := Parse("is:pr is:open label:old")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := Apply(existing, &deckv1.ViewQuery{
		RawQuery: "is:closed label:new unknown:kept",
		Builder: &deckv1.QueryBuilder{Clauses: []*deckv1.QueryClause{{
			Clause: &deckv1.QueryClause_State{State: &deckv1.StateQualifier{
				State: deckv1.PullRequestState_PULL_REQUEST_STATE_OPEN,
			}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(updated.RawQuery, "is:open") ||
		!strings.Contains(updated.RawQuery, "is:closed") ||
		!strings.Contains(updated.RawQuery, "unknown:kept") {
		t.Fatalf("raw query did not remain authoritative: %q", updated.RawQuery)
	}
}

func TestBuilderEditPreservesPersonalOwnerQualifier(t *testing.T) {
	t.Parallel()
	existing, err := Parse("is:pr user:octocat is:open")
	if err != nil {
		t.Fatal(err)
	}
	edited := proto.Clone(existing).(*deckv1.ViewQuery)
	for _, clause := range edited.Builder.Clauses {
		if state := clause.GetState(); state != nil {
			state.State = deckv1.PullRequestState_PULL_REQUEST_STATE_CLOSED
		}
	}
	updated, err := Apply(existing, &deckv1.ViewQuery{Builder: edited.Builder})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.RawQuery, "user:octocat") ||
		strings.Contains(updated.RawQuery, "org:octocat") {
		t.Fatalf("personal owner qualifier changed: %q", updated.RawQuery)
	}
}
