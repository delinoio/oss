package realqa_test

import (
	"slices"
	"strings"
	"testing"

	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCanonicalServicesAndMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file    protoreflect.FileDescriptor
		service protoreflect.Name
		methods []string
	}{
		{
			file:    realqav1.File_devhud_realqa_v1_preset_proto,
			service: "RealQAPresetService",
			methods: []string{
				"ListPresets",
				"GetPreset",
				"CreatePreset",
				"UpdatePreset",
				"DeletePreset",
				"DeleteFeatureData",
			},
		},
		{
			file:    realqav1.File_devhud_realqa_v1_tracker_proto,
			service: "RealQATrackerService",
			methods: []string{
				"GetGitHubConnection",
				"StartGitHubConnection",
				"ListGitHubInstallations",
				"DisconnectGitHubConnection",
				"ListRepositories",
				"GetRepositoryIssueSchema",
			},
		},
		{
			file:    realqav1.File_devhud_realqa_v1_submission_proto,
			service: "RealQASubmissionService",
			methods: []string{
				"ListSubmissions",
				"CreateSubmission",
				"CreateImageUpload",
				"FinalizeImageUpload",
				"SubmitIssue",
				"GetSubmission",
				"RebindSubmissionStorageAuthorization",
				"DeleteImage",
				"DeleteSubmissionAssets",
			},
		},
	}

	var serviceNames []string
	for _, test := range tests {
		services := test.file.Services()
		if services.Len() != 1 {
			t.Fatalf("%s services = %d, want 1", test.file.Path(), services.Len())
		}
		service := services.ByName(test.service)
		if service == nil {
			t.Fatalf("%s missing %s", test.file.Path(), test.service)
		}
		serviceNames = append(serviceNames, string(service.FullName()))
		got := methodNames(service.Methods())
		if !slices.Equal(got, test.methods) {
			t.Errorf("%s methods = %v, want %v", test.service, got, test.methods)
		}
	}

	slices.Sort(serviceNames)
	wantServices := []string{
		"devhud.realqa.v1.RealQAPresetService",
		"devhud.realqa.v1.RealQASubmissionService",
		"devhud.realqa.v1.RealQATrackerService",
	}
	if !slices.Equal(serviceNames, wantServices) {
		t.Errorf("canonical services = %v, want %v", serviceNames, wantServices)
	}
}

func TestHardLimitsAndNoImageCountField(t *testing.T) {
	t.Parallel()

	want := map[realqav1.RealQALimit]int32{
		realqav1.RealQALimit_REAL_QA_LIMIT_PERSONAL_PRESETS:          50,
		realqav1.RealQALimit_REAL_QA_LIMIT_ORGANIZATION_PRESETS:      250,
		realqav1.RealQALimit_REAL_QA_LIMIT_DEVICE_SHORTCUTS:          20,
		realqav1.RealQALimit_REAL_QA_LIMIT_MAX_IMAGE_ENCODED_BYTES:   25 * 1024 * 1024,
		realqav1.RealQALimit_REAL_QA_LIMIT_MAX_SESSION_ENCODED_BYTES: 250 * 1024 * 1024,
		realqav1.RealQALimit_REAL_QA_LIMIT_MAX_DECODED_IMAGE_PIXELS:  100_000_000,
		realqav1.RealQALimit_REAL_QA_LIMIT_MAX_FINAL_BODY_UTF8_BYTES: 60_000,
	}
	for limit, numeric := range want {
		if int32(limit) != numeric {
			t.Errorf("%s = %d, want %d", limit, limit, numeric)
		}
	}

	for _, file := range contractFiles() {
		walkMessages(file.Messages(), func(message protoreflect.MessageDescriptor) {
			fields := message.Fields()
			for index := range fields.Len() {
				name := string(fields.Get(index).Name())
				if strings.Contains(name, "screenshot_count") ||
					strings.Contains(name, "image_count") ||
					strings.Contains(name, "max_images") {
					t.Errorf("%s contains forbidden count field %s", message.FullName(), name)
				}
			}
		})
	}
}

func TestClosedCoreEnumsAndGitHubOnlyTracker(t *testing.T) {
	t.Parallel()

	requiredEnums := []protoreflect.Name{
		"OwnerScopeKind",
		"CaptureMode",
		"SelectorMode",
		"TrackerKind",
		"SubmissionState",
		"UploadState",
		"FailureClass",
		"AssetState",
		"ErrorReason",
	}
	enums := realqav1.File_devhud_realqa_v1_common_proto.Enums()
	for _, name := range requiredEnums {
		enum := enums.ByName(name)
		if enum == nil {
			t.Errorf("missing closed enum %s", name)
			continue
		}
		if enum.Values().Get(0).Number() != 0 ||
			!strings.HasSuffix(string(enum.Values().Get(0).Name()), "_UNSPECIFIED") {
			t.Errorf("%s does not start with an UNSPECIFIED zero value", name)
		}
	}

	tracker := enums.ByName("TrackerKind")
	if tracker.Values().Len() != 2 ||
		tracker.Values().ByName("TRACKER_KIND_GITHUB_COM") == nil {
		t.Errorf("TrackerKind values = %v; v1 must contain only GitHub.com", tracker.Values())
	}
}

