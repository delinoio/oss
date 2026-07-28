package github

import (
	"bytes"
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
		"Thanks for helping us improve.",
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

func TestIssueFormAcceptsDropdownDefault(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Fields) != 1 || len(form.Fields[0].Options) != 2 {
		t.Fatalf("unexpected form: %#v", form)
	}

	invalid := bytes.Replace(contents, []byte("default: 1"), []byte("default: 2"), 1)
	if _, err = ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, invalid,
	); err == nil || !strings.Contains(err.Error(), "dropdown default") {
		t.Fatalf("expected invalid dropdown default, got %v", err)
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
