package github

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuthorizationTargetIsPinnedToGitHub(t *testing.T) {
	t.Parallel()
	authorization, err := NewAuthorization("fixture-realqa-client")
	if err != nil {
		t.Fatal(err)
	}
	state := strings.Repeat("a", 43)
	target, err := authorization.Target(state)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.Path != "/login/oauth/authorize" ||
		parsed.Query().Get("client_id") != "fixture-realqa-client" ||
		parsed.Query().Get("state") != state {
		t.Fatalf("unexpected target %q", target)
	}
}

func TestConnectionTargetIsPinnedToConfiguredGitHubApp(t *testing.T) {
	t.Parallel()
	state, err := NewStateCodec([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	authorization, err := NewAppAuthorization(
		"fixture-realqa-client", "fixture-realqa", state,
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	target, signedState, err := authorization.ConnectionTarget(
		string(OwnerKindPersonal),
		uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608"),
		uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51609"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.Path != "/apps/fixture-realqa/installations/new" ||
		parsed.Query().Get("state") != signedState {
		t.Fatalf("unexpected installation target %q", target)
	}
	if _, accountID, _, err := state.Verify(
		signedState, CallbackPurposeApp, now); err != nil {
		t.Fatal(err)
	} else if accountID !=
		uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51609") {
		t.Fatalf("callback account = %s", accountID)
	}
}
