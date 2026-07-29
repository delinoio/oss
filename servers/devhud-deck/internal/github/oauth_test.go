package github

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestRefreshRotatesExpiringUserCredential(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "refresh_token" ||
			form.Get("refresh_token") != "ghr_old" {
			t.Fatalf("refresh form = %q", string(body))
		}
		return jsonResponse(http.StatusOK, `{
			"access_token":"ghu_new",
			"expires_in":28800,
			"refresh_token":"ghr_new",
			"refresh_token_expires_in":15897600,
			"token_type":"bearer"
		}`), nil
	})
	oauth, err := NewOAuth(OAuthConfig{
		ClientID: "fixture-client", ClientSecret: "fixture-secret",
		AppSlug:     "deck-fixture",
		CallbackURL: "https://deck.deli.dev/github/oauth/callback",
	}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	oauth.now = func() time.Time { return now }
	refreshed, err := oauth.Refresh(context.Background(), Credential{
		UserID: 123, AccessToken: "ghu_old", RefreshToken: "ghr_old",
		ExpiresAt:             now.Add(-time.Second),
		RefreshTokenExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.UserID != 123 || refreshed.AccessToken != "ghu_new" ||
		refreshed.RefreshToken != "ghr_new" ||
		!refreshed.ExpiresAt.Equal(now.Add(8*time.Hour)) {
		t.Fatalf("refreshed credential = %#v", refreshed)
	}
}
