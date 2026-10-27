// SPDX-License-Identifier: Apache-2.0
// Package pricing fixes cost estimates at request completion using a bundled
// reference snapshot. See snapshot/LICENSE-MIT and snapshot/NOTICE for data attribution.
package pricing

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const ReferenceRevision = "88aaa754566d119b6b8fc898f8fb47602387d82e"

//go:embed snapshot/*.json
var bundled embed.FS

type Rates struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}
type Status string

const (
	Reported    Status = "reported"
	Estimated   Status = "estimated"
	Unreported  Status = "unreported"
	Unsupported Status = "unsupported"
)

type Source string

const (
	Catalog  Source = "jawcode"
	Expected Source = "expected"
	Operator Source = "operator"
)

type Verification string

const (
	Verified Verification = "verified"
	Derived  Verification = "verified-derived"
)

type PolicyMode string

const (
	Automatic PolicyMode = "automatic"
	Flat      PolicyMode = "flat"
	Custom    PolicyMode = "custom"
)

type Comparison string

const (
	Greater      Comparison = "gt"
	GreaterEqual Comparison = "gte"
)

type PromptPolicy struct {
	Mode            PolicyMode `json:"mode"`
	ThresholdTokens int64      `json:"thresholdTokens"`
	Comparison      Comparison `json:"comparison"`
	Rates           Rates      `json:"rates"`
}

// OptionalRates preserves unknown operator prices. All values are USD per million
// tokens. CacheWrite is a duration-independent fallback for either cache TTL.
type OptionalRates struct {
	Currency        string   `json:"currency"`
	Input           *float64 `json:"input,omitempty"`
	Output          *float64 `json:"output,omitempty"`
	CacheRead       *float64 `json:"cache_read,omitempty"`
	CacheWrite      *float64 `json:"cache_write,omitempty"`
	CacheWriteShort *float64 `json:"cache_write_short,omitempty"`
	CacheWriteLong  *float64 `json:"cache_write_long,omitempty"`
}
type Override struct {
	ServiceTier   string         `json:"serviceTier,omitempty"`
	OptionalRates *OptionalRates `json:"optionalRates,omitempty"`
	Provider      string         `json:"provider"`
	Model         string         `json:"modelId"`
	Rates         Rates          `json:"cost4"`
	PromptPolicy  *PromptPolicy  `json:"promptLengthPricing,omitempty"`
	SourceRef     string         `json:"source"`
	VerifiedAt    string         `json:"verifiedAt"`
	Status        Verification   `json:"status"`
}
type Price struct {
	ServiceTier                       string
	OptionalRates                     *OptionalRates
	Provider, Model, MetadataProvider string
	Rates                             Rates
	PromptPolicy                      *PromptPolicy
	Source                            Source
	SourceRef, VerifiedAt             string
	Status                            Verification
}
type contextRule struct {
	Provider           string
	Model              string `json:"modelId"`
	Threshold          int64  `json:"thresholdInputTokens"`
	Inclusive          bool
	Multiplier         Rates
	Relation           string `json:"confirmedPriorityRelation"`
	Source, VerifiedAt string
}
type priorityRule struct {
	Provider                     string
	Model                        string `json:"modelId"`
	Multiplier                   float64
	RequiresResponseConfirmation bool
	Source, VerifiedAt           string
}
type rules struct {
	Antigravity         map[string]string
	Revision            string
	Aliases             map[string]string
	Vendors             []string
	Overlays, Overrides []Override
	Priority            []priorityRule
	Context             []contextRule
}

// Snapshot is immutable after construction. Constructor inputs are deep copied;
// estimates contain their own price and provenance, ready for durable recording.
type Snapshot struct {
	rules     rules
	catalog   map[string]map[string]Rates
	overrides map[string]Override
	id        string
}

func (s *Snapshot) ID() string { return s.id }

