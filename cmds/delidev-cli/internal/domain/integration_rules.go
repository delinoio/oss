package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"sort"
	"strings"
)

const MaxPullRequestRules = 500
const MaxRuleChecks = 100

type RulesetSourceKind string

const (
	RulesetRepository    RulesetSourceKind = "repository"
	RulesetOrganization  RulesetSourceKind = "organization"
	RulesetEnterprise    RulesetSourceKind = "enterprise"
	RulesetUnknownSource RulesetSourceKind = "unknown"
)

func ClassifyRulesetSource(native string) RulesetSourceKind {
	switch native {
	case "Repository":
		return RulesetRepository
	case "Organization":
		return RulesetOrganization
	case "Enterprise":
		return RulesetEnterprise
	default:
		return RulesetUnknownSource
	}
}

type RequiredRuleCheck struct {
	Context string `json:"context"`
	// Nil preserves the provider's absence of an integration restriction. An
	// explicit zero is retained separately; it must not be guessed to mean any App.
	IntegrationID *string `json:"integration_id,omitempty"`
}

type RequiredRuleChecks struct {
	UnknownParameters    bool                `json:"unknown_parameters,omitempty"`
	Strict               bool                `json:"strict"`
	DoNotEnforceOnCreate *bool               `json:"do_not_enforce_on_create,omitempty"`
	Checks               []RequiredRuleCheck `json:"checks"`
}

// An explicit source SHA is the only supported workflow resolution profile.
// Ref is retained as evidence, never resolved or substituted for a missing SHA.
type RequiredWorkflowReference struct {
	RepositoryID string  `json:"repository_id"`
	Path         string  `json:"path"`
	SHA          *string `json:"sha,omitempty"`
	Ref          *string `json:"ref,omitempty"`
}

func (v RequiredWorkflowReference) Validate() error {
	if !PositiveDecimal(v.RepositoryID) || Text(v.Path, "workflow path", 1024, true) != nil || path.Clean(v.Path) != v.Path || !strings.HasPrefix(v.Path, ".github/workflows/") || strings.ContainsAny(v.Path, "\\\r\n") || v.SHA != nil && !repositorySHA(*v.SHA) || v.Ref != nil && Text(*v.Ref, "workflow ref", 1024, true) != nil {
		return invalidPRObservation()
	}
	return nil
}

type RequiredRuleWorkflows struct {
	UnknownParameters    bool                        `json:"unknown_parameters,omitempty"`
	DoNotEnforceOnCreate *bool                       `json:"do_not_enforce_on_create,omitempty"`
	Workflows            []RequiredWorkflowReference `json:"workflows"`
}

type ActiveRepositoryRule struct {
	Type             string            `json:"type"`
	RulesetID        string            `json:"ruleset_id"`
	SourceKind       RulesetSourceKind `json:"source_kind"`
	NativeSourceKind string            `json:"native_source_kind"`
	Source           string            `json:"source"`
	// Digest covers the entire original provider rule, including parameters not
	// exposed by this projection. It detects changes without publishing policy internals.
	Digest            string                 `json:"digest"`
	RequiredChecks    *RequiredRuleChecks    `json:"required_checks,omitempty"`
	RequiredWorkflows *RequiredRuleWorkflows `json:"required_workflows,omitempty"`
}

func (r ActiveRepositoryRule) Key() string { return r.RulesetID + ":" + r.Type }
func (r ActiveRepositoryRule) Validate() error {
	if !PositiveDecimal(r.RulesetID) || Text(r.Type, "rule type", 100, true) != nil || Text(r.Source, "ruleset source", 512, true) != nil || Text(r.NativeSourceKind, "ruleset source type", 64, true) != nil || strings.ContainsAny(r.Type+r.Source+r.NativeSourceKind, "\r\n") || r.SourceKind != ClassifyRulesetSource(r.NativeSourceKind) || !lowerDigest(r.Digest) {
		return invalidPRObservation()
	}
	if r.RequiredWorkflows != nil {
		w := r.RequiredWorkflows
		if r.Type != "workflows" || r.RequiredChecks != nil || w.Workflows == nil || len(w.Workflows) > MaxRuleChecks {
			return invalidPRObservation()
		}
		seen := map[string]bool{}
		for _, ref := range w.Workflows {
			key := ref.RepositoryID + ":" + ref.Path
			if ref.Validate() != nil || seen[key] {
				return invalidPRObservation()
			}
			seen[key] = true
		}
	}
	if r.Type != "required_status_checks" {
		if r.RequiredChecks != nil {
			return invalidPRObservation()
		}
		return nil
	}
	c := r.RequiredChecks
	if c == nil || c.Checks == nil || len(c.Checks) > MaxRuleChecks {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	for _, check := range c.Checks {
		if Text(check.Context, "required check context", 1024, true) != nil || strings.ContainsAny(check.Context, "\r\n") || check.IntegrationID != nil && !unsignedDecimal(*check.IntegrationID) {
			return invalidPRObservation()
		}
		key := check.Context + "\x00"
		if check.IntegrationID != nil {
			key += *check.IntegrationID
		}
		if seen[key] {
			return invalidPRObservation()
		}
		seen[key] = true
	}
	return nil
}
func lowerDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

type PullRequestRules struct {
	BaseRef string                 `json:"base_ref"`
	BaseSHA string                 `json:"base_sha"`
	HeadSHA string                 `json:"head_sha"`
	Rules   []ActiveRepositoryRule `json:"rules"`
	Digest  string                 `json:"digest"`
}

// Order-independent inventory digest includes the original rule digests and
// retained projections. It is an observation, never a future authorization grant.
func ActiveRulesDigest(rules []ActiveRepositoryRule) string {
	ordered := append([]ActiveRepositoryRule{}, rules...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key() < ordered[j].Key() })
	raw, _ := json.Marshal(ordered)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (v PullRequestRules) Validate(item RepositoryItem) error {
	if v.BaseRef != item.BaseRef || v.BaseSHA != item.BaseSHA || v.HeadSHA != item.HeadSHA || v.Rules == nil || len(v.Rules) > MaxPullRequestRules || v.Digest != ActiveRulesDigest(v.Rules) {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	sources := map[string][2]string{}
	for _, rule := range v.Rules {
		if rule.Validate() != nil || seen[rule.Key()] {
			return invalidPRObservation()
		}
		source := [2]string{rule.NativeSourceKind, rule.Source}
		if previous, exists := sources[rule.RulesetID]; exists && previous != source {
			return invalidPRObservation()
		}
		sources[rule.RulesetID] = source
		seen[rule.Key()] = true
	}
	return nil
}
