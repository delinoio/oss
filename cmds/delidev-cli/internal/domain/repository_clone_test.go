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
	for _, value := range []string{"", " /tmp/repo", "/tmp/repo", "../repo", "C:/repo", "file:///tmp/repo", "http://host/repo", "git://host/repo", "ext::helper", "https://token@host/repo", "https://user:password@host/repo", "ssh://user:password@host/repo", "https://host/repo?token=secret", "https://host/repo#token", "ssh://-option@host/repo", "-option@host:repo", "https://host/repo%00", "https://host/repo%0a", "https://host/repo\n", "https://host/../repo", "https://host/%2e%2e/repo", "https://host/repo%5cother", "https://host:0/repo", "https://host:65536/repo", "ssh://host", "host:", "git@host:repo:another"} {
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
