// SPDX-License-Identifier: Apache-2.0
package contracttest

import (
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"testing"
)

func TestGrokReservationNumbersMatchAllocationLedger(t *testing.T) {
	if pb.SystemCapability_SYSTEM_CAPABILITY_GROK_SUBSCRIPTION_LOGIN_V1 != 39 || pb.SystemCapability_SYSTEM_CAPABILITY_GROK_SUBSCRIPTION_EXECUTION_V1 != 40 || pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_GROK_SUBSCRIPTIONS_V1 != 21 {
		t.Fatal("Grok capability reservation numbers changed")
	}
	// Neighbouring owners keep their numbers: Claude 38, failed cleanup 41, Worker Claude 20 and inline models 22.
	if pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SUBSCRIPTIONS_V1 != 38 || pb.SystemCapability_SYSTEM_CAPABILITY_FAILED_SUBSCRIPTION_CLEANUP_V1 != 41 || pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CLAUDE_SUBSCRIPTIONS_V1 != 20 || pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1 != 22 {
		t.Fatal("neighbouring capability owners changed")
	}
	progress := (&pb.GetSubscriptionProgressResponse{}).ProtoReflect().Descriptor().Fields()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{"diagnostic": 7, "login_method": 8, "native_diagnostic": 9, "grok_diagnostic": 10} {
		field := progress.ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("progress field %s is not allocated as %d", name, number)
		}
	}
	if progress.ByName("grok_diagnostic").Message().FullName() != (&pb.GrokDiagnostic{}).ProtoReflect().Descriptor().FullName() {
		t.Fatal("grok_diagnostic has the wrong message type")
	}
	diagnostic := (&pb.GrokDiagnostic{}).ProtoReflect().Descriptor().Fields()
	expected := map[protoreflect.Name]protoreflect.FieldNumber{"detected_version": 1, "supported_version": 2, "phase": 3, "code": 4, "message": 5, "guidance": 6, "correlation_id": 7}
	if diagnostic.Len() != len(expected) {
		t.Fatalf("GrokDiagnostic has %d fields", diagnostic.Len())
	}
	for name, number := range expected {
		field := diagnostic.ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("GrokDiagnostic field %s is not allocated as %d", name, number)
		}
	}
	phases := map[pb.GrokDiagnosticPhase]int32{
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_UNSPECIFIED: 0,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_DISCOVERY:   1,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_VERSION:     2,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_PROFILE:     3,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_RUNTIME:     4,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN:       5,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH:     6,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT:      7,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_MODELS:      8,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_EXECUTION:   9,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_HISTORY:     10,
		pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_CLEANUP:     11,
	}
	for phase, number := range phases {
		if int32(phase) != number {
			t.Fatalf("%v is not allocated as %d", phase, number)
		}
	}
	if pb.GrokDiagnosticPhase(0).Descriptor().Values().Len() != len(phases) {
		t.Fatal("GrokDiagnosticPhase gained or lost members")
	}
}

func TestPreviousProgressEncodingDecodesWithoutGrokDiagnostic(t *testing.T) {
	// Encode with the previous schema's field set only, written by hand so this
	// stays independent of the new field.
	var wire []byte
	wire = protowire.AppendTag(wire, 1, protowire.BytesType)
	wire = protowire.AppendString(wire, "https://example.invalid/login")
	wire = protowire.AppendTag(wire, 4, protowire.VarintType)
	wire = protowire.AppendVarint(wire, uint64(pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING))
	diagnostic, e := proto.Marshal(&pb.CodexDiagnostic{Code: "codex-code", CorrelationId: "correlation"})
	if e != nil {
		t.Fatal(e)
	}
	wire = protowire.AppendTag(wire, 7, protowire.BytesType)
	wire = protowire.AppendBytes(wire, diagnostic)
	var decoded pb.GetSubscriptionProgressResponse
	if e := proto.Unmarshal(wire, &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded.GrokDiagnostic != nil || decoded.NativeDiagnostic != nil || decoded.Diagnostic.GetCode() != "codex-code" || decoded.Url != "https://example.invalid/login" {
		t.Fatal("previous encoding changed meaning", &decoded)
	}
	reencoded, e := proto.Marshal(&decoded)
	if e != nil {
		t.Fatal(e)
	}
	var again pb.GetSubscriptionProgressResponse
	if e := proto.Unmarshal(reencoded, &again); e != nil || again.GrokDiagnostic != nil || again.Diagnostic.GetCode() != "codex-code" {
		t.Fatal("round trip populated grok_diagnostic", e)
	}
}