// NewSnapshot accepts already-authorized common operator overrides. The server
// must enforce operator authorization and persist changes before constructing it.
func NewSnapshot(overrides []Override) (*Snapshot, error) {
	s := &Snapshot{catalog: map[string]map[string]Rates{}, overrides: map[string]Override{}}
	raw, err := bundled.ReadFile("snapshot/rules.json")
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &s.rules); err != nil {
		return nil, err
	}
	hash := sha256.New()
	hash.Write(raw)
	entries, err := bundled.ReadDir("snapshot")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() == "rules.json" {
			continue
		}
		data, err := bundled.ReadFile("snapshot/" + entry.Name())
		if err != nil {
			return nil, err
		}
		var models map[string]Rates
		if err = json.Unmarshal(data, &models); err != nil {
			return nil, err
		}
		s.catalog[strings.TrimSuffix(entry.Name(), ".json")] = models
		hash.Write(data)
	}
	for _, o := range overrides {
		if o.Provider == "" || o.Model == "" || !validRates(o.Rates) {
			return nil, errors.New("invalid operator price")
		}
		o.ServiceTier = normalizeTier(o.ServiceTier)
		if strings.ContainsRune(o.Provider, '\x00') || strings.ContainsRune(o.Model, '\x00') || strings.ContainsRune(o.ServiceTier, '\x00') {
			return nil, errors.New("invalid operator identity")
		}
		if o.OptionalRates != nil {
			if o.PromptPolicy != nil {
				return nil, errors.New("optional rates require flat absolute pricing")
			}
			o.OptionalRates = copyOptional(o.OptionalRates)
			if o.OptionalRates.Currency == "" {
				o.OptionalRates.Currency = "USD"
			}
			if o.OptionalRates.Currency != "USD" {
				return nil, errors.New("operator currency must be USD")
			}
			for _, rate := range optionalValues(o.OptionalRates) {
				if rate != nil && !validRates(Rates{Input: *rate}) {
					return nil, errors.New("invalid optional operator rate")
				}
			}
		}
		if _, ok := s.overrides[overrideKey(o.Provider, o.Model, o.ServiceTier)]; ok {
			return nil, errors.New("duplicate operator price")
		}
		if o.PromptPolicy != nil {
			p := *o.PromptPolicy
			if p.Mode != Flat && p.Mode != Automatic && p.Mode != Custom {
				return nil, errors.New("invalid prompt policy")
			}
			if p.Mode == Custom && (p.ThresholdTokens < 0 || (p.Comparison != Greater && p.Comparison != GreaterEqual) || !validRates(p.Rates)) {
				return nil, errors.New("invalid prompt threshold")
			}
			o.PromptPolicy = &p
		}
		s.overrides[overrideKey(o.Provider, o.Model, o.ServiceTier)] = o
	}
	raw, _ = json.Marshal(s.overrides)
	hash.Write(raw)
	s.id = ReferenceRevision + ":" + hex.EncodeToString(hash.Sum(nil))
	return s, nil
}
func key(provider, model string) string { return provider + "\x00" + model }
func validRates(r Rates) bool {
	for _, v := range []float64{r.Input, r.Output, r.CacheRead, r.CacheWrite} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e6 {
			return false
		}
	}
	return true
}
func nonzero(r Rates) bool { return r != (Rates{}) && validRates(r) }
func scale(a, b Rates) Rates {
	return Rates{a.Input * b.Input, a.Output * b.Output, a.CacheRead * b.CacheRead, a.CacheWrite * b.CacheWrite}
}
func uniform(r Rates, m float64) Rates { return scale(r, Rates{m, m, m, m}) }
func family(p string) string {
	if p == "anthropic2" {
		return "anthropic"
	}
	return p
}

var accountSuffix = regexp.MustCompile(`-p[a-f0-9]{6}$`)

func canonicalProvider(p string) string {
	switch p {
	case "chatgpt", "openai-multi":
		return "openai"
	}
	return p
}
func baseProvider(p string) string {
	switch p {
	case "openai-main", "chatgpt-main", "openai-multi-main":
		return "openai"
	}
	return canonicalProvider(accountSuffix.ReplaceAllString(p, ""))
}

type Resolution struct {
	// Literal configured names win over account labels, including names ending in -pabcdef.
	ConfiguredProviders  map[string]bool
	AccountProviders     map[string]string
	DisableModelFallback bool
}

