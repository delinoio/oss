package rpc

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func StartupObservation(v *pb.ExecutionStartupObservation) (domain.ExecutionStartupObservation, error) {
	if v == nil {
		return domain.ExecutionStartupObservation{}, domain.StartupRejectionUncertain()
	}
	o := domain.ExecutionStartupObservation{FailureKind: domain.ExecutionStartupFailureKind(v.FailureKind), State: domain.ExecutionStartupState(v.State), Phase: domain.ExecutionStartupPhase(v.Phase), Harness: domain.Harness(v.Harness), NativeVersion: v.NativeVersion, ExecutableSHA256: v.ExecutableSha256, Protocol: domain.NativeProtocol(v.Protocol), ProblemCode: domain.Code(v.ProblemCode), CorrelationID: domain.ID(v.CorrelationId), InputDelivery: domain.ExecutionStartupInputDelivery(v.InputDelivery), Cleanup: domain.ExecutionStartupCleanup(v.Cleanup)}
	return o, o.Validate()
}

func StartupMessage(o domain.ExecutionStartupObservation) *pb.ExecutionStartupObservation {
	return &pb.ExecutionStartupObservation{FailureKind: pb.ExecutionStartupFailureKind(o.FailureKind), State: pb.ExecutionStartupState(o.State), Phase: pb.ExecutionStartupPhase(o.Phase), Harness: string(o.Harness), NativeVersion: o.NativeVersion, ExecutableSha256: o.ExecutableSHA256, Protocol: string(o.Protocol), ProblemCode: string(o.ProblemCode), CorrelationId: string(o.CorrelationID), InputDelivery: pb.ExecutionStartupInputDelivery(o.InputDelivery), Cleanup: pb.ExecutionStartupCleanup(o.Cleanup)}
}
