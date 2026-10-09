// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

type nativeQuotaWindow struct {
	UsedPercent *int32 `json:"usedPercent"`
	Duration    *int64 `json:"windowDurationMins"`
	Reset       *int64 `json:"resetsAt"`
}
type nativeQuotaSnapshot struct {
	NormalModelSlug *string            `json:"normalModelSlug,omitempty"`
	LimitID         *string            `json:"limitId"`
	LimitName       *string            `json:"limitName"`
	Primary         *nativeQuotaWindow `json:"primary"`
	Secondary       *nativeQuotaWindow `json:"secondary"`
	Credits         *nativePaidCredits `json:"credits"`
	Individual      *struct {
		Limit     string `json:"limit"`
		Used      string `json:"used"`
		Remaining int32  `json:"remainingPercent"`
		Reset     int64  `json:"resetsAt"`
	} `json:"individualLimit"`
	SpendReached *bool   `json:"spendControlReached"`
	Plan         *string `json:"planType"`
	ReachedType  *string `json:"rateLimitReachedType"`
}

// An omitted balance is a sparse read, not a new null-balance observation.
type nativePaidCredits struct {
	HasCredits     *bool   `json:"hasCredits"`
	Unlimited      *bool   `json:"unlimited"`
	Balance        *string `json:"balance"`
	BalancePresent bool    `json:"-"`
}

func (v *nativePaidCredits) UnmarshalJSON(raw []byte) error {
	type wire nativePaidCredits
	var value wire
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	_, value.BalancePresent = fields["balance"]
	*v = nativePaidCredits(value)
	return nil
}

