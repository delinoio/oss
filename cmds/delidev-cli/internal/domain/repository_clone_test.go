// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestRepositoryCloneURLs(t *testing.T) {
	for _, value := range []string{"https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git", "git@github.com:owner/repo.git", "github.com:owner/repo.git", "https://git.example.com:8443/team/repo", "ssh://user@host:2222/team/repo", "https://[::1]/repo"} {
		got, err := ParseRepositoryCloneURL(value)
		if err != nil || got.DirectoryName != "repo" {
			t.Errorf("accepted URL: %q, %+v, %v", value, got, err)
		}
	}
	for _, value := range []string{"", " /tmp/repo", "/tmp/repo", "../repo", "C:/repo", "file:///tmp/repo", "http://host/repo", "git://host/repo", "ext::helper", "https://token@host/repo", "https://user:password@host/repo", "ssh://user:password@host/repo", "https://host/repo?token=secret", "https://host/repo#token", "ssh://-option@host/repo", "-option@host:repo", "https://host/repo%00", "https://host/repo%0a", "https://host/repo\n", "https://host/../repo", "https://host/%2e%2e/repo", "https://host/team%2Frepo", "https://host/team%2frepo", "https://host/repo%5cother", "https://host:0/repo", "https://host:65536/repo", "ssh://host", "host:", "git@host:repo:another"} {
		if _, err := ParseRepositoryCloneURL(value); err == nil {
			t.Errorf("accepted unsafe URL: %q", value)
		}
	}
}

func TestRepositoryCloneIdentityAcrossTransport(t *testing.T) {
	for _, value := range []string{"https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git", "git@github.com:owner/repo.git"} {
		got, err := ParseRepositoryCloneURL(value)
		if err != nil || got.GitHubOwner != "owner" || got.GitHubName != "repo" {
			t.Fatal(got, err)
		}
	}
	for _, value := range []string{"https://enterprise.example.com/owner/repo.git", "https://github.com:8443/owner/repo.git", "https://github.com/owner/repo/other"} {
		got, err := ParseRepositoryCloneURL(value)
		if err != nil || got.GitHubOwner != "" {
			t.Fatal(got, err)
		}
	}
}

func TestRepositoryCloneSourceIdentity(t *testing.T) {
	var identity string
	for _, value := range []string{"https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git", "git@github.com:owner/repo.git"} {
		got, err := RepositoryCloneSourceIdentity(value)
		if err != nil {
			t.Fatal(value, err)
		}
		if identity == "" {
			identity = got
		} else if got != identity {
			t.Fatalf("transport changed source identity: %q != %q", got, identity)
		}
		if !ValidRepositoryCloneSourceIdentity(got) {
			t.Fatalf("invalid generated source identity: %q", got)
		}
	}
	for _, value := range []string{"https://github.com/owner/other.git", "https://example.com/owner/repo.git", "https://github.com/owner/repo/other.git"} {
		got, err := RepositoryCloneSourceIdentity(value)
		if err != nil || got == identity {
			t.Fatalf("source identity collision: %q, %q, %v", value, got, err)
		}
	}
	for _, values := range [][2]string{
		{"alice@git.example.com:team/repo.git", "bob@git.example.com:team/repo.git"},
		{"git.example.com:team/repo.git", "git.example.com:/team/repo.git"},
		{"https://git.example.com/team/repo.git", "ssh://git@git.example.com/team/repo.git"},
	} {
		left, err := RepositoryCloneSourceIdentity(values[0])
		if err != nil {
			t.Fatal(values[0], err)
		}
		right, err := RepositoryCloneSourceIdentity(values[1])
		if err != nil {
			t.Fatal(values[1], err)
		}
		if left == right {
			t.Fatalf("security-significant source identity collision: %q and %q", values[0], values[1])
		}
	}
	for _, values := range [][2]string{
		{"ssh://git@git.example.com/team/repo.git", "git@git.example.com:/team/repo.git"},
		{"https://github.com/owner/repo.git", "git@github.com:Owner/Repo.git"},
	} {
		left, err := RepositoryCloneSourceIdentity(values[0])
		if err != nil {
			t.Fatal(values[0], err)
		}
		right, err := RepositoryCloneSourceIdentity(values[1])
		if err != nil {
			t.Fatal(values[1], err)
		}
		if left != right {
			t.Fatalf("equivalent SSH source identity changed: %q and %q", values[0], values[1])
		}
	}
	for _, value := range []string{"", "0", "not-a-digest", "000000000000000000000000000000000000000000000000000000000000000g"} {
		if ValidRepositoryCloneSourceIdentity(value) {
			t.Fatalf("accepted invalid source identity: %q", value)
		}
	}
}

func TestRepositoryCloneDirectory(t *testing.T) {
	for _, value := range []string{"repo", "My repository", "한글", "repo.git", "-folder"} {
		if err := ValidateRepositoryCloneDirectory(value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"", ".", "..", "../other", "a/b", "a\\b", "CON", "nul.txt", "Lpt1", "COM9.txt", "trailing.", "trailing ", "a:b", "a\n", "a?b"} {
		if ValidateRepositoryCloneDirectory(value) == nil {
			t.Fatal(value)
		}
	}
}
