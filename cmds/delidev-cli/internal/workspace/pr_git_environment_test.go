// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"runtime"
	"slices"
	"testing"
)

func TestPRGitEnvironmentSnapshotPlatformNames(t *testing.T) {
	for _, test := range []struct {
		name            string
		caseInsensitive bool
		input           []string
		want            []string
	}{
		{
			name:  "POSIX distinct names",
			input: []string{"GIT_SSH_COMMAND=original", "git_ssh_command=inert", "PATH=original-path", "path=inert-path"},
			want:  []string{"GIT_SSH_COMMAND=original", "PATH=original-path", "git_ssh_command=inert", "path=inert-path"},
		},
		{
			name:            "Windows last case-insensitive value",
			caseInsensitive: true,
			input:           []string{"GIT_SSH_COMMAND=earlier", "git_ssh_command=last", "PATH=earlier-path", "Path=last-path"},
			want:            []string{"GIT_SSH_COMMAND=last", "PATH=last-path"},
		},
		{
			name:  "POSIX last exact-name value",
			input: []string{"GIT_SSH_COMMAND=earlier", "git_ssh_command=inert", "GIT_SSH_COMMAND=last"},
			want:  []string{"GIT_SSH_COMMAND=last", "git_ssh_command=inert"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := snapshotPRGitEnvironment(test.input, test.caseInsensitive)
			if !slices.Equal(got, test.want) {
				t.Fatal("environment snapshot changed platform lookup semantics")
			}
			if !slices.Equal(snapshotPRGitEnvironment(got, test.caseInsensitive), got) {
				t.Fatal("environment snapshot is not stable when retained again")
			}
		})
	}
}

func TestOriginalPRGitEnvironmentPreservesPOSIXNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not retain distinct case variants in its native environment")
	}
	t.Setenv("GIT_SSH_COMMAND", "fixture-ssh")
	t.Setenv("git_ssh_command", "inert-fixture-ssh")
	t.Setenv("path", "inert-fixture-path")
	preflight := gitEnvironment()
	bridge := originalPRGitEnvironment()
	for _, entry := range []string{"GIT_SSH_COMMAND=fixture-ssh", "git_ssh_command=inert-fixture-ssh", "path=inert-fixture-path"} {
		if !slices.Contains(preflight, entry) || !slices.Contains(bridge, entry) {
			t.Fatal("bridge changed a case-sensitive preflight environment entry")
		}
	}
	if slices.Contains(bridge, "PATH=inert-fixture-path") || slices.Contains(bridge, "GIT_SSH_COMMAND=inert-fixture-ssh") {
		t.Fatal("an inert POSIX entry became an active Git lookup setting")
	}
}

func TestAppendGitConfigPreservesExistingEntries(t *testing.T) {
	environment := []string{"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=fixture", "GIT_CONFIG_KEY_1=core.sshCommand", "GIT_CONFIG_VALUE_1=ssh fixture"}
	got := appendGitConfig(environment, "core.longpaths", "true")
	want := []string{"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=fixture", "GIT_CONFIG_KEY_1=core.sshCommand", "GIT_CONFIG_VALUE_1=ssh fixture", "GIT_CONFIG_KEY_2=core.longpaths", "GIT_CONFIG_VALUE_2=true"}
	if !slices.Equal(got, want) {
		t.Fatalf("existing Git config entries were replaced: %v", got)
	}
}