func (s *Snapshot) Resolve(provider, model string, opts Resolution) (Price, bool) {
	if o, ok := s.overrides[key(provider, model)]; ok {
		return overridePrice(o), true
	}
	if !opts.ConfiguredProviders[provider] {
		if p, ok := opts.AccountProviders[provider]; ok {
			provider = p
		} else {
			provider = baseProvider(provider)
		}
	}
	price, ok := s.resolve(provider, model, opts)
	if ok {
		return price, true
	}
	if strings.HasPrefix(provider, "google-antigravity") {
		base := strings.TrimSpace(model)
		if mapped, found := s.rules.Antigravity[base]; found {
			base = mapped
		}
		if base != "" && base != model {
			return s.resolve(provider, base, opts)
		}
	}
	return Price{}, false
}
func overridePrice(o Override) Price {
	var p *PromptPolicy
	if o.PromptPolicy != nil {
		v := *o.PromptPolicy
		p = &v
	}
	return Price{ServiceTier: o.ServiceTier, OptionalRates: copyOptional(o.OptionalRates), Provider: o.Provider, Model: o.Model, Rates: o.Rates, PromptPolicy: p, Source: Operator, SourceRef: o.SourceRef, VerifiedAt: o.VerifiedAt, Status: Verified}
}
func (s *Snapshot) resolve(provider, model string, opts Resolution) (Price, bool) {
	if o, ok := s.overrides[key(provider, model)]; ok {
		return overridePrice(o), true
	}
	base, fast, supported := cursorModel(provider, model)
	if !supported {
		return Price{}, false
	}
	finish := func(p Price) (Price, bool) {
		if fast {
			p.Rates = uniform(p.Rates, 2)
			p.SourceRef = "https://cursor.com/docs/models/" + base
			p.Status = Verified
		}
		return p, true
	}
	fromOverlay := func(o Override) Price {
		return Price{Provider: provider, Model: model, Rates: o.Rates, Source: Expected, SourceRef: o.SourceRef, VerifiedAt: o.VerifiedAt, Status: o.Status}
	}
	for _, o := range s.rules.Overrides {
		if o.Provider == family(provider) && o.Model == model && nonzero(o.Rates) && (o.Status == Verified || o.Status == Derived) {
			return finish(fromOverlay(o))
		}
	}
	bundle := s.rules.Aliases[provider]
	if bundle == "" {
		bundle = s.rules.Aliases[strings.ToLower(provider)]
	}
	if r, ok := s.catalog[bundle][model]; ok && nonzero(r) {
		return finish(Price{Provider: provider, Model: model, MetadataProvider: bundle, Rates: r, Source: Catalog, SourceRef: "src/generated/model-metadata.ts@" + ReferenceRevision, Status: Verified})
	}
	for _, candidate := range []string{model, base} {
		if candidate == "" {
			continue
		}
		for _, status := range []Verification{Verified, Derived} {
			for _, o := range s.rules.Overlays {
				if o.Provider == family(provider) && o.Model == candidate && o.Status == status && nonzero(o.Rates) {
					return finish(fromOverlay(o))
				}
			}
		}
	}
	if !opts.DisableModelFallback {
		if r, vendor, ok := s.vendor(model); ok {
			return finish(Price{Provider: provider, Model: model, MetadataProvider: vendor, Rates: r, Source: Catalog, SourceRef: "src/generated/model-metadata.ts@" + ReferenceRevision, Status: Derived})
		}
	}
	return Price{}, false
}
func (s *Snapshot) vendor(model string) (Rates, string, bool) {
	lookup := func(id string) (Rates, string, bool) {
		for _, p := range s.rules.Vendors {
			if r, ok := s.catalog[p][id]; ok && nonzero(r) {
				return r, p, true
			}
		}
		return Rates{}, "", false
	}
	for _, id := range []string{model, strings.ReplaceAll(model, ".", "-")} {
		if r, p, ok := lookup(id); ok {
			return r, p, true
		}
	}
	parts := strings.Split(model, "/")
	if len(parts) == 2 {
		for _, id := range []string{parts[1], strings.ReplaceAll(parts[1], ".", "-")} {
			if r, p, ok := lookup(id); ok && strings.EqualFold(strings.ReplaceAll(parts[0], "-", ""), strings.ReplaceAll(p, "-", "")) {
				return r, p, true
			}
		}
	}
	return Rates{}, "", false
}

// Usage input is inclusive of read/write cache tokens, as in the reference.
// Nil input/output is unknown; optional absent cache counts mean no reported cache.
type Usage struct {
	InputTokens, OutputTokens, CacheReadInputTokens, CachedInputTokens, CacheCreationInputTokens, CacheCreation1hInputTokens *int64
	Estimated                                                                                                                bool
}
type Tokens struct{ Input, Output, CacheRead, CacheWrite, CacheWrite1h int64 }

