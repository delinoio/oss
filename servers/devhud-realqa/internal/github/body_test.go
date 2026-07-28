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
		"## RealQA capture\n- **OS:** Linux\n- **URL:** <https://example.com/path>\n\n" +
		"![First capture](https://assets.realqa.deli.dev/abcdefghijklmnopqrstuv)\n\n" +
		"<!-- realqa:submission:018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608 -->\n"
	if body != expected {
		t.Fatalf("body order changed:\n%s", body)
	}
	if len([]byte(body)) == len([]rune(body)) {
		t.Fatal("fixture did not exercise UTF-8 byte accounting")
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
