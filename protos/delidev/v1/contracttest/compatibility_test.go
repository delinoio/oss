// SPDX-License-Identifier: Apache-2.0
package contracttest

import (
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"testing"
)

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
	// The compatibility view must not replace or register duplicate canonical types.
	canonical, err := protoregistry.GlobalFiles.FindDescriptorByName("delidev.v1.Resource")
	if err != nil || canonical != (&pb.Resource{}).ProtoReflect().Descriptor() {
		t.Fatal("canonical registry changed", err)
	}
}
