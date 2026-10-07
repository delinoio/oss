// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	GrokVersion  = "1.0.46"
	GrokIssuer   = "https://auth.x.ai"
	GrokClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	GrokScope    = GrokIssuer + "::" + GrokClientID
)

type GrokAuthMode string

const GrokOIDC GrokAuthMode = "oidc"

// GrokAuth is the pinned native file shape. Parsing establishes consistency,
// never authentication: the server separately verifies the OAuth response and
// only an original leased native process can supply a refreshed file.
type GrokAuth struct {
	Key                string       `json:"key"`
	Mode               GrokAuthMode `json:"auth_mode"`
	Created            time.Time    `json:"create_time"`
	User               string       `json:"user_id"`
	Email              *string      `json:"email"`
	FirstName          *string      `json:"first_name,omitempty"`
	LastName           *string      `json:"last_name,omitempty"`
	Image              *string      `json:"profile_image_asset_id,omitempty"`
	PrincipalType      *string      `json:"principal_type,omitempty"`
	PrincipalID        *string      `json:"principal_id,omitempty"`
	TeamID             *string      `json:"team_id,omitempty"`
	TeamName           *string      `json:"team_name,omitempty"`
	TeamRole           *string      `json:"team_role,omitempty"`
	OrganizationID     *string      `json:"organization_id,omitempty"`
	OrganizationName   *string      `json:"organization_name,omitempty"`
	OrganizationRole   *string      `json:"organization_role,omitempty"`
	BlockedReason      *string      `json:"user_blocked_reason,omitempty"`
	TeamBlockedReasons []string     `json:"team_blocked_reasons,omitempty"`
	RetentionOptOut    bool         `json:"coding_data_retention_opt_out"`
	CanAdministerTeam  *bool        `json:"can_administer_team,omitempty"`
	CodeAccess         *bool        `json:"has_grok_code_access,omitempty"`
	Refresh            string       `json:"refresh_token"`
	Expires            time.Time    `json:"expires_at"`
	Issuer             string       `json:"oidc_issuer"`
	ClientID           string       `json:"oidc_client_id"`
}

func InvalidGrok() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The managed Grok authentication evidence is invalid or incomplete.", "Retain exclusive account ownership and reconcile the original operation. Do not import or redistribute an older login.")
}

func ParseGrok(raw []byte) (GrokAuth, Identity, error) {
	var store map[string]GrokAuth
	var identity Identity
	if len(raw) == 0 || len(raw) > MaxBundle || domain.Decode(raw, &store) != nil || len(store) != 1 {
		return GrokAuth{}, identity, InvalidGrok()
	}
	b, ok := store[GrokScope]
	if !ok || b.Mode != GrokOIDC || b.Issuer != GrokIssuer || b.ClientID != GrokClientID || b.Created.IsZero() || b.Created.After(time.Now().UTC().Add(time.Minute)) || !b.Expires.After(b.Created) || !grokOpaque(b.User, 256) || !grokOpaque(b.Key, 24<<10) || !grokOpaque(b.Refresh, 24<<10) {
		return GrokAuth{}, identity, InvalidGrok()
	}
	// A missing native principal pair is the personal user principal. A partial
	// pair cannot be reconstructed from an alias, team display name or token hint.
	principalType, principalID := "User", b.User
	if (b.PrincipalType == nil) != (b.PrincipalID == nil) {
		return GrokAuth{}, identity, InvalidGrok()
	}
	if b.PrincipalType != nil {
		principalType, principalID = *b.PrincipalType, *b.PrincipalID
		if !grokOpaque(principalID, 256) || principalType != "User" && principalType != "Team" || principalType == "User" && principalID != b.User || principalType == "Team" && (b.TeamID == nil || *b.TeamID != principalID) {
			return GrokAuth{}, identity, InvalidGrok()
		}
	}
	for _, value := range []*string{b.Email, b.FirstName, b.LastName, b.Image, b.TeamID, b.TeamName, b.TeamRole, b.OrganizationID, b.OrganizationName, b.OrganizationRole, b.BlockedReason} {
		if value != nil && domain.Text(*value, "native metadata", 1024, false) != nil {
			return GrokAuth{}, identity, InvalidGrok()
		}
	}
	if len(b.TeamBlockedReasons) > 16 {
		return GrokAuth{}, identity, InvalidGrok()
	}
	for _, value := range b.TeamBlockedReasons {
		if domain.Text(value, "native metadata", 256, true) != nil {
			return GrokAuth{}, identity, InvalidGrok()
		}
	}
	identity = Identity{Account: principalID, User: b.User, Service: domain.SubscriptionGrok, Issuer: GrokIssuer, PrincipalType: principalType, PrincipalID: principalID}
	if b.Email != nil {
		identity.Email = *b.Email
	}
	if b.FirstName != nil {
		identity.DisplayName = *b.FirstName
	}
	if b.LastName != nil {
		identity.DisplayName = strings.TrimSpace(identity.DisplayName + " " + *b.LastName)
	}
	return b, identity, nil
}

func grokOpaque(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for _, c := range []byte(value) {
		if c <= 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

func ParseService(service domain.SubscriptionService, raw []byte) (Identity, error) {
	switch service {
	case domain.SubscriptionChatGPT:
		_, identity, err := Parse(raw)
		return identity, err
	case domain.SubscriptionGrok:
		_, identity, err := ParseGrok(raw)
		return identity, err
	default:
		return Identity{}, domain.Fail(domain.Unsupported, "The subscription service has no managed bundle profile.", "Use the original service-specific authentication owner.")
	}
}

// CommitmentInput preserves historical Codex bytes. Grok ownership includes its
// complete service/issuer/user/principal tuple in a distinct closed shape.
func CommitmentInput(identity Identity) []byte {
	var raw []byte
	if identity.Service == domain.SubscriptionGrok {
		raw, _ = json.Marshal(struct {
			Service                                  domain.SubscriptionService
			Issuer, User, PrincipalType, PrincipalID string
		}{identity.Service, identity.Issuer, identity.User, identity.PrincipalType, identity.PrincipalID})
	} else {
		raw, _ = json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
	}
	return raw
}

func RefreshedService(service domain.SubscriptionService, before, after []byte) error {
	if service == domain.SubscriptionChatGPT {
		return Refreshed(before, after)
	}
	if service != domain.SubscriptionGrok {
		_, err := ParseService(service, after)
		return err
	}
	a, ai, err := ParseGrok(before)
	if err != nil {
		return err
	}
	b, bi, err := ParseGrok(after)
	if err != nil {
		return err
	}
	if !bytes.Equal(CommitmentInput(ai), CommitmentInput(bi)) || !b.Created.After(a.Created) || bytes.Equal(before, after) || a.Key == b.Key && a.Refresh == b.Refresh || !b.Expires.After(a.Expires) {
		return InvalidGrok()
	}
	return nil
}
