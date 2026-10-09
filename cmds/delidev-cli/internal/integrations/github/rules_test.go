package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func activeRuleFixture() map[string]any {
	return map[string]any{"type": "required_status_checks", "ruleset_id": json.Number("9007199254740993"), "ruleset_source_type": "Repository", "ruleset_source": "fixture-owner/repo", "parameters": map[string]any{"strict_required_status_checks_policy": false, "do_not_enforce_on_create": false, "required_status_checks": []any{map[string]any{"context": "CI Result", "integration_id": 15368}}}}
}

func rulesFixtureClient(t *testing.T, mutate func(int, uint32, map[string]any, http.Header)) (*Client, *int) {
	t.Helper()
	c, _ := repositoryFixture(t, nil)
	original := c.http.Transport
	reads := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/user" || r.URL.Path == "/repos/fixture-owner/repo" {
			return original.RoundTrip(r)
		}
		if r.Method != "GET" || r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Fatal("unscoped rule read")
		}
		header := http.Header{}
		var body any
		if r.URL.Path == "/repos/fixture-owner/repo/pulls/17" {
			body = queryFixtureItem(domain.RepositoryPullRequest, 17, true, false)
		} else {
			if r.URL.EscapedPath() != "/repos/fixture-owner/repo/rules/branches/main" || r.URL.Query().Get("per_page") != "100" {
				t.Fatalf("unexpected rules path %s", r.URL.RequestURI())
			}
			page, _ := strconv.ParseUint(r.URL.Query().Get("page"), 10, 32)
			if page == 1 {
				reads++
			}
			rule := activeRuleFixture()
			if mutate != nil {
				mutate(reads, uint32(page), rule, header)
			}
			body = []any{rule}
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return c, &reads
}
func readFixtureRules(c *Client) (RepositoryQueryObservation, error) {
	return c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryRules, Number: "17"})
}

func TestActiveRulesPreserveExactAppAndCompleteMultiSourceInventory(t *testing.T) {
	c, reads := rulesFixtureClient(t, func(_ int, page uint32, rule map[string]any, h http.Header) {
		if page == 1 {
			h.Set("Link", `<https://api.github.com/repositories/37/rules/branches/main?page=2&per_page=100>; rel="next"`)
		} else {
			rule["ruleset_id"] = 99
			rule["ruleset_source_type"] = "Organization"
			rule["ruleset_source"] = "fixture-owner"
		}
	})
	result, err := readFixtureRules(c)
	if err != nil || *reads != 2 || result.Rules == nil || len(result.Rules.Rules) != 2 || result.Rules.Validate(result.Items[0]) != nil {
		t.Fatal("complete rules unavailable", err)
	}
	if result.Rules.Rules[0].RulesetID != "9007199254740993" || *result.Rules.Rules[0].RequiredChecks.Checks[0].IntegrationID != "15368" {
		t.Fatal("rule/App identities rounded")
	}
}
func TestActiveRulesRejectChangesDuplicatePagesAndMalformedRequirements(t *testing.T) {
	for _, mode := range []string{"changed-app", "changed-unprojected", "duplicate-page", "foreign-link", "missing-checks", "null-strict", "duplicate-check", "unknown-app-shape"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := rulesFixtureClient(t, func(read int, page uint32, rule map[string]any, h http.Header) {
				p := rule["parameters"].(map[string]any)
				switch mode {
				case "changed-app":
					if read == 2 {
						p["required_status_checks"].([]any)[0].(map[string]any)["integration_id"] = 99
					}
				case "changed-unprojected":
					if read == 2 {
						p["future_policy"] = true
					}
				case "duplicate-page":
					if page == 1 {
						h.Set("Link", `<https://api.github.com/repos/fixture-owner/repo/rules/branches/main?page=2&per_page=100>; rel="next"`)
					}
				case "foreign-link":
					h.Set("Link", `<https://api.github.com/repositories/99/rules/branches/main?page=2&per_page=100>; rel="next"`)
				case "missing-checks":
					delete(p, "required_status_checks")
				case "null-strict":
					p["strict_required_status_checks_policy"] = nil
				case "duplicate-check":
					p["required_status_checks"] = append(p["required_status_checks"].([]any), p["required_status_checks"].([]any)[0])
				case "unknown-app-shape":
					p["required_status_checks"].([]any)[0].(map[string]any)["integration_id"] = "15368"
				}
			})
			_, err := readFixtureRules(c)
			if err == nil {
				t.Fatal("unverified rule inventory published")
			}
			if strings.HasPrefix(mode, "changed-") && domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("rule change not reported", err)
			}
		})
	}
}
func TestActiveRuleDigestPreservesUnknownPolicyAndExactNumbers(t *testing.T) {
	raw := `{"type":"future_rule","ruleset_id":9007199254740993,"ruleset_source_type":"Future","ruleset_source":"fixture","parameters":{"b":9007199254740993,"a":true}}`
	first, err := parseActiveRule([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	reordered := `{ "parameters": {"a":true, "b":9007199254740993}, "ruleset_source":"fixture","ruleset_source_type":"Future","ruleset_id":9007199254740993,"type":"future_rule" }`
	second, err := parseActiveRule([]byte(reordered))
	if err != nil || first.Digest != second.Digest || first.SourceKind != domain.RulesetUnknownSource {
		t.Fatal("order or unknown source lost", err)
	}
	changed, err := parseActiveRule([]byte(strings.Replace(raw, `"b":9007199254740993`, `"b":9007199254740992`, 1)))
	if err != nil || changed.Digest == first.Digest {
		t.Fatal("large policy number rounded", err)
	}
}

func TestActiveRulesBoundPaginationAndRejectChangedBase(t *testing.T) {
	c, _ := rulesFixtureClient(t, func(_ int, page uint32, rule map[string]any, h http.Header) {
		rule["ruleset_id"] = page
		h.Set("Link", `<https://api.github.com/repos/fixture-owner/repo/rules/branches/main?page=`+strconv.Itoa(int(page)+1)+`&per_page=100>; rel="next"`)
	})
	if _, err := readFixtureRules(c); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("incomplete continuing pages were accepted", err)
	}
	c, _ = rulesFixtureClient(t, nil)
	original := c.http.Transport
	details := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/repos/fixture-owner/repo/pulls/17" {
			details++
			if details == 2 {
				value := queryFixtureItem(domain.RepositoryPullRequest, 17, true, false)
				value["base"].(map[string]any)["ref"] = "changed-base"
				raw, _ := json.Marshal(value)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			}
		}
		return original.RoundTrip(r)
	})
	if _, err := readFixtureRules(c); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("changed base published", err)
	}
}
func TestActiveRulesPublicSchemaFixture(t *testing.T) {
	path := os.Getenv("DELIDEV_GITHUB_ACTIVE_RULES_FIXTURE")
	if path == "" {
		t.Skip("explicit public schema capture only; no ambient GitHub credentials")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if domain.Decode(raw, &rows) != nil || len(rows) == 0 {
		t.Fatal("missing public rules")
	}
	required := false
	for _, raw := range rows {
		rule, err := parseActiveRule(raw)
		if err != nil {
			t.Fatal(err)
		}
		if rule.Type == "required_status_checks" {
			for _, check := range rule.RequiredChecks.Checks {
				if check.Context == "CI Result" && check.IntegrationID != nil && *check.IntegrationID == "15368" {
					required = true
				}
			}
		}
	}
	if !required {
		t.Fatal("captured required CI/App provenance changed")
	}
}
