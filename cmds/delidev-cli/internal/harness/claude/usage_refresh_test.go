// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func usageRefreshBundle() subscription.ClaudeUsageBundle {
	return subscription.ClaudeUsageBundle{AccessToken: []byte(usageFixtureAccess), RefreshToken: []byte(usageFixtureRefresh), ExpiresAt: usageFixtureNow.Add(time.Hour), Scopes: []string{"user:inference", "user:profile"}, ClientID: UsageClientID}
}

func TestUsageRefreshEnvironmentCarriesOnlyTheChildOnlyOAuthVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no verified no-op browser opener")
	}
	config, _ := fixtureConfig(t, "usage-refresh-env")
	config.Process.Env = append(config.Process.Env, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=foreign", "CLAUDE_CODE_OAUTH_SCOPES=foreign", "CLAUDE_CODE_OAUTH_CLIENT_ID=foreign", "CLAUDE_CODE_OAUTH_TOKEN=foreign")
	auth := AuthConfig{Process: config.Process, Version: config.Version, Home: config.Home}
	env, err := UsageRefreshEnvironment(auth, usageRefreshBundle())
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, dup := values[key]; dup {
			t.Fatalf("duplicate %s", key)
		}
		values[key] = value
	}
	if values["CLAUDE_CODE_OAUTH_REFRESH_TOKEN"] != usageFixtureRefresh || values["CLAUDE_CODE_OAUTH_SCOPES"] != "user:inference user:profile" || values["CLAUDE_CODE_OAUTH_CLIENT_ID"] != UsageClientID {
		t.Fatalf("child-only variables = %q %q %q", values["CLAUDE_CODE_OAUTH_REFRESH_TOKEN"], values["CLAUDE_CODE_OAUTH_SCOPES"], values["CLAUDE_CODE_OAUTH_CLIENT_ID"])
	}
	if values["CLAUDE_CONFIG_DIR"] != config.Home || values["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != config.Home {
		t.Fatal("the refresh did not run in the exact usage profile")
	}
	if _, present := values["CLAUDE_CODE_OAUTH_TOKEN"]; present {
		t.Fatal("a foreign token reached the refresh child")
	}
	for key, value := range values {
		if strings.Contains(value, usageFixtureAccess) {
			t.Fatalf("the access token reached %s", key)
		}
	}
	// The client binding is optional.
	bundle := usageRefreshBundle()
	bundle.ClientID = ""
	env, err = UsageRefreshEnvironment(auth, bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "CLAUDE_CODE_OAUTH_CLIENT_ID=") {
			t.Fatal("an absent client binding was invented")
		}
	}
	// Without a refresh token the renewal is unsupported and nothing is built.
	bundle.RefreshToken = nil
	if _, err := UsageRefreshEnvironment(auth, bundle); err == nil {
		t.Fatal("an environment was built without a refresh token")
	}
}

func usageRefreshScript(t *testing.T, body string) AuthConfig {
	t.Helper()
	config, _ := fixtureConfig(t, "usage-refresh-run")
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config.Process.Executable = script
	return AuthConfig{Process: config.Process, Version: config.Version, Home: config.Home}
}

func TestRunUsageRefreshClassifiesTheJoinedChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture child is a shell script")
	}
	marker := filepath.Join(t.TempDir(), "env-seen")
	cases := []struct {
		name string
		body string
		want UsageRefreshStatus
	}{
		{"success", `[ "$CLAUDE_CODE_OAUTH_SCOPES" = "user:inference user:profile" ] && [ "$1 $2 $3" = "auth login --claudeai" ] && : > ` + marker + `; exit 0`, UsageRefreshCompleted},
		{"rejected exit", `exit 1`, UsageRefreshRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			auth := usageRefreshScript(t, c.body)
			status, err := RunUsageRefresh(context.Background(), auth, usageRefreshBundle())
			if status != c.want {
				t.Fatalf("status = %q (%v), want %q", status, err, c.want)
			}
			if (status == UsageRefreshCompleted) != (err == nil) {
				t.Fatalf("error = %v for %q", err, status)
			}
		})
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the child never saw its environment and arguments")
	}
}

func TestRunUsageRefreshTimeoutAndCancellationAreUncertain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture child is a shell script")
	}
	auth := usageRefreshScript(t, `sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	status, err := RunUsageRefresh(ctx, auth, usageRefreshBundle())
	if status != UsageRefreshUncertain || err == nil {
		t.Fatalf("an interrupted child = %q %v, want uncertain", status, err)
	}
}

func TestRunUsageRefreshWithoutRefreshTokenStartsNothing(t *testing.T) {
	auth := usageRefreshScript(t, `exit 0`)
	bundle := usageRefreshBundle()
	bundle.RefreshToken = nil
	status, err := RunUsageRefresh(context.Background(), auth, bundle)
	if status != UsageRefreshNotStarted || err == nil {
		t.Fatalf("status = %q %v", status, err)
	}
}

// refreshChildFake fails the start barrier like a broken private channel.
type refreshChildFake struct {
	resumeErr error
	closeErr  error
	closed    int
}

func (f *refreshChildFake) Resume() error         { return f.resumeErr }
func (f *refreshChildFake) CloseInput() error     { return nil }
func (f *refreshChildFake) Done() <-chan struct{} { return make(chan struct{}) }
func (f *refreshChildFake) Wait() error           { return nil }
func (f *refreshChildFake) Close() error          { f.closed++; return f.closeErr }

func TestRunUsageRefreshResumeFailureIsUncertainBecauseTheTokenMayHaveBeenSent(t *testing.T) {
	for name, closeErr := range map[string]error{"stopped": nil, "stop unconfirmed": errors.New("cleanup unconfirmed")} {
		t.Run(name, func(t *testing.T) {
			auth := usageRefreshScript(t, `exit 0`)
			child := &refreshChildFake{resumeErr: errors.New("write unix: broken pipe"), closeErr: closeErr}
			previous := startUsageRefreshChild
			startUsageRefreshChild = func(context.Context, process.Config) (usageRefreshChild, error) { return child, nil }
			defer func() { startUsageRefreshChild = previous }()
			status, err := RunUsageRefresh(context.Background(), auth, usageRefreshBundle())
			if status != UsageRefreshUncertain || err == nil {
				t.Fatalf("a failed start barrier = %q %v, want uncertain", status, err)
			}
			if child.closed != 1 {
				t.Fatalf("the child was closed %d times, want once", child.closed)
			}
		})
	}
}

func TestRunUsageRefreshStartFailureStaysNotStarted(t *testing.T) {
	auth := usageRefreshScript(t, `exit 0`)
	previous := startUsageRefreshChild
	startUsageRefreshChild = func(context.Context, process.Config) (usageRefreshChild, error) {
		return nil, errors.New("spawn failed")
	}
	defer func() { startUsageRefreshChild = previous }()
	status, err := RunUsageRefresh(context.Background(), auth, usageRefreshBundle())
	if status != UsageRefreshNotStarted || err == nil {
		t.Fatalf("a failed spawn = %q %v, want not started", status, err)
	}
}
