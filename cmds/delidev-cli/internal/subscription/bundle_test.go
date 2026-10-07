package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixtureBundle(account, user, rotation string, at time.Time) []byte {
	claims, _ := json.Marshal(map[string]any{"email": "fixture@example.invalid", "nonce": rotation, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account, "chatgpt_user_id": user, "chatgpt_plan_type": "plus"}})
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic-signature"
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": token, "access_token": token, "refresh_token": "synthetic-refresh-" + rotation, "account_id": account}, "last_refresh": at})
	return raw
}

func TestManagedBundleRefreshRequiresIndependentFileEvidence(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	before := fixtureBundle("account-fixture", "user-fixture", "first", at)
	if _, identity, err := Parse(before); err != nil || identity.Account != "account-fixture" {
		t.Fatal("valid controlled bundle rejected")
	}
	for name, after := range map[string][]byte{
		"unchanged":     before,
		"metadata-only": fixtureBundle("account-fixture", "user-fixture", "first", at.Add(time.Second)),
		"tokens-only":   fixtureBundle("account-fixture", "user-fixture", "second", at),
		"wrong-account": fixtureBundle("other-account", "user-fixture", "second", at.Add(time.Second)),
		"wrong-user":    fixtureBundle("account-fixture", "other-user", "second", at.Add(time.Second)),
	} {
		t.Run(name, func(t *testing.T) {
			if Refreshed(before, after) == nil {
				t.Fatal("incomplete refresh was accepted")
			}
		})
	}
	if Refreshed(before, fixtureBundle("account-fixture", "user-fixture", "second", at.Add(time.Second))) != nil {
		t.Fatal("complete native rotation rejected")
	}
}

func TestRejectExternalOrMalformedBundles(t *testing.T) {
	raw := fixtureBundle("fixture", "user", "first", time.Now().Add(-time.Minute))
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	for _, name := range []string{"apikey", "external-mode", "unknown-secret", "foreign-token", "missing-refresh", "oversized"} {
		t.Run(name, func(t *testing.T) {
			var v map[string]any
			_ = json.Unmarshal(raw, &v)
			switch name {
			case "apikey":
				v["OPENAI_API_KEY"] = "synthetic-api-key"
			case "external-mode":
				v["auth_mode"] = "chatgptAuthTokens"
			case "unknown-secret":
				v["personal_access_token"] = "synthetic-pat"
			case "foreign-token":
				v["tokens"].(map[string]any)["account_id"] = "foreign"
			case "missing-refresh":
				v["tokens"].(map[string]any)["refresh_token"] = ""
			case "oversized":
				v["padding"] = strings.Repeat("x", MaxBundle)
			}
			changed, _ := json.Marshal(v)
			if _, _, err := Parse(changed); err == nil {
				t.Fatal("unsafe bundle accepted")
			}
		})
	}
}
