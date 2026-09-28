package domain

import (
	"net/url"
	"strings"
	"testing"
)

func TestGitHubFormsHaveOnlyVerifiedPrefills(t *testing.T) {
	for _, c := range []struct {
		kind   PATKind
		owner  string
		access GitHubTokenAccess
		count  int
	}{
		{FineGrainedPAT, "fixture-owner", GitHubSelectedRepositories, 9},
		{ClassicPAT, "", GitHubPublicRepositories, 1},
		{ClassicPAT, "fixture-owner", GitHubPrivateRepositories, 2},
	} {
		raw, err := GitHubTokenFormURL(c.kind, c.owner, c.access)
		if err != nil || ValidateGitHubPresentationURL(raw) != nil {
			t.Fatal(raw, err)
		}
		u, _ := url.Parse(raw)
		q := u.Query()
		if len(q) != c.count || q.Has("checks") || q.Has("repository_ids") {
			t.Fatal("unverified prefill", q)
		}
		if c.kind == FineGrainedPAT {
			for _, k := range []string{"metadata", "contents", "issues", "pull_requests", "statuses"} {
				if q.Get(k) != "read" {
					t.Fatal(k)
				}
			}
			if q.Get("target_name") != c.owner || q.Get("expires_in") != "30" {
				t.Fatal(q)
			}
		}
		if c.access == GitHubPrivateRepositories && q.Get("scopes") != "repo" {
			t.Fatal(q)
		}
		if c.access == GitHubPublicRepositories && q.Has("scopes") {
			t.Fatal(q)
		}
	}
	for _, c := range []struct {
		kind   PATKind
		owner  string
		access GitHubTokenAccess
	}{{FineGrainedPAT, "", GitHubSelectedRepositories}, {FineGrainedPAT, "a", GitHubPrivateRepositories}, {ClassicPAT, "", GitHubSelectedRepositories}, {ClassicPAT, "bad/owner", GitHubPublicRepositories}, {"unknown", "", GitHubPublicRepositories}} {
		if _, err := GitHubTokenFormURL(c.kind, c.owner, c.access); err == nil {
			t.Fatal("invalid selection accepted", c)
		}
	}
}

func TestGitHubPresentationRejectsAuthorityAndNormalizationBypasses(t *testing.T) {
	form, _ := GitHubTokenFormURL(FineGrainedPAT, "fixture-owner", GitHubSelectedRepositories)
	valid := []string{form, "https://github.com/owner/repo/pull/9007199254740993", "https://github.com/owner/repo/issues/1"}
	for _, raw := range valid {
		if err := ValidateGitHubPresentationURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	bad := []string{
		"file:///tmp/a", "https://evil.example/owner/repo/pull/1", "https://github.com.evil.example/owner/repo/pull/1", "https://user@github.com/owner/repo/pull/1", "https://github.com:443/owner/repo/pull/1", "http://github.com/owner/repo/pull/1",
		"https://github.com/owner/repo/pull/01", "https://github.com/owner/repo/pull/18446744073709551616", "https://github.com/owner/repo/pull/1#", "https://github.com/owner/repo/pull/1?", "https://github.com/owner/repo/pull/1?token=secret", "https://github.com/owner/repo/pull/%31", "https://github.com/owner/repo/../pull/1", "https://github.com/owner/repo\\evil/pull/1",
		form + "&checks=read", form + "&contents=write", form + "#", strings.Replace(form, "contents=read", "contents=write", 1), strings.Replace(form, "target_name=fixture-owner", "target_name=fixture-owner%26scopes%3Drepo", 1),
	}
	for _, raw := range bad {
		if ValidateGitHubPresentationURL(raw) == nil {
			t.Fatal("unsafe address accepted", raw)
		}
	}
}
