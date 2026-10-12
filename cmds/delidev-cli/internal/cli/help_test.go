// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelpDocumentsBranchComparisons(t *testing.T) {
	for _, entryPoint := range []string{"help", "--help"} {
		t.Run(entryPoint, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := Run(context.Background(), []string{entryPoint}, IO{
				In: strings.NewReader(""), Out: &out, Err: &stderr,
			})
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("help failed: code=%d stderr=%q", code, stderr.String())
			}
			lines := strings.Split(out.String(), "\n")
			for _, command := range []string{"session diff", "session review-context"} {
				var entry string
				for _, line := range lines {
					if strings.HasPrefix(strings.TrimSpace(line), command+" ") {
						if entry != "" {
							t.Fatalf("duplicate help entry for %s", command)
						}
						entry = line
					}
				}
				for _, syntax := range []string{
					"--id ID", "--repository-id ID", "[--path RELATIVE]",
					"--comparison branch|working-tree|staged|creation",
					"--base-type local-branch|remote-branch|commit",
					"--base-name NAME", "[--base-remote REMOTE]",
				} {
					if !strings.Contains(entry, syntax) {
						t.Errorf("%s help is missing %q: %q", command, syntax, entry)
					}
				}
			}
			for _, rule := range []string{
				"Both commands default to working-tree when --comparison is omitted.",
				"Base flags are valid only with --comparison branch; --base-type and --base-name are required.",
				"--base-remote is required for remote-branch; omit it for local-branch and commit.",
			} {
				if !strings.Contains(out.String(), rule) {
					t.Errorf("help is missing branch comparison guidance %q", rule)
				}
			}
		})
	}
}
