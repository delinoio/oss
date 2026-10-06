// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"net/url"
	"testing"
	"time"
)

func TestGeminiProjectIsImmutableAndConnectionScoped(t *testing.T) {
	f := newOAuthFixture(t)
	f.s.oauthRegistrations = map[domain.ProviderPresetID]providers.OAuthRegistration{domain.PresetGemini: {ClientID: "delidev-fixture.apps.googleusercontent.com", RedirectURI: "http://127.0.0.1/oauth/google-gemini/callback", Registration: providers.OAuthRegistered, Compatibility: providers.OAuthAPIAccepted}}
	inventory, e := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range inventory.Msg.Entries {
		if p.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_GEMINI {
			f.provider = p.Provider
		}
	}
	for _, project := range []string{"", "MyProject", "short", "project-id\n", "project-ending-"} {
		_, e = f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), CallbackUrl: "http://127.0.0.1:55451/oauth/google-gemini/callback", Google: &pb.AccountOAuthGoogleOptions{QuotaProjectId: project}}))
		wantAccountCode(t, e, domain.InvalidArgument)
	}
	start := &pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), CallbackUrl: "http://127.0.0.1:55451/oauth/google-gemini/callback", Google: &pb.AccountOAuthGoogleOptions{QuotaProjectId: "my-ai-project"}}
	r, e := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(start))
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(r.Msg.AuthorizationUrl)
	if u.Host != "accounts.google.com" || u.Query().Get("access_type") != "offline" || u.Query().Get("prompt") != "consent" || u.Query().Get("scope") != "https://www.googleapis.com/auth/cloud-platform" {
		t.Fatal("wrong Google authorization")
	}
	start.Google.QuotaProjectId = "another-project"
	_, e = f.s.StartAccountOAuth(f.ctx, connect.NewRequest(start))
	wantAccountCode(t, e, domain.Conflict)
	f.s.oauthTokenClient = oauthTokenFixture{exchange: func(_ context.Context, p oauthProfile, _, _ []byte, callback string) (oauthTokenResult, error) {
		if p.preset != domain.PresetGemini || callback != "http://127.0.0.1:55451/oauth/google-gemini/callback" {
			t.Error("wrong Google profile")
		}
		return tokenResult("google-access-sentinel", "google-refresh-sentinel", time.Now().Add(time.Hour)), nil
	}}
	done, e := f.completeHF(r.Msg.Attempt, domain.NewID(), "google-code-sentinel", u.Query().Get("state"))
	if e != nil || done.Msg.Account == nil {
		t.Fatalf("completion: %v", e)
	}
	body := accountBody(t, done.Msg.Account)
	credential, e := f.s.resolveAPICredential(f.ctx, domain.ID(done.Msg.Account.Id), body.Connection.ID, body.ProviderID)
	defer clear(credential.key)
	if e != nil || credential.quotaProject != "my-ai-project" || string(credential.key) != "google-access-sentinel" {
		t.Fatal("quota credential changed")
	}
	e = f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
		v, found, e := tx.AccountOAuthCredential(domain.ID(done.Msg.Account.Id), body.Connection.ID)
		if !found || v.QuotaProject != "my-ai-project" {
			t.Error("project missing from private binding")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	assertOAuthPrivate(t, f, "google-access-sentinel", "google-refresh-sentinel", "google-code-sentinel", u.Query().Get("state"))
}

func TestOAuthInventoryAdvertisesOneSharedCapability(t *testing.T) {
	f := newHuggingFaceFixture(t)
	f.s.oauthRegistrations[domain.PresetGemini] = providers.OAuthRegistration{ClientID: "delidev-fixture.apps.googleusercontent.com", RedirectURI: "http://127.0.0.1/oauth/google-gemini/callback", Registration: providers.OAuthRegistered, Compatibility: providers.OAuthAPIAccepted}
	f.s.oauthRegistrations[domain.PresetHuggingFace] = providers.OAuthRegistration{ClientID: "delidev-fixture-public-client", RedirectURI: "http://localhost/oauth/hugging-face/callback", Registration: providers.OAuthRegistered, Compatibility: providers.OAuthAPIAccepted}
	r, e := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
	if e != nil {
		t.Fatal(e)
	}
	var count int
	for _, capability := range r.Msg.Capabilities {
		if capability == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_V1 {
			count++
		}
	}
	if count != 1 {
		t.Fatal("shared OAuth capability duplicated")
	}
}
