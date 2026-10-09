// SPDX-License-Identifier: Apache-2.0
package contracttest

import (
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"testing"
)

func TestCurrentDescriptorsHaveNoLegacyAggregate(t *testing.T) {
	if _, e := protoregistry.GlobalFiles.FindFileByPath("delidev/v1/delidev.proto"); e == nil {
		t.Fatal("retired aggregate is registered")
	}
	files := []protoreflect.FileDescriptor{pb.File_delidev_v1_common_proto, pb.File_delidev_v1_system_proto, pb.File_delidev_v1_provider_proto, pb.File_delidev_v1_configuration_proto, pb.File_delidev_v1_usage_proto, pb.File_delidev_v1_worker_proto}
	for _, file := range files {
		restored, e := protodesc.NewFile(protodesc.ToFileDescriptorProto(file), protoregistry.GlobalFiles)
		if e != nil || restored.Path() != file.Path() {
			t.Fatal("current canonical descriptor failed", e)
		}
		for i := 0; i < file.Messages().Len(); i++ {
			message := file.Messages().Get(i)
			registered, e := protoregistry.GlobalTypes.FindMessageByName(message.FullName())
			if e != nil || registered.Descriptor() != message {
				t.Fatal("canonical registration changed", e)
			}
		}
	}
	if (&pb.Resource{}).ProtoReflect().Descriptor().Fields().ByName("id").Number() != 1 {
		t.Fatal("resource wire allocation changed")
	}
	if pb.SystemCapability_SYSTEM_CAPABILITY_INLINE_WORKER_MODELS_V1 != 42 || pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1 != 22 || pb.SystemCapability_SYSTEM_CAPABILITY_EXECUTION_STARTUP_V1 != 43 || pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1 != 23 {
		t.Fatal("separate original capability owners changed")
	}
}
