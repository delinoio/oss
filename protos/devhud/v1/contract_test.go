package devhudv1_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	devhudv1 "github.com/delinoio/oss/protos/devhud/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func protocolFiles() []protoreflect.FileDescriptor {
	return []protoreflect.FileDescriptor{
		devhudv1.File_devhud_v1_account_proto,
		devhudv1.File_devhud_v1_admin_proto,
		devhudv1.File_devhud_v1_bootstrap_proto,
		devhudv1.File_devhud_v1_common_proto,
		devhudv1.File_devhud_v1_diagnostics_proto,
		devhudv1.File_devhud_v1_settings_proto,
		devhudv1.File_devhud_v1_upload_proto,
	}
}

func TestRepresentativeProtoJSONFixturesRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message proto.Message
	}{
		{name: "bootstrap", message: &devhudv1.GetBootstrapResponse{}},
		{name: "settings", message: &devhudv1.GetSettingsResponse{}},
		{name: "upload", message: &devhudv1.FinalizeUploadResponse{}},
		{name: "diagnostics", message: &devhudv1.SubmitCrashReportRequest{}},
		{name: "audit", message: &devhudv1.AdminServiceListAuditEventsResponse{}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture, err := os.ReadFile(filepath.Join("testdata", test.name+".json"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if err := protojson.Unmarshal(fixture, test.message); err != nil {
				t.Fatalf("unmarshal ProtoJSON: %v", err)
			}

			wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(test.message)
			if err != nil {
				t.Fatalf("marshal binary: %v", err)
			}
			clone := test.message.ProtoReflect().Type().New().Interface()
			if err := proto.Unmarshal(wire, clone); err != nil {
				t.Fatalf("unmarshal binary: %v", err)
			}
			if !proto.Equal(test.message, clone) {
				t.Fatal("binary round trip changed the message")
			}
		})
	}
}

func TestServiceSurfaceIsExactAndUnary(t *testing.T) {
	t.Parallel()

	expected := map[protoreflect.FullName][]protoreflect.Name{
		"devhud.v1.BootstrapService":   {"GetBootstrap"},
		"devhud.v1.SettingsService":    {"GetSettings", "ReplaceSettings"},
		"devhud.v1.UploadService":      {"CreateUpload", "FinalizeUpload", "ListUploads", "DeleteUpload"},
		"devhud.v1.AccountService":     {"GetAccount", "DeleteAccount", "RestoreAccount"},
		"devhud.v1.DiagnosticsService": {"SubmitCrashReport"},
		"devhud.v1.AdminService":       {"ListUsers", "SetUserBlocked", "GetUserUsage", "ListUploads", "QuarantineUpload", "DeleteUpload", "ListAuditEvents"},
	}

	actual := make(map[protoreflect.FullName][]protoreflect.Name)
	for _, file := range protocolFiles() {
		services := file.Services()
		for index := 0; index < services.Len(); index++ {
			service := services.Get(index)
			methods := service.Methods()
			for methodIndex := 0; methodIndex < methods.Len(); methodIndex++ {
				method := methods.Get(methodIndex)
				if method.IsStreamingClient() || method.IsStreamingServer() {
					t.Fatalf("%s must remain unary", method.FullName())
				}
				actual[service.FullName()] = append(actual[service.FullName()], method.Name())
			}
		}
	}

	if len(actual) != len(expected) {
		t.Fatalf("got %d services, want %d", len(actual), len(expected))
	}
	for service, wantMethods := range expected {
		gotMethods, ok := actual[service]
		if !ok {
			t.Fatalf("missing service %s", service)
		}
		if strings.Join(namesToStrings(gotMethods), ",") != strings.Join(namesToStrings(wantMethods), ",") {
			t.Fatalf("%s methods = %v, want %v", service, gotMethods, wantMethods)
		}
	}
}

func TestSchemaExcludesForbiddenPayloadsAndRESTAnnotations(t *testing.T) {
	t.Parallel()

	forbiddenFields := map[string]struct{}{
		"pat": {}, "r2_credentials": {}, "secret": {}, "screenshot_bytes": {},
		"deck_results": {}, "dom": {}, "local_path": {}, "agent_output": {},
	}
	for _, file := range protocolFiles() {
		messages := file.Messages()
		for index := 0; index < messages.Len(); index++ {
			walkMessage(messages.Get(index), map[protoreflect.FullName]bool{}, func(message protoreflect.MessageDescriptor) {
				fields := message.Fields()
				for fieldIndex := 0; fieldIndex < fields.Len(); fieldIndex++ {
					field := fields.Get(fieldIndex)
					if _, forbidden := forbiddenFields[string(field.Name())]; forbidden {
						t.Errorf("forbidden field %s", field.FullName())
					}
				}
			})
		}
	}

	protoFiles, err := filepath.Glob("*.proto")
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	for _, path := range protoFiles {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(contents)
		if strings.Contains(text, "google/api/annotations.proto") || strings.Contains(text, "google.api.http") {
			t.Errorf("%s duplicates a business RPC as REST", path)
		}
	}
}

func TestAdminMessagesCannotReachSettingsBodies(t *testing.T) {
	t.Parallel()

	admin := devhudv1.File_devhud_v1_admin_proto.Services().ByName("AdminService")
	if admin == nil {
		t.Fatal("AdminService descriptor missing")
	}
	seen := make(map[protoreflect.FullName]bool)
	methods := admin.Methods()
	for index := 0; index < methods.Len(); index++ {
		method := methods.Get(index)
		for _, root := range []protoreflect.MessageDescriptor{method.Input(), method.Output()} {
			walkMessage(root, seen, func(message protoreflect.MessageDescriptor) {
				if message.FullName() == "google.protobuf.Struct" || message.FullName() == "devhud.v1.SettingsSnapshot" {
					t.Errorf("AdminService reaches forbidden settings type %s", message.FullName())
				}
			})
		}
	}
}

func TestUploadFixtureUsesUuidV7AndRawChecksum(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile(filepath.Join("testdata", "upload.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	response := &devhudv1.FinalizeUploadResponse{}
	if err := protojson.Unmarshal(fixture, response); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if response.Upload == nil {
		t.Fatal("upload missing")
	}
	for label, id := range map[string]*devhudv1.UuidV7{
		"upload": response.Upload.UploadId, "submission": response.Upload.SubmissionId, "group": response.Upload.UploadGroupId,
	} {
		if id == nil || !uuidV7Pattern.MatchString(id.Value) {
			t.Errorf("%s ID is not UUID v7: %v", label, id)
		}
	}
	if got := len(response.Upload.Sha256); got != 32 {
		t.Fatalf("checksum length = %d, want 32", got)
	}
}

func namesToStrings(names []protoreflect.Name) []string {
	values := make([]string, len(names))
	for index, name := range names {
		values[index] = string(name)
	}
	return values
}

func walkMessage(
	message protoreflect.MessageDescriptor,
	seen map[protoreflect.FullName]bool,
	visit func(protoreflect.MessageDescriptor),
) {
	if seen[message.FullName()] {
		return
	}
	seen[message.FullName()] = true
	visit(message)
	fields := message.Fields()
	for index := 0; index < fields.Len(); index++ {
		field := fields.Get(index)
		if field.Message() != nil {
			walkMessage(field.Message(), seen, visit)
		}
	}
}
