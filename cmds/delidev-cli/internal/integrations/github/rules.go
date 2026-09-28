package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func parseActiveRule(raw []byte) (domain.ActiveRepositoryRule, error) {
	f, ok := jsonObject(raw)
	id, idOK := exactUnsigned(f["ruleset_id"])
	rule := domain.ActiveRepositoryRule{Type: stringField(f, "type"), RulesetID: strconv.FormatUint(id, 10), NativeSourceKind: stringField(f, "ruleset_source_type"), Source: stringField(f, "ruleset_source")}
	if !ok || !idOK {
		return rule, queryUnavailable()
	}
	rule.SourceKind = domain.ClassifyRulesetSource(rule.NativeSourceKind)
	// jsonObject already checked duplicate keys/UTF-8. UseNumber prevents rounded
	// identities in the canonical provider digest; object key ordering is immaterial.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var original any
	if decoder.Decode(&original) != nil {
		return rule, queryUnavailable()
	}
	canonical, err := json.Marshal(original)
	if err != nil {
		return rule, queryUnavailable()
	}
	sum := sha256.Sum256(canonical)
	rule.Digest = hex.EncodeToString(sum[:])
	if rule.Type == "required_status_checks" {
		p, ok := jsonObject(f["parameters"])
		strict, strictOK := nullableBool(p, "strict_required_status_checks_policy")
		var rows []json.RawMessage
		if !ok || !strictOK || strict == nil || domain.Decode(p["required_status_checks"], &rows) != nil || rows == nil || len(rows) > domain.MaxRuleChecks {
			return rule, queryUnavailable()
		}
		required := &domain.RequiredRuleChecks{Strict: *strict, Checks: []domain.RequiredRuleCheck{}}
		for key := range p {
			if key != "strict_required_status_checks_policy" && key != "do_not_enforce_on_create" && key != "required_status_checks" {
				required.UnknownParameters = true
			}
		}
		if _, exists := p["do_not_enforce_on_create"]; exists {
			required.DoNotEnforceOnCreate, ok = nullableBool(p, "do_not_enforce_on_create")
			if !ok || required.DoNotEnforceOnCreate == nil {
				return rule, queryUnavailable()
			}
		}
		for _, raw := range rows {
			check, ok := jsonObject(raw)
			if !ok {
				return rule, queryUnavailable()
			}
			value := domain.RequiredRuleCheck{Context: stringField(check, "context")}
			for key := range check {
				if key != "context" && key != "integration_id" {
					required.UnknownParameters = true
				}
			}
			if rawID, exists := check["integration_id"]; exists && string(rawID) != "null" {
				id, ok := exactUnsigned(rawID)
				if !ok {
					return rule, queryUnavailable()
				}
				text := strconv.FormatUint(id, 10)
				value.IntegrationID = &text
			}
			required.Checks = append(required.Checks, value)
		}
		rule.RequiredChecks = required
	}
	if rule.Validate() != nil {
		return rule, queryUnavailable()
	}
	return rule, nil
}

func (c *Client) readActiveRules(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem) (*domain.PullRequestRules, error) {
	result := &domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: []domain.ActiveRepositoryRule{}}
	seen := map[string]bool{}
	for page := uint32(1); ; page++ {
		path := repositoryPath(repository) + "/rules/branches/" + url.PathEscape(item.BaseRef) + "?" + url.Values{"page": {strconv.FormatUint(uint64(page), 10)}, "per_page": {"100"}}.Encode()
		read := c.readRepositoryJSON(ctx, token, path)
		if read.state != domain.IntegrationAccessAvailable {
			return nil, readProblem(read)
		}
		var rows []json.RawMessage
		if domain.Decode(read.raw, &rows) != nil || rows == nil || len(rows) > 100 {
			return nil, queryUnavailable()
		}
		for _, raw := range rows {
			rule, err := parseActiveRule(raw)
			if err != nil || seen[rule.Key()] {
				return nil, queryUnavailable()
			}
			seen[rule.Key()] = true
			result.Rules = append(result.Rules, rule)
		}
		next, err := queryNextPage(read.link, path, page, repository)
		if err != nil {
			return nil, err
		}
		if len(result.Rules) > domain.MaxPullRequestRules || next != 0 && page >= 5 {
			return nil, domain.Fail(domain.ResourceExhausted, "The active branch rules exceed the complete read limit.", "Inspect the rules in GitHub; no partial requirements are returned.")
		}
		if next == 0 {
			break
		}
		// Empty continuing pages are not a complete inventory or a reason to
		// fetch indefinitely. Page progression is independent of projected rules.
	}
	sort.Slice(result.Rules, func(i, j int) bool { return result.Rules[i].Key() < result.Rules[j].Key() })
	result.Digest = domain.ActiveRulesDigest(result.Rules)
	if result.Validate(item) != nil {
		return nil, queryUnavailable()
	}
	return result, nil
}
