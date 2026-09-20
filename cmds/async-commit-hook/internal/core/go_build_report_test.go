package core

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoReportAcceptsActualToolchainBuildFailures(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":           "module example.test/buildfixture\n\ngo 1.25\n",
		"fixture.go":       "package fixture\nimport _ \"example.test/buildfixture/broken\"\n",
		"fixture_test.go":  "package fixture\nimport \"testing\"\nfunc TestExample(t *testing.T) {}\n",
		"broken/broken.go": "package broken\nvar Value = MissingBuildSymbol\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-json", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	report, err := cmd.Output()
	if err == nil {
		t.Fatal("fixture unexpectedly compiled")
	}
	if !bytes.Contains(report, []byte(`"Action":"build-fail"`)) {
		t.Fatalf("toolchain did not produce build JSON: %s\n%s", report, stderr.String())
	}
	failures, err := ParseReport(GoTest, report, "check", "go test -json ./...", "log")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, failure := range failures {
		if failure.Test == "example.test/buildfixture/broken" && strings.Contains(failure.Message, "MissingBuildSymbol") {
			found = true
			if failure.LogID != "log" || failure.Command != "go test -json ./..." {
				t.Fatal("build failure lost evidence references")
			}
		}
	}
	if !found {
		t.Fatalf("compiler diagnostics were lost: %#v", failures)
	}
	changed, err := ParseReport(GoTest, bytes.ReplaceAll(report, []byte("MissingBuildSymbol"), []byte("ChangedBuildSymbol")), "check", "go test -json ./...", "log")
	if err != nil || len(changed) != len(failures) {
		t.Fatal("diagnostic change altered failure count", err)
	}
	for i := range failures {
		if failures[i].ID != changed[i].ID {
			t.Fatal("diagnostics changed comparable failure identity")
		}
	}
}

func TestGoBuildEventsRemainSeparateFromTestEvents(t *testing.T) {
	var report bytes.Buffer
	emit := func(event map[string]string) {
		t.Helper()
		if err := json.NewEncoder(&report).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	emit(map[string]string{"Action": "start", "Package": "same"})
	emit(map[string]string{"Action": "build-output", "ImportPath": "same", "Output": "compiler one"})
	emit(map[string]string{"Action": "build-output", "ImportPath": "other [test]", "Output": "compiler two"})
	emit(map[string]string{"Action": "output", "Package": "same", "Output": "test output"})
	emit(map[string]string{"Action": "build-fail", "ImportPath": "other [test]"})
	emit(map[string]string{"Action": "build-fail", "ImportPath": "same"})
	emit(map[string]string{"Action": "build-output", "ImportPath": "same", "Output": "repeat"})
	emit(map[string]string{"Action": "build-fail", "ImportPath": "same"})
	emit(map[string]string{"Action": "fail", "Package": "same", "FailedBuild": "same"})
	failures, err := ParseReport(GoTest, report.Bytes(), "check", "command", "log")
	if err != nil || len(failures) != 4 {
		t.Fatal("interleaved failures lost", failures, err)
	}
	ids := map[string]bool{}
	for i, message := range []string{"compiler two", "compiler one", "repeat", "test output"} {
		if failures[i].Message != message || ids[failures[i].ID] {
			t.Fatal("build/test output or identity collided", failures)
		}
		ids[failures[i].ID] = true
	}
	for _, report := range []string{
		"{\"Action\":\"build-fail\"}\n",
		"{\"Action\":\"build-output\",\"Output\":\"bad\"}\n",
		"{\"Action\":\"build-unknown\",\"ImportPath\":\"pkg\"}\n",
	} {
		if _, err := ParseReport(GoTest, []byte(report), "check", "command", "log"); err == nil {
			t.Fatal("malformed build event accepted")
		}
	}
	warning := []byte("{\"Action\":\"build-output\",\"ImportPath\":\"pkg\",\"Output\":\"warning\"}\n{\"Action\":\"pass\",\"Package\":\"pkg\"}\n")
	if failures, err := ParseReport(GoTest, warning, "check", "command", "log"); err != nil || len(failures) != 0 {
		t.Fatal("warning failed a passing report", failures, err)
	}
}
