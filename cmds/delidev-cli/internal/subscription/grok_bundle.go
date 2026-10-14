// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	// GrokIssuer is the only OIDC issuer a managed Grok login may use.
	GrokIssuer = "https://auth.x.ai"
	// GrokClientID is the public Grok Build CLI OIDC client.
	GrokClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	// GrokScopeKey is the single native auth.json key: "<issuer>::<client-id>".
	GrokScopeKey = GrokIssuer + "::" + GrokClientID

	// MaxGrokAuthFile bounds one native auth.json document.
	MaxGrokAuthFile = 64 << 10
	// MaxGrokBundleEnvelope bounds the vault object that wraps one auth.json.
	MaxGrokBundleEnvelope = MaxGrokAuthFile + 1<<10

	grokBundleVersion     = 1
	grokBundleKind        = "grok-auth-bundle"
	grokAuthMode          = "oidc"
	maxGrokSecretField    = 24 << 10
	maxGrokIdentityField  = 512
	maxGrokProfileField   = 2 << 10
	grokIdentityDomain    = "delidev/subscription/identity/v1"
	grokRedactedLabel     = "grok-bundle"
	grokServiceIdentifier = string(domain.SubscriptionGrok)
)

// GrokBundleInvalid is the stable, value-free failure for every malformed,
// foreign, rebound or oversized Grok bundle. It never echoes input.
func GrokBundleInvalid() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The managed Grok authentication evidence is invalid or incomplete.", "Retain exclusive ownership and reconcile the original operation; never import or redistribute another login.")
}

// grokAuthEntry is the closed native auth.json scope entry. Optional native
// profile fields are pointers so a semantic round trip never invents values.
type grokAuthEntry struct {
	Key                       string  `json:"key"`
	AuthMode                  string  `json:"auth_mode"`
	CreateTime                string  `json:"create_time"`
	UserID                    string  `json:"user_id"`
	Email                     *string `json:"email,omitempty"`
	FirstName                 *string `json:"first_name,omitempty"`
	ProfileImageAssetID       *string `json:"profile_image_asset_id,omitempty"`
	PrincipalType             string  `json:"principal_type"`
	PrincipalID               string  `json:"principal_id"`
	TeamID                    *string `json:"team_id,omitempty"`
	CodingDataRetentionOptOut *bool   `json:"coding_data_retention_opt_out,omitempty"`
	RefreshToken              string  `json:"refresh_token"`
	ExpiresAt                 string  `json:"expires_at"`
	OIDCIssuer                string  `json:"oidc_issuer"`
	OIDCClientID              string  `json:"oidc_client_id"`
}

// GrokIdentity is the parsed, non-secret ownership evidence of one login.
type GrokIdentity struct {
	UserID        string
	PrincipalType string
	PrincipalID   string
	TeamID        string
	Expiry        time.Time
}

// GrokBundle holds exactly one native auth.json scope entry. Every accessor and
// formatter is redacted; callers must call Clear after the bytes were used.
type GrokBundle struct {
	entry    grokAuthEntry
	identity GrokIdentity
}

// Identity returns the parsed identity fields (no credential material).
func (b GrokBundle) Identity() GrokIdentity { return b.identity }

// Clear drops every reference held by the bundle. Go strings are immutable, so
// callers must also clear the raw buffers they parsed or materialized.
func (b *GrokBundle) Clear() { *b = GrokBundle{} }

// IdentityKey is a domain-separated SHA-256 over service, issuer, user_id,
// principal_type and principal_id. Each field is length-prefixed so field
// boundaries cannot be shifted. It is 64 lowercase hex characters, valid as a
// SubscriptionState identity commitment input, and distinguishes principals of
// the same user. Servers should persist a keyed commitment of this key.
func (b GrokBundle) IdentityKey() string {
	if b.identity.UserID == "" {
		return ""
	}
	return GrokIdentityKey(b.identity.UserID, b.identity.PrincipalType, b.identity.PrincipalID)
}

