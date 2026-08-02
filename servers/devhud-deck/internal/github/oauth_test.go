package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestExchangeBindsConfiguredCallbackURL(t *testing.T) {
	t.Parallel()
	const callbackURL = "https://deck.deli.dev/github/oauth/callback"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("code") != "fixture-code" ||
			form.Get("redirect_uri") != callbackURL {
			t.Fatalf("exchange form = %q", string(body))
		}
		return jsonResponse(http.StatusOK, `{
			"access_token":"ghu_new",
			"token_type":"bearer"
		}`), nil
	})
	oauth, err := NewOAuth(OAuthConfig{
		ClientID: "fixture-client", ClientSecret: "fixture-secret",
		AppSlug: "deck-fixture", CallbackURL: callbackURL,
	}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := oauth.Exchange(context.Background(), "fixture-code")
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "ghu_new" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestRefreshMapsRejectedRefreshTokenToReauthentication(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{
			"error":"bad_refresh_token",
			"error_description":"The refresh token is invalid."
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
	_, err = oauth.Refresh(context.Background(), Credential{
		UserID: 123, AccessToken: "ghu_old", RefreshToken: "ghr_rejected",
		ExpiresAt:             now.Add(-time.Second),
		RefreshTokenExpiresAt: now.Add(time.Hour),
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("rejected refresh error = %T %v", err, err)
	}
}

func TestRefreshKeepsClientConfigurationErrorsAsProviderFailures(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{
			"error":"incorrect_client_credentials"
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
	_, err = oauth.Refresh(context.Background(), Credential{
		UserID: 123, AccessToken: "ghu_old", RefreshToken: "ghr_valid",
		ExpiresAt:             now.Add(-time.Second),
		RefreshTokenExpiresAt: now.Add(time.Hour),
	})
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("client configuration error = %T %v", err, err)
	}
}

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

func TestOAuthDispatchObserverRunsAtTransportBoundary(t *testing.T) {
	t.Parallel()
	blocked := errors.New("dispatch accounting failed")
	roundTrips := 0
	oauth, err := NewOAuth(OAuthConfig{
		ClientID: "fixture-client", ClientSecret: "fixture-secret",
		AppSlug:     "deck-fixture",
		CallbackURL: "https://deck.deli.dev/github/oauth/callback",
	}, &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			roundTrips++
			return jsonResponse(http.StatusOK, `{
				"access_token":"ghu_new",
				"token_type":"bearer"
			}`), nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	oauth.now = func() time.Time { return now }
	credential := Credential{
		RefreshToken:          "ghr_valid",
		RefreshTokenExpiresAt: now.Add(time.Hour),
	}
	observerCalls := 0
	ctx := WithDispatchObserver(context.Background(), func() error {
		observerCalls++
		return blocked
	})
	if _, err := oauth.Refresh(ctx, credential); !errors.Is(err, blocked) ||
		observerCalls != 1 || roundTrips != 0 {
		t.Fatalf(
			"blocked refresh = %v, observer = %d, round trips = %d",
			err, observerCalls, roundTrips)
	}

	observerCalls = 0
	ctx, cancel := context.WithCancel(WithDispatchObserver(
		context.Background(), func() error {
			observerCalls++
			return nil
		}))
	cancel()
	if _, err := oauth.Refresh(ctx, credential); !errors.Is(err, ErrProvider) ||
		observerCalls != 0 || roundTrips != 0 {
		t.Fatalf(
			"cancelled refresh = %v, observer = %d, round trips = %d",
			err, observerCalls, roundTrips)
	}
}
