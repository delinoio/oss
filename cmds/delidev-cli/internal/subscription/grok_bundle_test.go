// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	grokTestUser      = "synthetic-user-0001"
	grokTestPrincipal = "synthetic-principal-0001"
	grokTestRefresh   = "synthetic-refresh-token-value"
	grokTestKey       = "synthetic-access-key-value"
	grokTestEmail     = "fixture@example.invalid"
	grokTestTeam      = "synthetic-team-0001"
)

// grokFixtureEntry builds a synthetic native scope entry as a generic map so a
// case can drop or replace any field.
func grokFixtureEntry() map[string]any {
	return map[string]any{
		"key": grokTestKey, "auth_mode": "oidc",
		"create_time": "2026-10-14T00:00:00.123456Z", "expires_at": "2026-10-14T06:00:00.123456Z",
		"user_id": grokTestUser, "email": grokTestEmail, "first_name": "Fixture", "profile_image_asset_id": "synthetic-asset",
		"principal_type": "user", "principal_id": grokTestPrincipal, "team_id": grokTestTeam,
		"coding_data_retention_opt_out": true, "refresh_token": grokTestRefresh,
		"oidc_issuer": GrokIssuer, "oidc_client_id": GrokClientID,
	}
}

func grokFixtureFile(t *testing.T, mutate func(entry map[string]any)) []byte {
	t.Helper()
	entry := grokFixtureEntry()
	if mutate != nil {
		mutate(entry)
	}
	raw, err := json.Marshal(map[string]any{GrokScopeKey: entry})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func grokSecretValues() []string {
	return []string{grokTestUser, grokTestPrincipal, grokTestRefresh, grokTestKey, grokTestEmail, grokTestTeam, GrokClientID}
}

func assertNoGrokValue(t *testing.T, label, text string) {
	t.Helper()
	for _, value := range grokSecretValues() {
		if strings.Contains(text, value) {
			t.Fatalf("%s exposed a bundle value", label)
		}
	}
}

func TestParseGrokAuthFileAcceptsValidBundle(t *testing.T) {
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal("valid bundle rejected")
	}
	id := bundle.Identity()
	if id.UserID != grokTestUser || id.PrincipalType != "user" || id.PrincipalID != grokTestPrincipal || id.TeamID != grokTestTeam || id.Expiry.IsZero() {
		t.Fatal("identity not parsed")
	}
	if !domain.NativeIdentityCommitmentValid(bundle.IdentityKey()) {
		t.Fatal("identity key is not a 32-byte hex commitment")
	}
	// Optional native fields may be absent.
	if _, err := ParseGrokAuthFile(grokFixtureFile(t, func(e map[string]any) {
		delete(e, "team_id")
		delete(e, "email")
		delete(e, "first_name")
		delete(e, "profile_image_asset_id")
		delete(e, "coding_data_retention_opt_out")
	})); err != nil {
		t.Fatal("bundle without optional fields rejected")
	}
}

