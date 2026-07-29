package github

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var fixtureSubmissionID = uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608")

func TestManifestHasOnlyConfiguredMinimumPermissions(t *testing.T) {
	t.Parallel()
	manifest, err := NewManifest(ProjectPermissionNone)
	if err != nil {
		t.Fatal(err)
	}
	if err = manifest.DefaultPermissions.Validate(ProjectPermissionNone); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../github-app-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture Manifest
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture, manifest) {
		t.Fatalf("fixture manifest diverged: %#v", fixture)
	}

	repositoryProject, err := NewManifest(ProjectPermissionRepository)
	if err != nil {
		t.Fatal(err)
	}
	if repositoryProject.DefaultPermissions.RepositoryProjects != PermissionWrite ||
		repositoryProject.DefaultPermissions.OrganizationProjects != PermissionNone {
		t.Fatalf("unexpected repository project permissions: %#v",
			repositoryProject.DefaultPermissions)
	}
	organizationProject, err := NewManifest(ProjectPermissionOrganization)
	if err != nil {
		t.Fatal(err)
	}
	if organizationProject.DefaultPermissions.OrganizationProjects != PermissionWrite ||
		organizationProject.DefaultPermissions.RepositoryProjects != PermissionNone {
		t.Fatalf("unexpected organization project permissions: %#v",
			organizationProject.DefaultPermissions)
	}
}