// GrokIdentityKey computes the identity key for the fixed Grok issuer.
func GrokIdentityKey(userID, principalType, principalID string) string {
	h := sha256.New()
	for _, field := range []string{grokIdentityDomain, grokServiceIdentifier, GrokIssuer, userID, principalType, principalID} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		h.Write(length[:])
		h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// opaque returns a short stable hash that identifies a bundle in diagnostics
// without revealing any field value.
func (b GrokBundle) opaque() string {
	key := b.IdentityKey()
	if key == "" {
		return "none"
	}
	return key[:12]
}

// String, GoString, Format and LogValue reveal only the service and an opaque
// identity hash, for every verb including %v, %+v and %#v.
func (b GrokBundle) String() string {
	return fmt.Sprintf("%s{service=%s identity=%s}", grokRedactedLabel, grokServiceIdentifier, b.opaque())
}
func (b GrokBundle) GoString() string { return b.String() }
func (b GrokBundle) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(b.String()))
}
func (b GrokBundle) LogValue() slog.Value {
	return slog.GroupValue(slog.String("service", grokServiceIdentifier), slog.String("identity", b.opaque()))
}

// MarshalJSON refuses to serialize the bundle through generic encoders so a
// credential cannot leak through an accidental json.Marshal of a parent value.
func (b GrokBundle) MarshalJSON() ([]byte, error) { return nil, GrokBundleInvalid() }

// MarshalText also refuses, because encoders prefer it for map keys and text.
func (b GrokBundle) MarshalText() ([]byte, error) { return nil, GrokBundleInvalid() }

func grokControlFree(value string, max int, required bool) bool {
	if required && value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func grokOptional(value *string, max int) bool {
	return value == nil || grokControlFree(*value, max, false)
}

func grokTime(value string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, value)
	return t.UTC(), err == nil
}

func grokSecret(value string) bool {
	return grokControlFree(value, maxGrokSecretField, true) && !strings.ContainsAny(value, " \t")
}

// marshalGrokJSON encodes value as compact JSON without HTML escaping. The
// default encoder rewrites <, > and & as six-byte \u escapes, so a bounded
// native file could grow past its own limit when re-emitted. Native Grok reads
// either spelling, and the bytes decode to identical strings.
func marshalGrokJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		clear(out.Bytes())
		return nil, err
	}
	raw := bytes.Clone(bytes.TrimSuffix(out.Bytes(), []byte("\n")))
	clear(out.Bytes())
	return raw, nil
}

// ParseGrokAuthFile validates raw native $GROK_HOME/auth.json bytes. The file
// must contain exactly the one pinned scope entry; unknown fields, duplicate
// keys, other scopes and mismatched issuer or client are rejected.
func ParseGrokAuthFile(raw []byte) (GrokBundle, error) {
	if len(raw) == 0 || len(raw) > MaxGrokAuthFile {
		return GrokBundle{}, GrokBundleInvalid()
	}
	var scopes map[string]json.RawMessage
	if domain.DecodeWithLimit(raw, &scopes, MaxGrokAuthFile) != nil || len(scopes) != 1 {
		return GrokBundle{}, GrokBundleInvalid()
	}
	value, ok := scopes[GrokScopeKey]
	if !ok {
		return GrokBundle{}, GrokBundleInvalid()
	}
	var entry grokAuthEntry
	if domain.DecodeWithLimit(value, &entry, MaxGrokAuthFile) != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	created, createdOK := grokTime(entry.CreateTime)
	expiry, expiryOK := grokTime(entry.ExpiresAt)
	if !createdOK || !expiryOK || !expiry.After(created) || entry.AuthMode != grokAuthMode ||
		entry.OIDCIssuer != GrokIssuer || entry.OIDCClientID != GrokClientID ||
		!grokSecret(entry.Key) || !grokSecret(entry.RefreshToken) ||
		!grokControlFree(entry.UserID, maxGrokIdentityField, true) ||
		!grokControlFree(entry.PrincipalType, maxGrokIdentityField, true) ||
		!grokControlFree(entry.PrincipalID, maxGrokIdentityField, true) ||
		!grokOptional(entry.TeamID, maxGrokIdentityField) ||
		!grokOptional(entry.Email, maxGrokProfileField) || !grokOptional(entry.FirstName, maxGrokProfileField) ||
		!grokOptional(entry.ProfileImageAssetID, maxGrokProfileField) {
		return GrokBundle{}, GrokBundleInvalid()
	}
	identity := GrokIdentity{UserID: entry.UserID, PrincipalType: entry.PrincipalType, PrincipalID: entry.PrincipalID, Expiry: expiry}
	if entry.TeamID != nil {
		identity.TeamID = *entry.TeamID
	}
	bundle := GrokBundle{entry: entry, identity: identity}
	// The accepted input bound alone does not bound the canonical re-encoding
	// (for example U+2028 and U+2029 always expand to six-byte escapes). Accept
	// only what AuthFile, and therefore SealGrokBundle, can always emit again.
	reencoded, err := bundle.AuthFile()
	if err != nil {
		bundle.Clear()
		return GrokBundle{}, GrokBundleInvalid()
	}
	clear(reencoded)
	return bundle, nil
}

