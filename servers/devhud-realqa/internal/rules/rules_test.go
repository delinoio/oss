package rules

import (
	"strings"
	"testing"
)

func TestResolveUsesExactProcessAndFirstMatchingRule(t *testing.T) {
	t.Parallel()
	set, err := Compile([]Rule{
		{
			ExactProcessName: "chrome",
			TitlePattern:     `^Issue ([0-9]+)$`,
			URLTemplate:      "https://github.com/delinoio/oss/issues/$1",
			Enabled:          true,
		},
		{
			ExactProcessName: "chrome",
			URLTemplate:      "https://fallback.example/",
			Enabled:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := set.Resolve("Chrome", "Issue 757"); ok || value != "" {
		t.Fatalf("case-insensitive process unexpectedly matched %q", value)
	}
	value, ok := set.Resolve("chrome", "Issue 757")
	if !ok || value != "https://github.com/delinoio/oss/issues/757" {
		t.Fatalf("Resolve() = %q, %v", value, ok)
	}
}

func TestResolveSkipsInvalidExpansionAndUsesFallback(t *testing.T) {
	t.Parallel()
	set, err := Compile([]Rule{
		{
			ExactProcessName: "chrome",
			TitlePattern:     `^(.+)$`,
			URLTemplate:      "https://$1.example.com/",
			Enabled:          true,
		},
		{
			ExactProcessName: "chrome",
			URLTemplate:      "https://fallback.example/",
			Enabled:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := set.Resolve("chrome", "user@example")
	if !ok || value != "https://fallback.example/" {
		t.Fatalf("Resolve() = %q, %v", value, ok)
	}
}

func TestCompileRejectsUnsafeOrDivergentRegexFeatures(t *testing.T) {
	t.Parallel()
	patterns := []string{
		`(?=secret)`, `(a)\1`, `(?P<name>a)`, `\C`, `\Qliteral\E`,
		`[a-z&&[^x]]`, `[a-z--[aeiou]]`, `a{101}`,
		strings.Repeat("a", MaxPatternBytes+1),
	}
	for _, pattern := range patterns {
		_, err := Compile([]Rule{{
			ExactProcessName: "app",
			TitlePattern:     pattern,
			URLTemplate:      "https://example.com/$1",
			Enabled:          true,
		}})
		if err == nil {
			t.Fatalf("Compile() accepted %q", pattern)
		}
	}
}

func TestCompileRejectsCredentialURLTemplates(t *testing.T) {
	t.Parallel()
	_, err := Compile([]Rule{{
		ExactProcessName: "app",
		URLTemplate:      "https://user:password@example.com/",
		Enabled:          true,
	}})
	if err == nil {
		t.Fatal("Compile() accepted credential-bearing URL")
	}
}
