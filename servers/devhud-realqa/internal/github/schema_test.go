package github

import (
	"os"
	"strings"
	"testing"
)

func TestParseMarkdownTemplateFixture(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("testdata/bug.md")
	if err != nil {
		t.Fatal(err)
	}
	template, err := ParseMarkdownTemplate(
		".github/ISSUE_TEMPLATE/bug.md", `"fixture-etag"`, contents,
	)
	if err != nil {
		t.Fatal(err)
	}
	if template.Definition.Name != "Bug report" ||
		template.TitlePrefix != "[Bug] " ||
		strings.Join(template.DefaultLabels, ",") != "bug,triage" ||
		strings.Join(template.DefaultAssignees, ",") != "octocat" ||
		!strings.HasPrefix(template.Body, "## Repository response") {
		t.Fatalf("unexpected template: %#v", template)
	}
}

func TestMarkdownTemplateAcceptsIssueTypeMetadata(t *testing.T) {
	t.Parallel()
	template, err := ParseMarkdownTemplate(
		".github/ISSUE_TEMPLATE/bug.md", `"fixture-etag"`, []byte(`---
name: Bug report
about: Report a bug
type: bug
---
Describe the bug.
`))
	if err != nil {
		t.Fatal(err)
	}
	if template.Definition.Name != "Bug report" || template.IssueType != "bug" ||
		template.Body != "Describe the bug.\n" {
		t.Fatalf("unexpected template: %#v", template)
	}
}

func TestIssueFormPreservesTypeAndTextDefaults(t *testing.T) {
	t.Parallel()
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, []byte(`
name: Bug report
description: Report a bug
type: Bug
body:
  - type: input
    id: summary
    attributes:
      label: Summary
      value: A bug happened
  - type: textarea
    id: steps
    attributes:
      label: Steps
      value: |
        1. Start
        2. Observe
`))
	if err != nil {
		t.Fatal(err)
	}
	if form.IssueType != "Bug" || len(form.Fields) != 2 ||
		form.Fields[0].DefaultValue != "A bug happened" ||
		form.Fields[1].DefaultValue != "1. Start\n2. Observe\n" {
		t.Fatalf("defaults were not preserved: %#v", form)
	}
}

func TestParseAndValidateIssueFormFixture(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("testdata/bug.yml")
	if err != nil {
		t.Fatal(err)
	}
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderIssueForm(form, []FormAnswer{
		{FieldID: "summary", Values: []string{"The window closes."}},
		{FieldID: "severity", Values: []string{"High"}},
		{FieldID: "terms", Values: []string{"I searched for duplicates."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Join([]string{
		"### Summary\n\nThe window closes.",
		"### Severity\n\nHigh",
		"### Checklist\n\n- [x] I searched for duplicates.",
	}, "\n\n")
	if rendered != expected {
		t.Fatalf("unexpected response:\n%s", rendered)
	}

	_, err = RenderIssueForm(form, []FormAnswer{
		{FieldID: "summary", Values: []string{"The window closes."}},
		{FieldID: "severity", Values: []string{"Critical"}},
		{FieldID: "terms", Values: []string{"I searched for duplicates."}},
	})
	if err == nil || !strings.Contains(err.Error(), "selected option is invalid") {
		t.Fatalf("expected provider option validation, got %v", err)
	}
	_, err = RenderIssueForm(form, []FormAnswer{
		{FieldID: "severity", Values: []string{"High"}},
		{FieldID: "terms", Values: []string{"I can reproduce this."}},
	})
	if err == nil || !strings.Contains(err.Error(), "response is required") {
		t.Fatalf("expected required input validation, got %v", err)
	}
}

func TestIssueFormRejectsProviderRequiredCheckboxOmission(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("testdata/bug.yml")
	if err != nil {
		t.Fatal(err)
	}
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RenderIssueForm(form, []FormAnswer{
		{FieldID: "summary", Values: []string{"Summary"}},
		{FieldID: "severity", Values: []string{"Low"}},
		{FieldID: "terms", Values: []string{"I can reproduce this."}},
	})
	if err == nil || !strings.Contains(err.Error(), "required checkbox") {
		t.Fatalf("expected required checkbox validation, got %v", err)
	}
}

func TestIssueFormRejectsUnsupportedDropdownDefault(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: dropdown
    id: severity
    attributes:
      label: Severity
      options:
        - Low
        - High
      default: 1
`)
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err == nil || !strings.Contains(err.Error(), "dropdown default is unsupported") {
		t.Fatalf("expected unsupported dropdown default, got form=%#v err=%v", form, err)
	}
}

func TestIssueFormPreservesMultipleDropdown(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: dropdown
    id: browsers
    attributes:
      label: Browsers
      multiple: true
      options:
        - Firefox
        - Chrome
`)
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Fields) != 1 || !form.Fields[0].Multiple {
		t.Fatalf("unexpected multiple dropdown: %#v", form.Fields)
	}
	if _, err = RenderIssueForm(form, []FormAnswer{{
		FieldID: "browsers", Values: []string{"Firefox", "Chrome"},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestIssueFormAcceptsCurrentGitHubMetadataAndOptionalFields(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
type: bug
body:
  - type: textarea
    attributes:
      label: Current behavior
  - type: upload
    id: screenshots
    attributes:
      label: Screenshots
      description: Add screenshots if available.
    validations:
      required: false
      accept: ".png,.jpg,.log"
  - type: input
    id: realqa-field-1
    attributes:
      label: Expected behavior
`)
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Fields) != 2 ||
		form.Fields[0].ID != "realqa-field-1-2" ||
		form.Fields[1].ID != "realqa-field-1" {
		t.Fatalf("unexpected normalized fields: %#v", form.Fields)
	}
	rendered, err := RenderIssueForm(form, []FormAnswer{
		{FieldID: "realqa-field-1-2", Values: []string{"The window closes."}},
		{FieldID: "realqa-field-1", Values: []string{"The window stays open."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "Screenshots") {
		t.Fatalf("optional upload field was rendered: %s", rendered)
	}
}

func TestIssueFormRejectsProjectDefaults(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
projects: ["octo-org/1"]
body:
  - type: input
    id: summary
    attributes:
      label: Summary
`)
	if _, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	); err == nil || !strings.Contains(err.Error(), "project defaults are unsupported") {
		t.Fatalf("expected unsupported project defaults, got %v", err)
	}
}

func TestIssueFormRejectsRequiredUploadField(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: upload
    attributes:
      label: Screenshots
    validations:
      required: true
`)
	if _, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	); err == nil || !strings.Contains(err.Error(), "upload field is unsupported") {
		t.Fatalf("expected required upload rejection, got %v", err)
	}
}