// AuthFile returns the native auth.json bytes (one scope entry) for Worker
// materialization. The caller owns and must clear the returned buffer.
func (b GrokBundle) AuthFile() ([]byte, error) {
	if b.entry.Key == "" {
		return nil, GrokBundleInvalid()
	}
	raw, err := marshalGrokJSON(map[string]grokAuthEntry{GrokScopeKey: b.entry})
	if err != nil || len(raw) > MaxGrokAuthFile {
		clear(raw)
		return nil, GrokBundleInvalid()
	}
	return raw, nil
}

type grokBundleEnvelope struct {
	Version    int             `json:"version"`
	Kind       string          `json:"kind"`
	Server     domain.ID       `json:"server"`
	Account    domain.ID       `json:"account"`
	Generation domain.ID       `json:"generation"`
	Auth       json.RawMessage `json:"auth"`
}

// SealGrokBundle builds the distinct, versioned vault envelope stored under the
// existing AccountLogin ownership, bound to one server, account and generation.
func SealGrokBundle(server, account, generation domain.ID, bundle GrokBundle) ([]byte, error) {
	for _, id := range []domain.ID{server, account, generation} {
		if id.Validate() != nil {
			return nil, GrokBundleInvalid()
		}
	}
	auth, err := bundle.AuthFile()
	if err != nil {
		return nil, err
	}
	defer clear(auth)
	raw, err := marshalGrokJSON(grokBundleEnvelope{Version: grokBundleVersion, Kind: grokBundleKind, Server: server, Account: account, Generation: generation, Auth: auth})
	if err != nil || len(raw) > MaxGrokBundleEnvelope {
		clear(raw)
		return nil, GrokBundleInvalid()
	}
	return raw, nil
}

// OpenGrokBundle validates the closed envelope, its exact ownership binding and
// the contained native file. A Codex bundle, Claude envelope, another account,
// server or generation fails with the same safe error.
func OpenGrokBundle(raw []byte, server, account, generation domain.ID) (GrokBundle, error) {
	var envelope grokBundleEnvelope
	if len(raw) == 0 || len(raw) > MaxGrokBundleEnvelope || domain.DecodeWithLimit(raw, &envelope, MaxGrokBundleEnvelope) != nil {
		return GrokBundle{}, GrokBundleInvalid()
	}
	defer clear(envelope.Auth)
	if envelope.Version != grokBundleVersion || envelope.Kind != grokBundleKind || envelope.Server != server || envelope.Account != account || envelope.Generation != generation {
		return GrokBundle{}, GrokBundleInvalid()
	}
	return ParseGrokAuthFile(bytes.Clone(envelope.Auth))
}

// GrokBundleEnvelope reports whether raw has the Grok envelope discriminator
// without validating or retaining its secret.
func GrokBundleEnvelope(raw []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	return len(raw) <= MaxGrokBundleEnvelope && json.Unmarshal(raw, &probe) == nil && probe.Kind == grokBundleKind
}
