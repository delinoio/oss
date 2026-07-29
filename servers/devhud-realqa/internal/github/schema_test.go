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

func TestIssueFormRejectsHiddenShortName(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug
description: Report a bug
body:
  - type: input
    id: summary
    attributes:
      label: Summary
`)
	if _, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	); err == nil || !strings.Contains(err.Error(), "name, description, and body") {
		t.Fatalf("expected short name rejection, got %v", err)
	}
}

func TestIssueFormRejectsInvalidFieldIDCharacters(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: input
    id: repro.steps
    attributes:
      label: Reproduction steps
`)
	if _, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	); err == nil || !strings.Contains(err.Error(), "field ID or label is invalid") {
		t.Fatalf("expected invalid field ID rejection, got %v", err)
	}
}

func TestIssueFormRejectsMultilineInputAnswer(t *testing.T) {
	t.Parallel()
	form := IssueForm{Fields: []FormField{{
		ID: "summary", Kind: FormFieldInput, Label: "Summary",
	}}}
	if _, err := RenderIssueForm(form, []FormAnswer{{
		FieldID: "summary", Values: []string{"first line\r\nsecond line"},
	}}); err == nil || !strings.Contains(err.Error(), "single-line") {
		t.Fatalf("expected multiline input rejection, got %v", err)
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

func TestIssueFormRejectsDuplicateDropdownOptions(t *testing.T) {
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
        - High
        - " High "
`)
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err == nil || !strings.Contains(err.Error(), "dropdown options must be unique") {
		t.Fatalf("expected duplicate dropdown rejection, got form=%#v err=%v", form, err)
	}
}

func TestIssueFormRejectsDuplicateCheckboxOptions(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: checkboxes
    id: terms
    attributes:
      label: Terms
      options:
        - label: I agree
        - label: " I agree "
`)
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err == nil || !strings.Contains(err.Error(), "checkbox options must be unique") {
		t.Fatalf("expected duplicate checkbox rejection, got form=%#v err=%v", form, err)
	}
}

func TestIssueFormRejectsDuplicateLabelsWithoutProviderIDs(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: input
    attributes:
      label: Summary
  - type: textarea
    attributes:
      label: " Summary "
`)
	form, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	)
	if err == nil || !strings.Contains(err.Error(), "without IDs must have unique labels") {
		t.Fatalf("expected duplicate generated-label rejection, got form=%#v err=%v", form, err)
	}
}

func TestIssueFormAllowsDuplicateLabelsWithProviderID(t *testing.T) {
	t.Parallel()
	contents := []byte(`
name: Bug report
description: Report a bug
body:
  - type: input
    attributes:
      label: Summary
  - type: textarea
    id: details
    attributes:
      label: Summary
`)
	if _, err := ParseIssueForm(
		".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
	); err != nil {
		t.Fatalf("provider ID should disambiguate duplicate labels: %v", err)
	}
}

func TestIssueFormRejectsForbiddenCredentialLabels(t *testing.T) {
	t.Parallel()
	for _, contents := range [][]byte{
		[]byte(`
name: Bug report
description: Report a bug
body:
  - type: input
    attributes:
      label: Password
`),
		[]byte(`
name: Bug report
description: Report a bug
body:
  - type: textarea
    attributes:
      label: Enter your PASSWORD here
`),
	} {
		if _, err := ParseIssueForm(
			".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
		); err == nil || !strings.Contains(err.Error(), "forbidden term") {
			t.Fatalf("expected forbidden label rejection, got %v", err)
		}
	}
}

func TestIssueFormRequiresSupportedSubmittedField(t *testing.T) {
	t.Parallel()
	for _, contents := range [][]byte{
		[]byte(`
name: Bug report
description: Report a bug
body:
  - type: markdown
    attributes:
      value: Describe the bug.
`),
		[]byte(`
name: Bug report
description: Report a bug
body:
  - type: upload
    attributes:
      label: Screenshots
    validations:
      required: false
`),
	} {
		if _, err := ParseIssueForm(
			".github/ISSUE_TEMPLATE/bug.yml", `"fixture-etag"`, contents,
		); err == nil || !strings.Contains(err.Error(), "supported submitted field") {
			t.Fatalf("expected submitted-field rejection, got %v", err)
		}
	}
}

func TestIssueFormPreservesTextareaAnswerWhitespace(t *testing.T) {
	t.Parallel()
	form := IssueForm{Fields: []FormField{{
		ID: "logs", Kind: FormFieldTextarea, Label: "Logs", Render: "yaml",
	}}}
	rendered, err := RenderIssueForm(form, []FormAnswer{{
		FieldID: "logs", Values: []string{"\r\n  nested: true\r\n"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	expected := "### Logs\n\n```yaml\n\n  nested: true\n```"
	if rendered != expected {
		t.Fatalf("textarea whitespace was not preserved:\n%q", rendered)
	}
}

func TestIssueFormRenderFenceExceedsAnswerBackticks(t *testing.T) {
	t.Parallel()
	form := IssueForm{Fields: []FormField{{
		ID: "logs", Kind: FormFieldTextarea, Label: "Logs", Render: "text",
	}}}
	rendered, err := RenderIssueForm(form, []FormAnswer{{
		FieldID: "logs", Values: []string{"before\n```\nafter\n``````"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	expected := "### Logs\n\n```````text\nbefore\n```\nafter\n``````\n```````"
	if rendered != expected {
		t.Fatalf("unexpected fenced response:\n%s", rendered)
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
