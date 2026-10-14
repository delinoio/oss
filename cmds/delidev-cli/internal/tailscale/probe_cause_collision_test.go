// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

var probeCauses = []ProbeCause{ProbeConnectionRefused, ProbeTimeout, ProbeCertificateInvalid, ProbeUnexpectedHTTP, ProbeInvalidIdentity}

// Check-only causes are namespaced on the wire so a generic emitter that uses a
// common word (for example the Codex handshake's unavailable + "timeout") can
// never be decoded as a Tailscale Check cause by clients.
func TestProbeCausesAreNamespaced(t *testing.T) {
	for _, cause := range probeCauses {
		if !strings.HasPrefix(string(cause), "tailscale_") {
			t.Errorf("%q is not namespaced", cause)
		}
	}
}

func TestGenericCausesDoNotProjectAsProbeCauses(t *testing.T) {
	for _, generic := range []string{"timeout", "connection_refused", "certificate_invalid", "unexpected_http", "invalid_identity"} {
		for _, code := range []domain.Code{domain.Unavailable, domain.Unsupported} {
			err := &domain.Error{Code: code, Cause: generic}
			if got := ProbeCauseOf(err); got != "" {
				t.Errorf("%s/%s projected as %q", code, generic, got)
			}
		}
	}
}

var causeLiteral = regexp.MustCompile(`\bCause\s*(?::|=)\s*"([^"]+)"`)

// No other emitter in the CLI may produce a Check-only cause value, and only the
// CheckTailscale handler may use the cause-aware probe, so other RPCs cannot
// reach these causes.
func TestProbeCausesAreUnreachableFromOtherEmitters(t *testing.T) {
	reserved := map[string]bool{}
	for _, cause := range probeCauses {
		reserved[string(cause)] = true
	}
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		inTailscale := filepath.Base(filepath.Dir(path)) == "tailscale"
		for _, match := range causeLiteral.FindAllStringSubmatch(string(raw), -1) {
			if reserved[match[1]] && !inTailscale {
				t.Errorf("%s emits reserved Check-only cause %q", path, match[1])
			}
		}
		if !inTailscale {
			text := string(raw)
			for _, use := range []string{"tailscale.CheckProbe(", "tailscale.InvalidIdentity("} {
				if strings.Contains(text, use) && filepath.Base(path) != "tailscale.go" {
					t.Errorf("%s uses Check-only %s outside the CheckTailscale handler", path, use)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
