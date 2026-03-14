package worktree

import (
	"testing"
)

func TestRepoCacheKey(t *testing.T) {
	key1 := repoCacheKey("https://github.com/org/repo.git")
	key2 := repoCacheKey("https://github.com/org/repo.git")
	key3 := repoCacheKey("https://github.com/org/other.git")

	if key1 != key2 {
		t.Errorf("same URL should produce same key: %s != %s", key1, key2)
	}
	if key1 == key3 {
		t.Error("different URLs should produce different keys")
	}
	if len(key1) != 16 {
		t.Errorf("expected 16 hex chars, got %d: %s", len(key1), key1)
	}
}

func TestRepoNameFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/org/myrepo.git", "myrepo"},
		{"https://github.com/org/myrepo", "myrepo"},
		{"git@github.com:org/myrepo.git", "myrepo"},
		{"repo", "repo"},
	}

	for _, tt := range tests {
		got := repoNameFromURL(tt.url)
		if got != tt.want {
			t.Errorf("repoNameFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
