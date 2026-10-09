// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestStartupImageKindRoundTripAndLegacyOmission(t *testing.T) {
	o := domain.ExecutionStartupObservation{State: domain.StartupFailed, Phase: domain.StartupInput, Harness: domain.Codex, ProblemCode: domain.Unsupported, CorrelationID: domain.NewID(), InputDelivery: domain.StartupNotSent, Cleanup: domain.StartupCleanupConfirmed, FailureKind: domain.StartupImageInputRejected}
	got, err := StartupObservation(StartupMessage(o))
	if err != nil || got != o {
		t.Fatal("image kind roundtrip changed", err)
	}
	o.FailureKind = 0
	message := StartupMessage(o)
	raw, _ := proto.Marshal(message)
	legacy := &pb.ExecutionStartupObservation{State: message.State, Phase: message.Phase, Harness: message.Harness, ProblemCode: message.ProblemCode, CorrelationId: message.CorrelationId, InputDelivery: message.InputDelivery, Cleanup: message.Cleanup}
	expected, _ := proto.Marshal(legacy)
	if !bytes.Equal(raw, expected) {
		t.Fatal("zero kind changed legacy bytes")
	}
	message.FailureKind = pb.ExecutionStartupFailureKind(99)
	if _, err := StartupObservation(message); err == nil {
		t.Fatal("unknown kind accepted")
	}
}
