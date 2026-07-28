package github

import (
	"net/url"
	"strings"
	"testing"
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
