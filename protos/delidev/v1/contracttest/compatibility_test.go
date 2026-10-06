// SPDX-License-Identifier: Apache-2.0
package contracttest

import (
	"encoding/json"
	"os"
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// This snapshot comes from the last intact historical relocation map. It must
// remain independent of the editable generator input, including after regeneration.
func historicalDeclarationOrder(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile("testdata/legacy-declaration-order.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceRevision string   `json:"sourceRevision"`
		Messages       []string `json:"message"`
		Enums          []string `json:"enum"`
		Services       []string `json:"service"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SourceRevision != "54187b780d48e94a49763e87fc140f449869d9f4" || len(fixture.Messages) != 303 || len(fixture.Enums) != 36 || len(fixture.Services) != 19 {
		t.Fatal("historical order fixture must retain all 358 declarations")
	}
	return map[string][]string{"message": fixture.Messages, "enum": fixture.Enums, "service": fixture.Services}
}

func TestLegacyDescriptorRetainsHistoricalOrder(t *testing.T) {
	legacy := pb.File_delidev_v1_delidev_proto
	actual := map[string][]string{}
	for i := 0; i < legacy.Messages().Len(); i++ {
		actual["message"] = append(actual["message"], string(legacy.Messages().Get(i).Name()))
	}
	for i := 0; i < legacy.Enums().Len(); i++ {
		actual["enum"] = append(actual["enum"], string(legacy.Enums().Get(i).Name()))
	}
	for i := 0; i < legacy.Services().Len(); i++ {
		actual["service"] = append(actual["service"], string(legacy.Services().Get(i).Name()))
	}
	for kind, expected := range historicalDeclarationOrder(t) {
		t.Run(kind, func(t *testing.T) {
			if len(actual[kind]) < len(expected) {
				t.Fatalf("aggregate has %d %ss, want at least %d", len(actual[kind]), kind, len(expected))
			}
			for index, name := range expected {
				if got := actual[kind][index]; got != name {
					t.Errorf("%s[%d] = %s, want historical %s", kind, index, got, name)
				}
			}
			seen := map[string]bool{}
			for _, name := range actual[kind] {
				if seen[name] {
					t.Errorf("duplicate %s %s", kind, name)
				}
				seen[name] = true
			}
		})
	}
	for _, sample := range []struct {
		kind  string
		index int
		name  string
	}{
		{"message", 272, "GetPullRequestFixCapabilitiesRequest"},
		{"message", 276, "CreateTerminalRequest"},
		{"message", 280, "WatchTerminalOutputRequest"},
		{"enum", 32, "PullRequestFixProfile"},
		{"enum", 33, "TerminalAction"},
		{"service", 16, "PullRequestFixService"},
		{"service", 17, "TerminalService"},
		{"service", 18, "BrowserService"},
	} {
		if len(actual[sample.kind]) <= sample.index {
			t.Errorf("missing %s[%d], want %s", sample.kind, sample.index, sample.name)
			continue
		}
		if got := actual[sample.kind][sample.index]; got != sample.name {
			t.Errorf("%s[%d] = %s, want %s", sample.kind, sample.index, got, sample.name)
		}
	}
}

func TestLegacyDescriptorRetainsAggregateReflection(t *testing.T) {
	legacy := pb.File_delidev_v1_delidev_proto
	message := legacy.Messages().ByName("Resource")
	if message == nil || message.FullName() != "delidev.v1.Resource" || message.Fields().ByName("id").Number() != 1 {
		t.Fatal("legacy reflection lost Resource")
	}
	service := legacy.Services().ByName("SystemService")
	if service == nil || service.Methods().ByName("GetStatus").Output().FullName() != "delidev.v1.GetStatusResponse" {
		t.Fatal("legacy reflection lost SystemService")
	}
	enum := legacy.Enums().ByName("SystemCapability")
	if enum == nil || enum.Values().ByName("SYSTEM_CAPABILITY_AUTOMATIC_TITLES_V1").Number() != 1 {
		t.Fatal("legacy reflection lost capabilities")
	}
	var messages, enums, services int
	protoregistry.GlobalFiles.RangeFilesByPackage("delidev.v1", func(file protoreflect.FileDescriptor) bool {
		if file.Path() != legacy.Path() {
			messages += file.Messages().Len()
			enums += file.Enums().Len()
			services += file.Services().Len()
		}
		return true
	})
	if messages != legacy.Messages().Len() || enums != legacy.Enums().Len() || services != legacy.Services().Len() {
		t.Fatal("aggregate omitted declarations", messages, enums, services)
	}
	restored, err := protodesc.NewFile(protodesc.ToFileDescriptorProto(legacy), protoregistry.GlobalFiles)
	if err != nil || restored.Messages().Len() != messages {
		t.Fatal("aggregate cannot round trip", err)
	}
	for _, canonical := range []protoreflect.ServiceDescriptor{
		pb.File_delidev_v1_workspace_storage_proto.Services().ByName("WorkspaceStorageService"),
		pb.File_delidev_v1_network_proto.Services().ByName("NetworkService"),
		pb.File_delidev_v1_subscription_proto.Services().ByName("SubscriptionService"),
	} {
		for _, file := range []protoreflect.FileDescriptor{legacy, restored} {
			service := file.Services().ByName(canonical.Name())
			if service == nil || service.Index() < 19 {
				t.Errorf("%s missing after the historical service prefix", canonical.Name())
			}
		}
		registered, err := protoregistry.GlobalFiles.FindDescriptorByName(canonical.FullName())
		if err != nil || registered != canonical {
			t.Errorf("canonical service %s changed: %v", canonical.Name(), err)
		}
	}
	for i := 0; i < legacy.Messages().Len(); i++ {
		name := legacy.Messages().Get(i).FullName()
		canonical, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			t.Fatal(err)
		}
		registered, err := protoregistry.GlobalTypes.FindMessageByName(name)
		if err != nil || registered.Descriptor() != canonical {
			t.Errorf("canonical message %s changed: %v", name, err)
		}
	}
	for i := 0; i < legacy.Enums().Len(); i++ {
		name := legacy.Enums().Get(i).FullName()
		canonical, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			t.Fatal(err)
		}
		registered, err := protoregistry.GlobalTypes.FindEnumByName(name)
		if err != nil || registered.Descriptor() != canonical {
			t.Errorf("canonical enum %s changed: %v", name, err)
		}
	}
	// The compatibility view must not replace or register duplicate canonical types.
	canonical, err := protoregistry.GlobalFiles.FindDescriptorByName("delidev.v1.Resource")
	if err != nil || canonical != (&pb.Resource{}).ProtoReflect().Descriptor() {
		t.Fatal("canonical registry changed", err)
	}
}