func TestComposeBodyExactOrderAndUTF8Bytes(t *testing.T) {
	t.Parallel()
	input := IssueInput{
		SubmissionID:       fixtureSubmissionID,
		RepositoryResponse: "## Repository response\n\n재현됨",
		Capture: CaptureMetadata{
			Environment:  []CaptureField{{Key: "OS", Value: "Linux"}},
			SanitizedURL: "https://example.com/path",
		},
		Images: []InlineImage{{
			AltText: "First capture",
			URL:     "https://assets.realqa.deli.dev/abcdefghijklmnopqrstuv",
		}},
	}
	body, err := ComposeBody(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := "## Repository response\n\n재현됨\n\n" +
		"## RealQA capture\n- **OS:** ```Linux```\n- **URL:** <https://example.com/path>\n\n" +
		"![First capture](https://assets.realqa.deli.dev/abcdefghijklmnopqrstuv)\n\n" +
		"<!-- realqa:submission:018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608 -->\n"
	if body != expected {
		t.Fatalf("body order changed:\n%s", body)
	}
	if len([]byte(body)) == len([]rune(body)) {
		t.Fatal("fixture did not exercise UTF-8 byte accounting")
	}
}

func TestComposeBodyUsesSafeEnvironmentCodeSpans(t *testing.T) {
	t.Parallel()
	body, err := ComposeBody(IssueInput{
		SubmissionID: fixtureSubmissionID,
		Capture: CaptureMetadata{Environment: []CaptureField{{
			Key:   "Window",
			Value: "![pixel](https://example.com/pixel) `status`",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body,
		"- **Window:** ``` ![pixel](https://example.com/pixel) `status` ```\n") {
		t.Fatalf("environment metadata did not use a safe code span:\n%s", body)
	}
}

func TestComposeBodyPreservesRepositoryResponseEdgeWhitespace(t *testing.T) {
	t.Parallel()
	body, err := ComposeBody(IssueInput{
		SubmissionID:       fixtureSubmissionID,
		RepositoryResponse: "    command\r\nanswer  \r\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := "    command\nanswer  \n\n\n" +
		"## RealQA capture\n_No capture metadata included._\n\n" +
		"<!-- realqa:submission:018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608 -->\n"
	if body != expected {
		t.Fatalf("repository response whitespace changed:\n%q", body)
	}
}

func TestComposeBodyUsesSafeDOMCodeSpans(t *testing.T) {
	t.Parallel()
	body, err := ComposeBody(IssueInput{
		SubmissionID: fixtureSubmissionID,
		Capture: CaptureMetadata{DOM: &DOMMetadata{
			CSSSelector:    "button[data-label=`approve``now`]",
			AccessibleName: "`Approve`",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body,
		"- **DOM selector:** ```button[data-label=`approve``now`]```\n") ||
		!strings.Contains(body, "- **DOM accessible name:** ``` `Approve` ```\n") {
		t.Fatalf("DOM metadata did not use safe code spans:\n%s", body)
	}
}

func TestComposeBodyRejectsUnsafeCaptureURLAutolinks(t *testing.T) {
	t.Parallel()
	for _, captureURL := range []string{
		"https://example.com/path with space",
		"https://example.com/?next=>outside",
	} {
		_, err := ComposeBody(IssueInput{
			SubmissionID: fixtureSubmissionID,
			Capture:      CaptureMetadata{SanitizedURL: captureURL},
		})
		if err == nil || !strings.Contains(err.Error(), "capture URL is invalid") {
			t.Fatalf("expected unsafe capture URL %q to be rejected, got %v", captureURL, err)
		}
	}
}

func TestComposeBodyRejectsUnsafeInlineImageDestinations(t *testing.T) {
	t.Parallel()
	for _, imageURL := range []string{
		"https://assets.realqa.deli.dev/capture)trailing",
		"https://assets.realqa.deli.dev/capture with-space",
		"https://assets.realqa.deli.dev/capture\u00a0with-space",
	} {
		_, err := ComposeBody(IssueInput{
			SubmissionID: fixtureSubmissionID,
			Images:       []InlineImage{{URL: imageURL}},
		})
		if err == nil || !strings.Contains(err.Error(), "inline image URL is invalid") {
			t.Fatalf("expected unsafe inline image URL %q to be rejected, got %v",
				imageURL, err)
		}
	}
}

func TestComposeBodyAllowsManyImagesUntilSerializedOverflow(t *testing.T) {
	t.Parallel()
	input := IssueInput{
		SubmissionID: fixtureSubmissionID,
		Capture:      CaptureMetadata{},
	}
	for index := 0; index < 500; index++ {
		input.Images = append(input.Images, InlineImage{
			URL: "https://assets.realqa.deli.dev/" + strings.Repeat("a", 22) +
				string(rune('A'+index%26)),
		})
	}
	body, err := ComposeBody(input)
	if err != nil {
		t.Fatalf("500 images below the byte limit should be accepted: %v", err)
	}
	if strings.Count(body, "![") != 500 {
		t.Fatalf("expected every image, got %d", strings.Count(body, "!["))
	}
	for len([]byte(body)) <= BodyByteLimit {
		input.Images = append(input.Images, InlineImage{
			URL: "https://assets.realqa.deli.dev/" + strings.Repeat("z", 32),
		})
		body, err = ComposeBody(input)
		if err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "60,000 UTF-8 bytes") {
		t.Fatalf("expected exact byte overflow, got %v", err)
	}
}

func TestGHESAndCustomHostsAreRejected(t *testing.T) {
	t.Parallel()
	for _, config := range []ClientConfig{
		{APIOrigin: "https://github.example.com/api/v3", WebOrigin: WebOrigin},
		{APIOrigin: APIOrigin, WebOrigin: "https://github.example.com"},
		{APIOrigin: "https://api.github.com.example", WebOrigin: WebOrigin},
	} {
		if _, err := NewClient(config); err == nil ||
			!strings.Contains(err.Error(), "GHES and custom hosts") {
			t.Fatalf("expected host rejection for %#v, got %v", config, err)
		}
	}
}

func TestValidateAssigneeLoginUsesGitHubUsernameRules(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"octocat",
		"mona-lisa",
		strings.Repeat("a", 39),
	} {
		if err := ValidateAssigneeLogin(value); err != nil {
			t.Fatalf("valid login %q was rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"mona.lisa",
		"mona_lisa",
		"-octocat",
		"octocat-",
		"mona--lisa",
		strings.Repeat("a", 40),
	} {
		if err := ValidateAssigneeLogin(value); err == nil {
			t.Fatalf("invalid login %q was accepted", value)
		}
	}
}
