package presentation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitHubLauncherChild(t *testing.T) {
	if len(os.Args) < 5 || os.Args[len(os.Args)-3] != "--github-launch-fixture" {
		return
	}
	if os.Args[len(os.Args)-2] == "hang" {
		time.Sleep(time.Minute)
		os.Exit(1)
	}
	bytes, _ := json.Marshal(map[string]any{"url": os.Args[len(os.Args)-2], "env": os.Environ()})
	if os.WriteFile(os.Args[len(os.Args)-1], bytes, 0600) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestGitHubOpeningUsesOneArgumentAndExcludesCredentials(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-github-secret")
	t.Setenv("ANTHROPIC_API_KEY", "fixture-provider-secret")
	t.Setenv("BROWSER", "untrusted-browser-command")
	executable, _ := os.Executable()
	output := filepath.Join(t.TempDir(), "launch.json")
	raw := "https://github.com/owner/repo/pull/1"
	err := openGitHub(context.Background(), raw, func(address string) (string, []string, error) {
		return executable, []string{"-test.run=^TestGitHubLauncherChild$", "--", "--github-launch-fixture", address, output}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), raw) || strings.Contains(string(result), "fixture-github-secret") || strings.Contains(string(result), "fixture-provider-secret") || strings.Contains(string(result), "untrusted-browser-command") {
		t.Fatal("invalid launch projection")
	}
	called := false
	if openGitHub(context.Background(), "https://github.com/owner/repo/pull/1?secret=x", func(string) (string, []string, error) { called = true; return "", nil, nil }) == nil || called {
		t.Fatal("unsafe URL reached launcher")
	}
}

func TestGitHubOpeningTimeoutDoesNotAcknowledgeDispatch(t *testing.T) {
	executable, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := openGitHub(ctx, "https://github.com/owner/repo/issues/1", func(string) (string, []string, error) {
		return executable, []string{"-test.run=^TestGitHubLauncherChild$", "--", "--github-launch-fixture", "hang", filepath.Join(t.TempDir(), "unused")}, nil
	})
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatal("unbounded or acknowledged timeout", err)
	}
}
