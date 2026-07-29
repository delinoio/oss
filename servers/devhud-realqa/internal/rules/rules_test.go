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
		`[a-z&&[^x]]`, `[a-z--[aeiou]]`, `[[:alpha:]&&[^x]]`, `a{101}`,
		`[a[b]&&c]`, `(a+)+$`, `(?:a|aa)+$`, `((?:a|aa))+$`, `([a-z]*)*$`,
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

func TestCompileRejectsBackslashURLTemplates(t *testing.T) {
	t.Parallel()
	for _, template := range []string{
		`https://example.com/a\b`,
		`https://example.com/?value=\b`,
	} {
		if _, err := Compile([]Rule{{
			ExactProcessName: "app",
			URLTemplate:      template,
			Enabled:          true,
		}}); err == nil {
			t.Fatalf("Compile() accepted backslash URL template %q", template)
		}
	}
}

func TestCompileAcceptsCommonRegexEdgeCases(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{
		`^[[:digit:]{101}]$`,
		`^([[:digit:]{101}])+$`,
		`^\p{^Greek}+$`,
		`^\P{^Greek}+$`,
		`^\x{3000}$`,
		`^(\x{41})+$`,
		`^\_$`,
		`^\!$`,
		`^[\p{Greek}-\p{Latin}]$`,
		`^[\d-\w]$`,
		`^Issue {$`,
		`^Issue }$`,
		`^(a{01})+$`,
		`^(a{1,02})+$`,
	} {
		if _, err := Compile([]Rule{{
			ExactProcessName: "app",
			TitlePattern:     pattern,
			URLTemplate:      "HTTPS://example.com/",
			Enabled:          true,
		}}); err != nil {
			t.Fatalf("Compile() rejected common regex pattern %q: %v", pattern, err)
		}
	}
}

func TestResolveSkipsBackslashExpansion(t *testing.T) {
	t.Parallel()
	set, err := Compile([]Rule{
		{
			ExactProcessName: "app",
			TitlePattern:     `^(.+)$`,
			URLTemplate:      "https://example.com/$1",
			Enabled:          true,
		},
		{
			ExactProcessName: "app",
			URLTemplate:      "https://fallback.example/",
			Enabled:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := set.Resolve("app", `a\b`)
	if !ok || value != "https://fallback.example/" {
		t.Fatalf("Resolve() = %q, %v", value, ok)
	}
}

func TestCompileAcceptsLiteralClassOperatorText(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{
		`^issue--draft$`, `^issue&&draft$`, `^issue~~draft$`,
		`^[[]--$`, `^[[]&&$`, `^[[]~~$`,
	} {
		if _, err := Compile([]Rule{{
			ExactProcessName: "app",
			TitlePattern:     pattern,
			URLTemplate:      "https://example.com/",
			Enabled:          true,
		}}); err != nil {
			t.Fatalf("Compile() rejected safe literal %q: %v", pattern, err)
		}
	}
}
