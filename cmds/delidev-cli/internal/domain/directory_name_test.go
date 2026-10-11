// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

func TestPortableDirectoryName(t *testing.T) {
	for name, want := range map[string]string{
		"oss":                         "oss",
		"OSS":                         "OSS",
		"  oss  ":                     "oss",
		"a/b":                         "a_b",
		"a:b*c?":                      "a_b_c_",
		"CON":                         "_CON",
		"con.txt":                     "_con.txt",
		"LPT1":                        "_LPT1",
		"":                            "repository",
		"...":                         "repository",
		"name. . ":                    "name",
		"a\x00b":                      "a_b",
		"é":                          "é",
		strings.Repeat("a", 300):      strings.Repeat("a", 255),
		strings.Repeat("\u00e9", 200): strings.Repeat("\u00e9", 127),
	} {
		got := PortableDirectoryName(name)
		if got != want {
			t.Fatalf("PortableDirectoryName(%q) = %q, want %q", name, got, want)
		}
		if err := ValidateRepositoryCloneDirectory(got); err != nil {
			t.Fatalf("converted name %q is not portable: %v", got, err)
		}
		if PortableDirectoryName(got) != got {
			t.Fatalf("conversion of %q is not idempotent", got)
		}
	}
}