func TestParseGrokAuthFileRejectsInvalidBundlesWithoutEchoingValues(t *testing.T) {
	otherScope := func(key string) []byte {
		raw, _ := json.Marshal(map[string]any{key: grokFixtureEntry()})
		return raw
	}
	twoEntries := func() []byte {
		raw, _ := json.Marshal(map[string]any{GrokScopeKey: grokFixtureEntry(), "https://auth.x.ai::other-client": grokFixtureEntry()})
		return raw
	}
	cases := map[string][]byte{
		"empty":                  nil,
		"not-json":               []byte("{" + grokTestRefresh),
		"array":                  []byte("[]"),
		"empty-object":           []byte("{}"),
		"other-issuer-key":       otherScope("https://auth.example.invalid::" + GrokClientID),
		"other-client-key":       otherScope(GrokIssuer + "::other-client"),
		"unprefixed-key":         otherScope("default"),
		"two-entries":            twoEntries(),
		"oversize":               append([]byte(`{"`+GrokScopeKey+`":{"key":"`), bytes.Repeat([]byte("a"), MaxGrokAuthFile)...),
		"duplicate-top-key":      []byte(`{"` + GrokScopeKey + `":{},"` + GrokScopeKey + `":{}}`),
		"duplicate-entry-key":    []byte(`{"` + GrokScopeKey + `":{"key":"` + grokTestKey + `","key":"` + grokTestKey + `"}}`),
		"trailing-data":          append(grokFixtureFile(t, nil), []byte(` {"x":1}`)...),
		"unknown-field":          grokFixtureFile(t, func(e map[string]any) { e["extra_secret"] = grokTestRefresh }),
		"empty-refresh":          grokFixtureFile(t, func(e map[string]any) { e["refresh_token"] = "" }),
		"missing-refresh":        grokFixtureFile(t, func(e map[string]any) { delete(e, "refresh_token") }),
		"empty-key":              grokFixtureFile(t, func(e map[string]any) { e["key"] = "" }),
		"whitespace-key":         grokFixtureFile(t, func(e map[string]any) { e["key"] = "a b" }),
		"control-in-refresh":     grokFixtureFile(t, func(e map[string]any) { e["refresh_token"] = "abc\ndef" }),
		"huge-refresh":           grokFixtureFile(t, func(e map[string]any) { e["refresh_token"] = strings.Repeat("a", maxGrokSecretField+1) }),
		"bad-create-time":        grokFixtureFile(t, func(e map[string]any) { e["create_time"] = "yesterday" }),
		"bad-expires-at":         grokFixtureFile(t, func(e map[string]any) { e["expires_at"] = "2026-13-40T00:00:00Z" }),
		"missing-expires-at":     grokFixtureFile(t, func(e map[string]any) { delete(e, "expires_at") }),
		"expiry-before-creation": grokFixtureFile(t, func(e map[string]any) { e["expires_at"] = "2026-10-13T00:00:00Z" }),
		"wrong-auth-mode":        grokFixtureFile(t, func(e map[string]any) { e["auth_mode"] = "api_key" }),
		"entry-issuer-differs":   grokFixtureFile(t, func(e map[string]any) { e["oidc_issuer"] = "https://auth.example.invalid" }),
		"entry-client-differs":   grokFixtureFile(t, func(e map[string]any) { e["oidc_client_id"] = "other-client" }),
		"missing-user":           grokFixtureFile(t, func(e map[string]any) { delete(e, "user_id") }),
		"missing-principal-type": grokFixtureFile(t, func(e map[string]any) { e["principal_type"] = "" }),
		"missing-principal-id":   grokFixtureFile(t, func(e map[string]any) { delete(e, "principal_id") }),
		"wrong-type-team":        grokFixtureFile(t, func(e map[string]any) { e["team_id"] = 7 }),
		"control-in-user":        grokFixtureFile(t, func(e map[string]any) { e["user_id"] = "user\x00" }),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			bundle, err := ParseGrokAuthFile(raw)
			if err == nil {
				t.Fatal("invalid bundle accepted")
			}
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != domain.RecoveryRequired || err.Error() != GrokBundleInvalid().Error() {
				t.Fatal("rejection is not the stable safe error")
			}
			assertNoGrokValue(t, "error", err.Error()+fmt.Sprintf("%v", failure))
			if bundle.IdentityKey() != "" {
				t.Fatal("rejected parse returned an identity")
			}
		})
	}
}

func TestGrokBundleAuthFileRoundTripsSemantically(t *testing.T) {
	original := grokFixtureFile(t, nil)
	bundle, err := ParseGrokAuthFile(original)
	if err != nil {
		t.Fatal(err)
	}
	native, err := bundle.AuthFile()
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]map[string]any
	if json.Unmarshal(original, &want) != nil || json.Unmarshal(native, &got) != nil {
		t.Fatal("round trip is not JSON")
	}
	// Timestamps are compared as instants, every other field exactly.
	for _, field := range []string{"create_time", "expires_at"} {
		a, _ := grokTime(want[GrokScopeKey][field].(string))
		b, _ := grokTime(got[GrokScopeKey][field].(string))
		if !a.Equal(b) {
			t.Fatal("timestamp changed")
		}
		delete(want[GrokScopeKey], field)
		delete(got[GrokScopeKey], field)
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Fatal("native fields changed in the round trip")
	}
	again, err := ParseGrokAuthFile(native)
	if err != nil || again.IdentityKey() != bundle.IdentityKey() {
		t.Fatal("re-parse changed the identity")
	}
	// Absent optional fields stay absent.
	sparse, err := ParseGrokAuthFile(grokFixtureFile(t, func(e map[string]any) { delete(e, "team_id"); delete(e, "email") }))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := sparse.AuthFile()
	if bytes.Contains(out, []byte(`"team_id"`)) || bytes.Contains(out, []byte(`"email"`)) {
		t.Fatal("absent optional field was invented")
	}
}

