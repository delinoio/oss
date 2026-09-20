//go:build !windows

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReportsMustBeProducedAfterTheirOwningCheckStarts(t *testing.T) {
	const report = "<testsuite tests=\"1\"><testcase name=\"passed\"/></testsuite>"
	for _, prerequisite := range []bool{false, true} {
		for _, fresh := range []bool{false, true} {
			t.Run(fmt.Sprintf("prerequisite=%v/fresh=%v", prerequisite, fresh), func(t *testing.T) {
				command := "true"
				if fresh {
					command = fmt.Sprintf("printf '%%s' '%s' > result.xml", report)
				}
				config := fmt.Sprintf("version=1\n[checks.test]\ncommand=%q\n", command)
				if prerequisite {
					config += "depends_on=[\"prepare\"]\n"
				}
				config += "[[checks.test.reports]]\nkind=\"junit\"\npath=\"result.xml\"\n"
				if prerequisite {
					config += fmt.Sprintf("[checks.prepare]\ncommand=%q\n", "printf '%s' '"+report+"' > result.xml")
				}
				s, repo := fixture(t, config)
				if err := os.WriteFile(filepath.Join(repo, "result.xml"), []byte(report), 0600); err != nil {
					t.Fatal(err)
				}
				for _, args := range [][]string{{"add", "result.xml"}, {"-c", "commit.gpgsign=false", "commit", "-m", "stale report"}} {
					if _, err := Git(context.Background(), repo, args...); err != nil {
						t.Fatal(err)
					}
				}
				r := runFixture(t, s, repo)
				if s.GateRun(r).Passed != fresh {
					t.Fatalf("stale report accepted or fresh report rejected: %+v", r)
				}
			})
		}
	}
}

func TestReportPathsCannotHaveCompetingOwners(t *testing.T) {
	for _, path := range []string{"result.xml", "RESULT.xml"} {
		_, err := ParseProject([]byte(fmt.Sprintf("version=1\n[checks.a]\ncommand=\"true\"\n[[checks.a.reports]]\nkind=\"junit\"\npath=\"result.xml\"\n[checks.b]\ncommand=\"true\"\n[[checks.b.reports]]\nkind=\"junit\"\npath=%q\n", path)))
		if err == nil {
			t.Fatal("accepted competing report owners")
		}
	}
}

func TestReportPreparationCannotDeleteOutsideWorkspace(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "report.xml")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := clearReportOutputs(workspace, []Report{{Path: "escape/report.xml"}}); err == nil {
		t.Fatal("accepted escaping report")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "private" {
		t.Fatal("outside file changed")
	}
}