func Normalize(u Usage) (Tokens, bool) {
	if u.InputTokens == nil || u.OutputTokens == nil || *u.InputTokens < 0 || *u.OutputTokens < 0 {
		return Tokens{}, false
	}
	value := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	input, output := *u.InputTokens, *u.OutputTokens
	write := value(u.CacheCreationInputTokens)
	read := value(u.CacheReadInputTokens)
	if u.CacheReadInputTokens == nil {
		read = value(u.CachedInputTokens)
	}
	if read < 0 || write < 0 || write > input {
		return Tokens{}, false
	}
	if read > input-write {
		if u.CacheReadInputTokens != nil || u.CachedInputTokens == nil || u.CacheCreationInputTokens == nil {
			return Tokens{}, false
		}
		read = max(0, read-write)
		if read > input-write {
			return Tokens{}, false
		}
	}
	write1h := min(max(0, value(u.CacheCreation1hInputTokens)), write)
	return Tokens{input - read - write, output, read, write, write1h}, true
}

type Breakdown struct{ Input, Output, CacheRead, CacheWrite, Total float64 }

func Calculate(t Tokens, r Rates) Breakdown {
	b := Breakdown{Input: r.Input * float64(t.Input) / 1e6, Output: r.Output * float64(t.Output) / 1e6, CacheRead: r.CacheRead * float64(t.CacheRead) / 1e6, CacheWrite: r.CacheWrite * (float64(t.CacheWrite-t.CacheWrite1h) + 1.6*float64(t.CacheWrite1h)) / 1e6}
	b.Total = b.Input + b.Output + b.CacheRead + b.CacheWrite
	return b
}

type TierOutcome struct {
	Canonical, Confirmation, FastOutcome, WireKind, WireValue string
	ResponseServiceTier                                       *string
	ResponseTierAuthoritative                                 *bool
}
type Tier struct {
	Response, Requested, Configured *string
	Outcome                         *TierOutcome
}

func (t Tier) normalized() Tier {
	if t.Outcome == nil {
		return t
	}
	o := t.Outcome
	auth := o.ResponseTierAuthoritative == nil || *o.ResponseTierAuthoritative
	p := "priority"
	if auth && o.Canonical == "priority" && o.Confirmation == "confirmed" {
		return Tier{Response: &p}
	}
	if auth && o.ResponseServiceTier != nil {
		return Tier{Response: o.ResponseServiceTier}
	}
	if o.Canonical == "priority" && (o.Confirmation == "assumed" || (!auth && o.Confirmation == "confirmed")) {
		return Tier{Requested: &p}
	}
	if o.FastOutcome == "unknown" && o.WireKind == "service-tier" {
		return Tier{Requested: &o.WireValue}
	}
	return Tier{}
}
func fastTier(p *string) bool {
	if p == nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(*p))
	return v == "fast" || v == "priority"
}
func (t Tier) effective() *string {
	if t.Response != nil {
		return t.Response
	}
	if t.Requested != nil {
		return t.Requested
	}
	return t.Configured
}

type Request struct {
	Provider, Model string
	Usage           *Usage
	Status          Status
	Tier            Tier
	Resolution      Resolution
}
type UnknownReason string

const (
	MissingUsage UnknownReason = "missing-usage"
	InvalidUsage UnknownReason = "invalid-usage"
	MissingPrice UnknownReason = "missing-price"
)

type Estimate struct {
	SnapshotID                                 string
	RecordedAt                                 time.Time
	Known                                      bool
	UnknownReason                              UnknownReason
	Price                                      Price
	EffectiveRates                             Rates
	EffectiveOptionalRates                     *OptionalRates
	Tokens                                     Tokens
	Cost                                       Breakdown
	Estimated                                  bool
	PriorityMultiplier                         float64
	ContextTier                                string
	CustomThresholdApplied, PriorityLowerBound bool
	Tier                                       Tier
}

