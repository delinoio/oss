package contracttest

import (
	"bytes"
	"testing"

	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestServiceInventoryAndResponseMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		service protoreflect.ServiceDescriptor
		methods []protoreflect.Name
	}{
		{serviceByName(t, devhudv1.File_devhud_v1_bootstrap_proto, "BootstrapService"), []protoreflect.Name{"GetBootstrap"}},
		{serviceByName(t, devhudv1.File_devhud_v1_settings_proto, "SettingsService"), []protoreflect.Name{"GetSettings", "ReplaceSettings"}},
		{serviceByName(t, devhudv1.File_devhud_v1_upload_proto, "UploadService"), []protoreflect.Name{"CreateUpload", "FinalizeUpload", "ListUploads", "DeleteUpload"}},
		{serviceByName(t, devhudv1.File_devhud_v1_account_proto, "AccountService"), []protoreflect.Name{"GetAccount", "DeleteAccount", "RestoreAccount"}},
		{serviceByName(t, devhudv1.File_devhud_v1_diagnostics_proto, "DiagnosticsService"), []protoreflect.Name{"SubmitCrashReport"}},
		{serviceByName(t, devhudv1.File_devhud_v1_admin_proto, "AdminService"), []protoreflect.Name{"ListUsers", "SetUserBlocked", "GetUserUsage", "ListUploads", "QuarantineUpload", "DeleteUpload", "ListAuditEvents"}},
	}

	methodCount := 0
	for _, test := range tests {
		methods := test.service.Methods()
		methodCount += methods.Len()
		if methods.Len() != len(test.methods) {
			t.Fatalf("%s has %d methods, want %d", test.service.FullName(), methods.Len(), len(test.methods))
		}
		for index, expected := range test.methods {
			method := methods.Get(index)
			if method.Name() != expected {
				t.Errorf("%s method %d is %s, want %s", test.service.FullName(), index, method.Name(), expected)
			}
			metadata := method.Output().Fields().ByName("metadata")
			if metadata == nil || metadata.Message().FullName() != "devhud.v1.ResponseMetadata" {
				t.Errorf("%s.%s response does not contain ResponseMetadata", test.service.FullName(), method.Name())
			}
		}
	}
	if methodCount != 18 {
		t.Fatalf("service inventory contains %d methods, want 18", methodCount)
	}
}

func TestUploadFinalizationBinaryRoundTrip(t *testing.T) {
	t.Parallel()

	checksum := make([]byte, 32)
	for index := range checksum {
		checksum[index] = byte(index)
	}
	id := &devhudv1.UuidV7{Value: "018f47a2-7b3c-7def-8abc-1234567890ab"}
	want := &devhudv1.FinalizeUploadRequest{
		UploadId:          id,
		SubmissionId:      id,
		UploadGroupId:     id,
		ReservationId:     id,
		StagingGeneration: 42,
		ExpectedSizeBytes: 50_000_000,
		ExpectedSha256:    checksum,
		ObservedEtag:      "immutable-etag",
	}

	data, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("marshal finalization request: %v", err)
	}
	got := new(devhudv1.FinalizeUploadRequest)
	if err := proto.Unmarshal(data, got); err != nil {
		t.Fatalf("unmarshal finalization request: %v", err)
	}
	if !proto.Equal(want, got) {
		t.Fatalf("round trip mismatch: got %v, want %v", got, want)
	}
	if !bytes.Equal(got.GetExpectedSha256(), checksum) {
		t.Fatal("round trip changed raw checksum bytes")
	}
}

func serviceByName(t *testing.T, file protoreflect.FileDescriptor, name protoreflect.Name) protoreflect.ServiceDescriptor {
	t.Helper()
	service := file.Services().ByName(name)
	if service == nil {
		t.Fatalf("service %s is missing from %s", name, file.Path())
	}
	return service
}
