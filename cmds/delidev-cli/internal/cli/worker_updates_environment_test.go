// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWorkerHandoffPreservesGhSelectorWithoutTokens(t *testing.T) {
	if strings.Contains(strings.Join(updateEnvironment(), "\n"), "GH_TOKEN=") {
		t.Fatal("ambient token inherited")
	}
	directory := t.TempDir()
	t.Setenv("GH_CONFIG_DIR", directory)
	t.Setenv("GH_TOKEN", "synthetic-denied-token")
	t.Setenv("GITHUB_TOKEN", "synthetic-denied-token")
	t.Setenv("DELIDEV_TEST_FOREIGN_SECRET", "synthetic-denied-secret")
	// Both signed-controller selection and update/rollback use this exact env.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestWorkerGhHandoffChild$")
	cmd.Env = append(updateEnvironment(), "DELIDEV_TEST_GH_CHILD=1")
	joined := strings.Join(cmd.Env, "\n")
	if !strings.Contains(joined, "GH_CONFIG_DIR="+directory) || strings.Contains(joined, "synthetic-denied") {
		t.Fatal("handoff lost selector or inherited credentials")
	}
	output, err := cmd.Output()
	if err != nil || string(output) != directory {
		t.Fatal("child received a different gh identity or credentials", err)
	}
}

func TestWorkerGhHandoffChild(t *testing.T) {
	if os.Getenv("DELIDEV_TEST_GH_CHILD") != "1" {
		return
	}
	if os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("DELIDEV_TEST_FOREIGN_SECRET") != "" {
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString(os.Getenv("GH_CONFIG_DIR"))
	os.Exit(0)
}