// Estimate fixes applied rates and provenance. Persist this value; never resolve
// historical requests again when replacing the active Snapshot.
func (s *Snapshot) Estimate(req Request, now time.Time) Estimate {
	e := Estimate{SnapshotID: s.id, RecordedAt: now, PriorityMultiplier: 1}
	if req.Usage == nil || (req.Status != Reported && req.Status != Estimated) {
		e.UnknownReason = MissingUsage
		return e
	}
	tokens, ok := Normalize(*req.Usage)
	if !ok {
		e.UnknownReason = InvalidUsage
		return e
	}
	e.Tokens = tokens
	price, ok := s.ResolveForTier(req.Provider, req.Model, req.Resolution, req.Tier)
	if !ok {
		e.UnknownReason = MissingPrice
		return e
	}
	e.Price = price
	tier := req.Tier.normalized()
	e.Tier = copyTier(req.Tier)
	if price.OptionalRates != nil {
		e.EffectiveOptionalRates = copyOptional(price.OptionalRates)
		cost, known := calculateOptional(tokens, price.OptionalRates)
		if !known {
			e.UnknownReason = MissingPrice
			return e
		}
		e.Known = true
		e.Cost = cost
		e.Estimated = req.Usage.Estimated || req.Status == Estimated
		return e
	}
	confirmed := fastTier(tier.Response)
	r := price.Rates
	var rule *contextRule
	for i := range s.rules.Context {
		c := &s.rules.Context[i]
		if c.Provider == family(price.Provider) && c.Model == req.Model {
			rule = c
			break
		}
	}
	p := price.PromptPolicy
	custom := p != nil && p.Mode == Custom
	crossed := false
	if custom {
		crossed = *req.Usage.InputTokens > p.ThresholdTokens || (p.Comparison == GreaterEqual && *req.Usage.InputTokens == p.ThresholdTokens)
	} else if (p == nil || p.Mode != Flat) && rule != nil {
		crossed = *req.Usage.InputTokens > rule.Threshold || (rule.Inclusive && *req.Usage.InputTokens == rule.Threshold)
	}
	if crossed && !(confirmed && rule != nil && rule.Relation == "exclusive") {
		if custom {
			r = p.Rates
			e.CustomThresholdApplied = true
		} else {
			r = scale(r, rule.Multiplier)
			e.ContextTier = "long"
		}
		e.PriorityLowerBound = confirmed && rule != nil && rule.Relation == "lower-bound"
	}
	if price.ServiceTier == "" && fastTier(tier.effective()) && !(e.ContextTier != "" && (rule == nil || rule.Relation != "stack")) && !(e.CustomThresholdApplied && confirmed && rule != nil && rule.Relation == "lower-bound") {
		for _, pr := range s.rules.Priority {
			if pr.Provider == price.Provider && pr.Model == req.Model && (!pr.RequiresResponseConfirmation || confirmed) {
				e.PriorityMultiplier = pr.Multiplier
				r = uniform(r, pr.Multiplier)
				break
			}
		}
	}
	o := req.Tier.Outcome
	if price.Provider == "openrouter" && o != nil && o.Canonical == "priority" && o.FastOutcome == "applied" && (o.Confirmation == "confirmed" || o.Confirmation == "assumed") {
		e.PriorityLowerBound = true
	}
	e.Known = true
	e.EffectiveRates = r
	e.Cost = Calculate(tokens, r)
	e.Estimated = req.Usage.Estimated || req.Status == Estimated || price.Status == Derived
	return e
}
func copyTier(t Tier) Tier {
	copyString := func(p *string) *string {
		if p == nil {
			return nil
		}
		s := *p
		return &s
	}
	t.Response = copyString(t.Response)
	t.Requested = copyString(t.Requested)
	t.Configured = copyString(t.Configured)
	if t.Outcome != nil {
		o := *t.Outcome
		o.ResponseServiceTier = copyString(o.ResponseServiceTier)
		if o.ResponseTierAuthoritative != nil {
			b := *o.ResponseTierAuthoritative
			o.ResponseTierAuthoritative = &b
		}
		t.Outcome = &o
	}
	return t
}

type Aggregate struct {
	KnownCost                    float64
	KnownRecords, MissingRecords int64
}

func (a *Aggregate) Add(e Estimate) {
	if e.Known {
		a.KnownCost += e.Cost.Total
		a.KnownRecords++
	} else {
		a.MissingRecords++
	}
}

type Combo struct {
	Known              bool
	Cost               Breakdown
	Attempts           []Estimate
	MissingAttempts    int
	PriorityLowerBound bool
}

func (s *Snapshot) EstimateCombo(requests []Request, now time.Time) Combo {
	c := Combo{Known: len(requests) > 0, PriorityLowerBound: len(requests) > 0}
	for _, r := range requests {
		e := s.Estimate(r, now)
		c.Attempts = append(c.Attempts, e)
		if !e.Known {
			c.Known = false
			c.MissingAttempts++
		}
		c.Cost.Input += e.Cost.Input
		c.Cost.Output += e.Cost.Output
		c.Cost.CacheRead += e.Cost.CacheRead
		c.Cost.CacheWrite += e.Cost.CacheWrite
		c.Cost.Total += e.Cost.Total
		c.PriorityLowerBound = c.PriorityLowerBound && e.PriorityLowerBound
	}
	if !c.Known {
		c.Cost = Breakdown{}
		c.PriorityLowerBound = false
	}
	return c
}
func (s *Snapshot) String() string { return fmt.Sprintf("pricing snapshot %s", s.id) }