func TestIssueDefinitionDescriptorBoundary(t *testing.T) {
	t.Parallel()

	common := realqav1.File_devhud_realqa_v1_common_proto
	if common.Messages().ByName("RepositoryIssueDefinitionRef") == nil ||
		common.Enums().ByName("RepositoryIssueDefinitionKind") == nil {
		t.Fatal("common descriptor is missing shared issue-definition symbols")
	}
	for _, file := range []protoreflect.FileDescriptor{
		realqav1.File_devhud_realqa_v1_tracker_proto,
		realqav1.File_devhud_realqa_v1_submission_proto,
	} {
		imports := file.Imports()
		for index := range imports.Len() {
			if imports.Get(index).Path() == "devhud-realqa/v1/preset.proto" {
				t.Fatalf("%s imports the preset descriptor", file.Path())
			}
		}
	}
	preset := realqav1.File_devhud_realqa_v1_preset_proto
	for _, name := range []protoreflect.Name{
		"ProcessUrlRule",
		"ShortcutDefinition",
		"Preset",
		"ListPresetsRequest",
		"ListPresetsResponse",
		"GetPresetRequest",
		"GetPresetResponse",
		"CreatePresetRequest",
		"CreatePresetResponse",
		"UpdatePresetRequest",
		"UpdatePresetResponse",
		"DeletePresetRequest",
		"DeletePresetResponse",
		"OwnerFeatureDeletion",
		"DelibaseAccountLifecycleDeletion",
		"DelibaseOrganizationLifecycleDeletion",
		"DeleteFeatureDataRequest",
		"DeleteFeatureDataResponse",
	} {
		if preset.Messages().ByName(name) == nil {
			t.Errorf("preset descriptor is missing compatibility message %s", name)
		}
	}
	if preset.Enums().ByName("FeatureDeletionTriggerKind") == nil {
		t.Error("preset descriptor is missing FeatureDeletionTriggerKind")
	}
}

func TestRevisionPaginationProviderAndContentBoundaries(t *testing.T) {
	t.Parallel()

	common := realqav1.File_devhud_realqa_v1_common_proto.Messages()
	if common.ByName("UuidV7").Fields().ByName("value").Kind() != protoreflect.StringKind {
		t.Error("UuidV7.value must be a string")
	}
	revision := common.ByName("Revision")
	if revision.Fields().ByName("value") == nil || revision.Fields().ByName("etag") == nil {
		t.Error("Revision must carry both numeric revision and ETag")
	}

	for _, pair := range [][2]protoreflect.MessageDescriptor{
		{
			realqav1.File_devhud_realqa_v1_preset_proto.Messages().ByName("ListPresetsRequest"),
			realqav1.File_devhud_realqa_v1_preset_proto.Messages().ByName("ListPresetsResponse"),
		},
		{
			realqav1.File_devhud_realqa_v1_tracker_proto.Messages().ByName("ListRepositoriesRequest"),
			realqav1.File_devhud_realqa_v1_tracker_proto.Messages().ByName("ListRepositoriesResponse"),
		},
		{
			realqav1.File_devhud_realqa_v1_submission_proto.Messages().ByName("ListSubmissionsRequest"),
			realqav1.File_devhud_realqa_v1_submission_proto.Messages().ByName("ListSubmissionsResponse"),
		},
	} {
		if pair[0].Fields().ByName("page") == nil || pair[1].Fields().ByName("page") == nil {
			t.Errorf("%s/%s missing opaque pagination", pair[0].Name(), pair[1].Name())
		}
	}

	provider := common.ByName("ProviderExtension")
	if provider.Oneofs().Len() != 1 ||
		provider.Oneofs().Get(0).Fields().Len() != 1 ||
		provider.Oneofs().Get(0).Fields().ByName("github") == nil {
		t.Errorf("ProviderExtension must be a GitHub-only typed union: %v", provider)
	}

	summary := realqav1.File_devhud_realqa_v1_submission_proto.Messages().ByName("SubmissionSummary")
	for _, forbidden := range []protoreflect.Name{
		"title",
		"body",
		"url",
		"dom_selection",
		"object_key",
	} {
		if summary.Fields().ByName(forbidden) != nil {
			t.Errorf("SubmissionSummary leaks forbidden retained field %s", forbidden)
		}
	}
}

func TestMessagesContainNoAuthenticationCredentials(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"authorization_header",
		"access_token",
		"refresh_token",
		"forwarded_user_token",
		"client_secret",
		"github_token",
		"r2_secret",
		"webhook_secret",
	}
	for _, file := range contractFiles() {
		walkMessages(file.Messages(), func(message protoreflect.MessageDescriptor) {
			fields := message.Fields()
			for index := range fields.Len() {
				name := string(fields.Get(index).Name())
				if slices.Contains(forbidden, name) {
					t.Errorf("%s contains credential field %s", message.FullName(), name)
				}
			}
		})
	}
}

func contractFiles() []protoreflect.FileDescriptor {
	return []protoreflect.FileDescriptor{
		realqav1.File_devhud_realqa_v1_common_proto,
		realqav1.File_devhud_realqa_v1_preset_proto,
		realqav1.File_devhud_realqa_v1_tracker_proto,
		realqav1.File_devhud_realqa_v1_submission_proto,
	}
}

func methodNames(methods protoreflect.MethodDescriptors) []string {
	names := make([]string, 0, methods.Len())
	for index := range methods.Len() {
		names = append(names, string(methods.Get(index).Name()))
	}
	return names
}

func walkMessages(
	messages protoreflect.MessageDescriptors,
	visit func(protoreflect.MessageDescriptor),
) {
	for index := range messages.Len() {
		message := messages.Get(index)
		visit(message)
		walkMessages(message.Messages(), visit)
	}
}
