// SPDX-License-Identifier: Apache-2.0
package contracttest

import (
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestManagedCodexRecoveryAllocations(t *testing.T) {
	if pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_RECOVERY_V1 != 72 {
		t.Fatal("managed ChatGPT recovery capability number changed")
	}
	// Neighbouring Worker capabilities keep their numbers.
	if pb.WorkerCapability_WORKER_CAPABILITY_CLAUDE_IMAGE_INPUTS_V1 != 71 || pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_SUBSCRIPTIONS_V1 != 3 {
		t.Fatal("neighbouring capability owners changed")
	}
	service := pb.File_delidev_v1_subscription_proto.Services().ByName("SubscriptionService")
	for _, method := range []struct {
		name, input, output string
	}{
		{"SyncSubscriptionRecoveryCapture", "SyncSubscriptionRecoveryCaptureRequest", "SyncSubscriptionRecoveryCaptureResponse"},
		{"ReadSubscriptionRecoveryCapture", "ReadSubscriptionRecoveryCaptureRequest", "ReadSubscriptionRecoveryCaptureResponse"},
		{"RecoverSubscription", "RecoverSubscriptionRequest", "RecoverSubscriptionResponse"},
	} {
		descriptor := service.Methods().ByName(protoreflect.Name(method.name))
		if descriptor == nil || string(descriptor.Input().Name()) != method.input || string(descriptor.Output().Name()) != method.output || descriptor.IsStreamingClient() || descriptor.IsStreamingServer() {
			t.Fatalf("%s is not a unary %s/%s operation", method.name, method.input, method.output)
		}
	}
}

func TestManagedCodexRecoveryWireNumbers(t *testing.T) {
	messages := map[protoreflect.Message]map[protoreflect.Name]protoreflect.FieldNumber{
		(&pb.SyncSubscriptionRecoveryCaptureRequest{}).ProtoReflect():  {"mutation": 1, "lease_id": 2, "machine_id": 3, "instance_id": 4, "generation_id": 5, "original": 6, "latest": 7},
		(&pb.SyncSubscriptionRecoveryCaptureResponse{}).ProtoReflect(): {"capture_id": 1, "replayed": 2},
		(&pb.ReadSubscriptionRecoveryCaptureRequest{}).ProtoReflect():  {"account_id": 1, "lease_id": 2, "machine_id": 3, "instance_id": 4, "original_instance_id": 5, "generation_id": 6},
		(&pb.ReadSubscriptionRecoveryCaptureResponse{}).ProtoReflect(): {"capture_id": 1, "original": 2, "latest": 3},
		(&pb.RecoverSubscriptionRequest{}).ProtoReflect():              {"mutation": 1, "lease_id": 2, "machine_id": 3, "instance_id": 4, "original_instance_id": 5, "generation_id": 6, "report": 7, "capture_id": 8, "settled_member_lease_ids": 9, "blocked_reason": 10},
		(&pb.RecoverSubscriptionResponse{}).ProtoReflect():             {"account": 1, "replayed": 2, "outcome": 3, "blocked_reason": 4},
	}
	for message, expected := range messages {
		fields := message.Descriptor().Fields()
		if fields.Len() != len(expected) {
			t.Fatalf("%s gained or lost fields", message.Descriptor().Name())
		}
		for name, number := range expected {
			field := fields.ByName(name)
			if field == nil || field.Number() != number {
				t.Fatalf("%s.%s is not allocated as %d", message.Descriptor().Name(), name, number)
			}
		}
	}
	reports := map[pb.SubscriptionRecoveryReport]int32{
		pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_UNSPECIFIED:                0,
		pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE:       1,
		pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL: 2,
		pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED:                    3,
	}
	for value, number := range reports {
		if int32(value) != number {
			t.Fatalf("%v is not allocated as %d", value, number)
		}
	}
	if pb.SubscriptionRecoveryReport(0).Descriptor().Values().Len() != len(reports) {
		t.Fatal("SubscriptionRecoveryReport gained or lost members")
	}
	outcomes := map[pb.SubscriptionRecoveryOutcome]int32{
		pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_UNSPECIFIED:  0,
		pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED:     1,
		pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED: 2,
		pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED:      3,
	}
	for value, number := range outcomes {
		if int32(value) != number {
			t.Fatalf("%v is not allocated as %d", value, number)
		}
	}
	if pb.SubscriptionRecoveryOutcome(0).Descriptor().Values().Len() != len(outcomes) {
		t.Fatal("SubscriptionRecoveryOutcome gained or lost members")
	}
	if pb.SubscriptionRecoveryBlockedReason(0).Descriptor().Values().Len() != 7 || pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_ACTIVE_OWNER != 6 {
		t.Fatal("SubscriptionRecoveryBlockedReason changed")
	}
}

// The original Finish contract is unchanged: older peers neither see nor send
// any recovery field on it.
func TestOriginalFinishContractIsUnchangedByRecovery(t *testing.T) {
	fields := (&pb.FinishSubscriptionRequest{}).ProtoReflect().Descriptor().Fields()
	if fields.Len() != 11 || fields.ByName("finish_scope").Number() != 11 {
		t.Fatal("FinishSubscriptionRequest changed")
	}
	response := (&pb.FinishSubscriptionResponse{}).ProtoReflect().Descriptor().Fields()
	if response.Len() != 2 {
		t.Fatal("FinishSubscriptionResponse changed")
	}
}