func TestGrokBundleRedaction(t *testing.T) {
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("grok_bundle", "bundle", bundle)
	logger.Info("grok_bundle_pointer", slog.Any("bundle", &bundle))
	textLogger := slog.New(slog.NewTextHandler(&logs, nil))
	textLogger.Info("grok_bundle", "bundle", bundle)
	outputs := map[string]string{
		"%v": fmt.Sprintf("%v", bundle), "%+v": fmt.Sprintf("%+v", bundle), "%#v": fmt.Sprintf("%#v", bundle),
		"%s": fmt.Sprintf("%s", bundle), "%v-pointer": fmt.Sprintf("%v", &bundle), "%+v-pointer": fmt.Sprintf("%+v", &bundle),
		"String": bundle.String(), "slog": logs.String(),
		"%v-struct": fmt.Sprintf("%+v", struct{ B GrokBundle }{bundle}),
	}
	for label, text := range outputs {
		assertNoGrokValue(t, label, text)
		if !strings.Contains(text, "grok") {
			t.Fatalf("%s does not identify the service", label)
		}
	}
	if !strings.Contains(bundle.String(), bundle.IdentityKey()[:12]) || strings.Contains(bundle.String(), bundle.IdentityKey()) {
		t.Fatal("redacted form must carry only a short opaque identity hash")
	}
	if _, err := json.Marshal(bundle); err == nil {
		t.Fatal("generic JSON encoding of a bundle must fail")
	} else {
		assertNoGrokValue(t, "json error", err.Error())
	}
	if _, err := json.Marshal(map[string]any{"nested": &bundle}); err == nil {
		t.Fatal("nested JSON encoding of a bundle must fail")
	}
	if got := (GrokBundle{}).String(); !strings.Contains(got, "none") {
		t.Fatal("empty bundle form unexpected")
	}
}

func TestGrokIdentityKeyBindsServiceIssuerUserAndPrincipal(t *testing.T) {
	base, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	key := base.IdentityKey()
	if len(key) != 64 || key != GrokIdentityKey(grokTestUser, "user", grokTestPrincipal) {
		t.Fatal("identity key is not the documented SHA-256")
	}
	rotated, _ := ParseGrokAuthFile(grokFixtureFile(t, func(e map[string]any) {
		e["refresh_token"], e["key"], e["team_id"], e["email"] = "other-refresh", "other-key", "other-team", "other@example.invalid"
		e["expires_at"] = "2026-10-14T09:00:00Z"
	}))
	if rotated.IdentityKey() != key {
		t.Fatal("token rotation or profile metadata changed the identity")
	}
	for name, mutate := range map[string]func(map[string]any){
		"principal-id":   func(e map[string]any) { e["principal_id"] = "synthetic-principal-0002" },
		"principal-type": func(e map[string]any) { e["principal_type"] = "team" },
		"user":           func(e map[string]any) { e["user_id"] = "synthetic-user-0002" },
	} {
		other, err := ParseGrokAuthFile(grokFixtureFile(t, mutate))
		if err != nil || other.IdentityKey() == key {
			t.Fatalf("%s did not change the identity key", name)
		}
	}
	// Field boundaries cannot be shifted between adjacent identity fields.
	if GrokIdentityKey("ab", "c", "d") == GrokIdentityKey("a", "bc", "d") || GrokIdentityKey("a", "b", "cd") == GrokIdentityKey("a", "bc", "d") {
		t.Fatal("identity fields are ambiguous")
	}
}