type nativeResetCredit struct {
	ID          string  `json:"id"`
	ResetType   string  `json:"resetType"`
	Status      string  `json:"status"`
	Granted     int64   `json:"grantedAt"`
	Expires     *int64  `json:"expiresAt"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
}
type nativeQuotaRead struct {
	// Official optional observations are decoded privately and never projected.
	OrdinaryUsageAllowed *bool                          `json:"ordinaryUsageAllowed"`
	AccountID            *string                        `json:"accountId"`
	RateLimitUpsell      json.RawMessage                `json:"rateLimitUpsell"`
	Legacy               *nativeQuotaSnapshot           `json:"rateLimits"`
	Buckets              map[string]nativeQuotaSnapshot `json:"rateLimitsByLimitId"`
	ResetCredits         *struct {
		Count   *int64               `json:"availableCount"`
		Credits *[]nativeResetCredit `json:"credits"`
	} `json:"rateLimitResetCredits"`
}

// ReadManagedQuota uses the already authenticated original process. No token
// refresh, inference, billing request, custom endpoint or second writer occurs.
func (c *Client) ReadManagedQuota(ctx context.Context, observation domain.ID) (domain.SubscriptionQuotaObservation, error) {
	var value nativeQuotaRead
	if c.managedHome == "" || observation.Validate() != nil {
		return domain.SubscriptionQuotaObservation{}, incompatible()
	}
	if err := c.managedCall(ctx, "account/rateLimits/read", nativewire.OmittedParams{}, &value); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	result, err := projectQuota(value, observation, time.Now().UTC())
	if err != nil {
		return result, err
	}
	if err := c.validateQuotaReflection(result); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	return result, nil
}
func (c *Client) ConsumeManagedResetCredit(ctx context.Context, operation domain.SubscriptionObservationOperation) (domain.SubscriptionResetOutcome, error) {
	if c.managedHome == "" || operation.Validate() != nil || operation.Action != domain.SubscriptionResetCredit || operation.Phase != domain.SubscriptionObservationSending {
		return "", incompatible()
	}
	return c.consumeManagedResetCredit(ctx, operation.ID, operation.CreditID)
}

// ConsumeServerResetCredit requires the server's original durable send claim.
// It cannot turn a quota record or a renderer selector into consumption authority.
func (c *Client) ConsumeServerResetCredit(ctx context.Context, operation domain.ServerCreditOperation) (domain.SubscriptionResetOutcome, error) {
	if c.managedHome == "" || operation.Validate() != nil || operation.Phase != domain.SubscriptionObservationSending || !operation.SendClaimed {
		return "", incompatible()
	}
	return c.consumeManagedResetCredit(ctx, operation.ID, operation.CreditID)
}
func (c *Client) consumeManagedResetCredit(ctx context.Context, key domain.ID, credit string) (domain.SubscriptionResetOutcome, error) {
	var result struct {
		Outcome domain.SubscriptionResetOutcome `json:"outcome"`
	}
	input := struct {
		Key    string  `json:"idempotencyKey"`
		Credit *string `json:"creditId,omitempty"`
	}{Key: string(key)}
	if credit != "" {
		input.Credit = &credit
	}
	if err := c.managedCall(ctx, "account/rateLimitResetCredit/consume", input, &result); err != nil {
		return "", err
	}
	if !result.Outcome.Valid() {
		return "", domain.InvalidSubscriptionObservation()
	}
	return result.Outcome, nil
}
func projectQuota(value nativeQuotaRead, observation domain.ID, now time.Time) (domain.SubscriptionQuotaObservation, error) {
	result := domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{}}
	if value.Legacy == nil || len(value.Buckets) > 32 {
		return result, domain.InvalidSubscriptionObservation()
	}
	buckets := value.Buckets
	if len(buckets) == 0 {
		id := "codex"
		if value.Legacy.LimitID != nil {
			id = *value.Legacy.LimitID
		}
		buckets = map[string]nativeQuotaSnapshot{id: *value.Legacy}
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		snapshot := buckets[key]
		if !domain.ValidSubscriptionOpaqueID(key) || len(key) > 110 || snapshot.LimitID != nil && *snapshot.LimitID != key {
			return result, domain.InvalidSubscriptionObservation()
		}
		if c := snapshot.Credits; c != nil && c.BalancePresent {
			result.PaidCredits = append(result.PaidCredits, domain.SubscriptionPaidCreditBucket{ID: key, HasCredits: c.HasCredits, Unlimited: c.Unlimited, Balance: c.Balance, ObservedAt: now})
		}
		for _, slot := range []struct {
			name   string
			window *nativeQuotaWindow
		}{{"primary", snapshot.Primary}, {"secondary", snapshot.Secondary}} {
			if slot.window == nil {
				continue
			}
			w := slot.window
			if w.UsedPercent == nil || *w.UsedPercent < 0 || *w.UsedPercent > 100 {
				return result, domain.InvalidSubscriptionObservation()
			}
			remaining := float64(100-*w.UsedPercent) / 100
			projected := domain.SubscriptionQuotaWindow{ID: key + ":" + slot.name, Remaining: &remaining, DurationMinutes: w.Duration}
			if w.Reset != nil {
				reset := time.Unix(*w.Reset, 0).UTC()
				projected.ResetAt = &reset
			}
			result.Windows = append(result.Windows, projected)
		}
		if snapshot.Individual != nil {
			remaining := float64(snapshot.Individual.Remaining) / 100
			reset := time.Unix(snapshot.Individual.Reset, 0).UTC()
			result.Windows = append(result.Windows, domain.SubscriptionQuotaWindow{ID: key + ":spend", Remaining: &remaining, ResetAt: &reset})
		}
		// One blocked bucket blocks the account. Sparse absence retains the last
		// successful spend-control observation rather than manufacturing recovery.
		if snapshot.SpendReached != nil {
			if result.SpendControlReached == nil {
				v := false
				result.SpendControlReached = &v
			}
			*result.SpendControlReached = *result.SpendControlReached || *snapshot.SpendReached
		}
	}
	if credits := value.ResetCredits; credits != nil {
		if credits.Count == nil {
			return result, domain.InvalidSubscriptionObservation()
		}
		projected := &domain.SubscriptionResetCredits{ObservationID: observation, ObservedAt: now, AvailableCount: *credits.Count}
		if credits.Credits != nil {
			items := make([]domain.SubscriptionResetCreditDetail, 0, len(*credits.Credits))
			for _, credit := range *credits.Credits {
				resetType := credit.ResetType
				if resetType != "codexRateLimits" {
					resetType = "unknown"
				}
				status := credit.Status
				if status != "available" && status != "redeeming" && status != "redeemed" {
					status = "unknown"
				}
				item := domain.SubscriptionResetCreditDetail{ID: credit.ID, ResetType: domain.SubscriptionResetType(resetType), Status: domain.SubscriptionCreditStatus(status), GrantedAt: time.Unix(credit.Granted, 0).UTC()}
				if credit.Expires != nil {
					v := time.Unix(*credit.Expires, 0).UTC()
					item.ExpiresAt = &v
				}
				items = append(items, item)
			}
			projected.Credits = &items
		}
		result.Credits = projected
	}
	if err := result.Validate(now); err != nil {
		return result, err
	}
	return result, nil
}
func (c *Client) validateQuotaReflection(value domain.SubscriptionQuotaObservation) error {
	raw, err := security.ReadPrivate(filepath.Join(c.managedHome, "auth.json"), subscription.MaxBundle)
	if err != nil {
		return subscription.Invalid()
	}
	defer clear(raw)
	bundle, identity, err := subscription.Parse(raw)
	if err != nil {
		return err
	}
	return ValidateQuotaSecrets(value, bundle.Tokens.Access, bundle.Tokens.Refresh, bundle.Tokens.ID, identity.Email, identity.Account, identity.User)
}

// ValidateQuotaSecrets keeps protected credentials and original account identity
// out of bounded quota identifiers, including common encoded reflections.
func ValidateQuotaSecrets(value domain.SubscriptionQuotaObservation, secrets ...string) error {
	projected, err := json.Marshal(value)
	if err != nil {
		return subscription.Invalid()
	}
	defer clear(projected)
	for _, token := range secrets {
		if token == "" {
			continue
		}
		if quotaIdentifierReflects(value, token) {
			return subscription.Invalid()
		}
		needle := []byte(token)
		if len(needle) >= 8 && bytes.Contains(projected, needle) {
			return subscription.Invalid()
		}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			encoded := []byte(encoding.EncodeToString(needle))
			found := quotaIdentifierReflects(value, string(encoded)) || len(needle) >= 8 && bytes.Contains(projected, encoded)
			clear(encoded)
			if found {
				return subscription.Invalid()
			}
		}
	}
	return nil
}

// Only the adapter-owned final window suffix is removed. Exact matching keeps
// short protected words from rejecting unrelated public metadata substrings.
func quotaIdentifierReflects(value domain.SubscriptionQuotaObservation, protected string) bool {
	for _, window := range value.Windows {
		id := window.ID
		for _, suffix := range []string{":primary", ":secondary", ":spend"} {
			if strings.HasSuffix(id, suffix) {
				id = strings.TrimSuffix(id, suffix)
				break
			}
		}
		if window.ID == protected || id == protected {
			return true
		}
	}
	for _, b := range value.PaidCredits {
		if b.ID == protected || b.Balance != nil && *b.Balance == protected {
			return true
		}
	}
	if value.Credits != nil && value.Credits.Credits != nil {
		for _, credit := range *value.Credits.Credits {
			if credit.ID == protected {
				return true
			}
		}
	}
	return false
}
