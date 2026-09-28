package domain

import (
	"strings"
	"testing"
)

func TestRulesRequireCompleteBoundInventoryAndConsistentSource(t *testing.T) {
	item := RepositoryItem{BaseRef: "release/next", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	rules := []ActiveRepositoryRule{{Type: "deletion", RulesetID: "9007199254740993", SourceKind: RulesetOrganization, NativeSourceKind: "Organization", Source: "fixture-owner", Digest: strings.Repeat("d", 64)}}
	v := PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: rules, Digest: ActiveRulesDigest(rules)}
	if v.Validate(item) != nil {
		t.Fatal("valid inventory rejected")
	}
	v.HeadSHA = item.BaseSHA
	if v.Validate(item) == nil {
		t.Fatal("foreign PR head accepted")
	}
	v.HeadSHA = item.HeadSHA
	v.Rules = append(v.Rules, rules[0])
	v.Digest = ActiveRulesDigest(v.Rules)
	if v.Validate(item) == nil {
		t.Fatal("duplicate rule accepted")
	}
	v.Rules[1].Type = "non_fast_forward"
	v.Rules[1].Source = "foreign"
	v.Digest = ActiveRulesDigest(v.Rules)
	if v.Validate(item) == nil {
		t.Fatal("one ruleset with conflicting sources accepted")
	}
	for _, q := range []RepositoryQuery{
		{Kind: RepositoryIssue, Operation: RepositoryRules, Number: "1"},
		{Kind: RepositoryPullRequest, Operation: RepositoryRules, Number: "1", Page: 1, PageSize: 20},
		{Kind: RepositoryPullRequest, Operation: RepositoryRules, Number: "1", State: RepositoryItemsAll},
	} {
		if q.Validate() == nil {
			t.Fatal("partial or foreign rules query accepted")
		}
	}
}

func TestRuleInventoryDigestIsOrderIndependentButBindsOriginalPolicy(t *testing.T) {
	a := ActiveRepositoryRule{Type: "deletion", RulesetID: "17", Digest: strings.Repeat("a", 64)}
	b := ActiveRepositoryRule{Type: "pull_request", RulesetID: "19", Digest: strings.Repeat("b", 64)}
	first := ActiveRulesDigest([]ActiveRepositoryRule{a, b})
	if first != ActiveRulesDigest([]ActiveRepositoryRule{b, a}) {
		t.Fatal("order changed inventory")
	}
	b.Digest = strings.Repeat("c", 64)
	if first == ActiveRulesDigest([]ActiveRepositoryRule{a, b}) {
		t.Fatal("unknown original parameter change lost")
	}
}