func TestGrokBundleEnvelopeBindsOwnershipAndService(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealGrokBundle(server, account, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !GrokBundleEnvelope(sealed) || ClaudeTokenEnvelope(sealed) {
		t.Fatal("envelope discriminator is not distinct")
	}
	opened, err := OpenGrokBundle(sealed, server, account, generation)
	if err != nil || opened.IdentityKey() != bundle.IdentityKey() {
		t.Fatal("sealed bundle did not reopen")
	}
	for name, open := range map[string]func() error{
		"server":     func() error { _, err := OpenGrokBundle(sealed, domain.NewID(), account, generation); return err },
		"account":    func() error { _, err := OpenGrokBundle(sealed, server, domain.NewID(), generation); return err },
		"generation": func() error { _, err := OpenGrokBundle(sealed, server, account, domain.NewID()); return err },
		"native-file": func() error {
			_, err := OpenGrokBundle(grokFixtureFile(t, nil), server, account, generation)
			return err
		},
		"codex-bundle": func() error {
			_, err := OpenGrokBundle(fixtureBundle("account-fixture", "user-fixture", "first", bundle.identity.Expiry), server, account, generation)
			return err
		},
		"truncated": func() error {
			_, err := OpenGrokBundle(sealed[:len(sealed)/2], server, account, generation)
			return err
		},
		"empty": func() error { _, err := OpenGrokBundle(nil, server, account, generation); return err },
		"wrong-version": func() error {
			_, err := OpenGrokBundle(bytes.Replace(sealed, []byte(`"version":1`), []byte(`"version":2`), 1), server, account, generation)
			return err
		},
	} {
		if err := open(); err == nil || err.Error() != GrokBundleInvalid().Error() {
			t.Fatalf("%s rebinding was not rejected with the safe error", name)
		}
	}
	if _, err := SealGrokBundle("", account, generation, bundle); err == nil {
		t.Fatal("sealing without a server id succeeded")
	}
	if _, err := SealGrokBundle(server, account, generation, GrokBundle{}); err == nil {
		t.Fatal("sealing an empty bundle succeeded")
	}
	if GrokBundleEnvelope(fixtureBundle("account-fixture", "user-fixture", "first", bundle.identity.Expiry)) {
		t.Fatal("Codex bundle matched the Grok discriminator")
	}
}

func TestGrokBundleClearDropsReferences(t *testing.T) {
	bundle, _ := ParseGrokAuthFile(grokFixtureFile(t, nil))
	bundle.Clear()
	if bundle.IdentityKey() != "" {
		t.Fatal("cleared bundle kept an identity")
	}
	if _, err := bundle.AuthFile(); err == nil {
		t.Fatal("cleared bundle materialized a file")
	}
}

// grokRawFile encodes a native file the way the native client does: without
// HTML escaping, so bounded fields keep their literal one-byte characters.
func grokRawFile(t *testing.T, mutate func(entry map[string]any)) []byte {
	t.Helper()
	entry := grokFixtureEntry()
	if mutate != nil {
		mutate(entry)
	}
	raw, err := marshalGrokJSON(map[string]any{GrokScopeKey: entry})
	if err != nil {
		t.Fatal(err)
	}
	// The encoder always escapes the JavaScript line separators; a native
	// writer may emit them literally, so restore the literal three-byte form.
	raw = bytes.ReplaceAll(raw, []byte("\\u2028"), []byte("\u2028"))
	raw = bytes.ReplaceAll(raw, []byte("\\u2029"), []byte("\u2029"))
	return raw
}

func grokBundleIDs() (domain.ID, domain.ID, domain.ID) {
	return domain.NewID(), domain.NewID(), domain.NewID()
}

// An accepted native file must always be emitted again. HTML-sensitive
// characters are one byte in the native file but six bytes when escaped, so the
// canonical encoding must not escape them.
func TestGrokBundleReencodesAcceptedHTMLSensitiveFieldsWithinBound(t *testing.T) {
	for _, ch := range []string{"<", ">", "&"} {
		t.Run(ch, func(t *testing.T) {
			key := strings.Repeat(ch, 12<<10)
			raw := grokRawFile(t, func(e map[string]any) { e["key"] = key; e["refresh_token"] = strings.Repeat(ch, 12<<10) })
			if len(raw) > MaxGrokAuthFile {
				t.Fatalf("fixture is not within the input bound: %d", len(raw))
			}
			bundle, err := ParseGrokAuthFile(raw)
			if err != nil {
				t.Fatal("a bounded HTML-sensitive file was rejected")
			}
			native, err := bundle.AuthFile()
			if err != nil {
				t.Fatal("an accepted bundle could not be re-encoded")
			}
			if bytes.Contains(native, []byte("\\u003")) || bytes.Contains(native, []byte("\\u0026")) || len(native) > MaxGrokAuthFile {
				t.Fatal("HTML-sensitive characters were expanded")
			}
			server, account, generation := grokBundleIDs()
			sealed, err := SealGrokBundle(server, account, generation, bundle)
			if err != nil {
				t.Fatal("an accepted bundle could not be sealed")
			}
			if len(sealed) > MaxGrokBundleEnvelope {
				t.Fatal("the sealed envelope exceeds its bound")
			}
			opened, err := OpenGrokBundle(sealed, server, account, generation)
			if err != nil || !opened.Equal(bundle) || opened.entry.Key != key {
				t.Fatal("the sealed bundle did not round trip semantically")
			}
		})
	}
}

// Whatever the input bound accepts, the canonical encoding and the sealed
// envelope must succeed; anything whose canonical form cannot fit is rejected
// at parse time instead of failing later during seal.
func TestGrokBundleAcceptedInputAlwaysReencodesAndSeals(t *testing.T) {
	server, account, generation := grokBundleIDs()
	for _, unit := range []string{"a", "<", "&", " ", " ", "é", "漢", "😀", `"`, `\`, "\u007f"[:0] + "~"} {
		for _, size := range []int{1 << 10, 8 << 10, 12 << 10, 20 << 10, 24 << 10} {
			count := size / len(unit)
			if count == 0 {
				continue
			}
			field := strings.Repeat(unit, count)
			if len(field) > maxGrokSecretField {
				field = field[:maxGrokSecretField-maxGrokSecretField%len(unit)]
			}
			raw := grokRawFile(t, func(e map[string]any) {
				e["key"], e["refresh_token"] = field, field
				e["email"], e["first_name"], e["profile_image_asset_id"] = strings.Repeat(unit, maxGrokProfileField/len(unit)), strings.Repeat(unit, maxGrokProfileField/len(unit)), strings.Repeat(unit, maxGrokProfileField/len(unit))
			})
			bundle, err := ParseGrokAuthFile(raw)
			if err != nil {
				if !errors.Is(err, GrokBundleInvalid()) && err.Error() != GrokBundleInvalid().Error() {
					t.Fatal("rejection was not the stable bundle error")
				}
				continue
			}
			native, err := bundle.AuthFile()
			if err != nil {
				t.Fatalf("accepted input (unit %q, size %d) cannot be re-encoded", unit, size)
			}
			again, err := ParseGrokAuthFile(native)
			if err != nil || !again.Equal(bundle) {
				t.Fatalf("re-encoded bundle (unit %q, size %d) is not accepted again", unit, size)
			}
			sealed, err := SealGrokBundle(server, account, generation, bundle)
			if err != nil {
				t.Fatalf("accepted input (unit %q, size %d) cannot be sealed", unit, size)
			}
			if _, err := OpenGrokBundle(sealed, server, account, generation); err != nil {
				t.Fatalf("sealed bundle (unit %q, size %d) cannot be opened", unit, size)
			}
		}
	}
}

// U+2028 and U+2029 are always escaped by the encoder, so input made of them can
// pass the input bound yet exceed it once re-encoded. It is rejected up front.
func TestParseGrokAuthFileRejectsInputThatCannotBeReencoded(t *testing.T) {
	field := strings.Repeat("\u2028", maxGrokSecretField/3)
	raw := grokRawFile(t, func(e map[string]any) { e["key"], e["refresh_token"] = field, field })
	if len(raw) > MaxGrokAuthFile {
		t.Fatalf("fixture is not within the input bound: %d", len(raw))
	}
	if _, err := ParseGrokAuthFile(raw); err == nil {
		t.Fatal("a file whose canonical encoding exceeds the bound was accepted")
	}
}

// The envelope's embedded document is parsed in place and cleared afterwards, so
// opening must neither alias nor modify the caller's sealed bytes, and the
// returned bundle must stay usable after the working copy is cleared.
func TestOpenGrokBundleLeavesInputIntactAndBundleUsable(t *testing.T) {
	bundle, err := ParseGrokAuthFile(grokFixtureFile(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	server, account, generation := grokBundleIDs()
	sealed, err := SealGrokBundle(server, account, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(sealed)
	opened, err := OpenGrokBundle(sealed, server, account, generation)
	if err != nil {
		t.Fatal("sealed bundle did not open")
	}
	if !bytes.Equal(sealed, before) {
		t.Fatal("opening modified the caller's sealed bytes")
	}
	if !opened.Equal(bundle) || opened.entry.Key != grokTestKey || opened.entry.RefreshToken != grokTestRefresh {
		t.Fatal("the opened bundle lost credential material")
	}
	native, err := opened.AuthFile()
	if err != nil || !bytes.Contains(native, []byte(grokTestRefresh)) {
		t.Fatal("the opened bundle cannot be materialized")
	}
	// A rejected envelope still fails with the stable error and no mutation.
	rebound := bytes.Clone(sealed)
	if _, err := OpenGrokBundle(rebound, server, account, domain.NewID()); err == nil || err.Error() != GrokBundleInvalid().Error() {
		t.Fatal("a rebound envelope was not rejected with the safe error")
	}
	if !bytes.Equal(rebound, sealed) {
		t.Fatal("rejecting an envelope modified the caller's bytes")
	}
}
