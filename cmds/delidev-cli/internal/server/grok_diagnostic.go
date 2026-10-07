// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func grokDiagnosticMessage(d *domain.GrokDiagnostic) *pb.GrokDiagnostic {
	if d == nil || d.Validate() != nil {
		return nil
	}
	phases := map[domain.GrokPhase]pb.GrokDiagnosticPhase{
		domain.GrokDiscovery: pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_DISCOVERY,
		domain.GrokVersion:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_VERSION,
		domain.GrokProfile:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_PROFILE,
		domain.GrokRuntime:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_RUNTIME,
		domain.GrokLogin:     pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN,
		domain.GrokRefresh:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH,
		domain.GrokLogout:    pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT,
		domain.GrokModels:    pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_MODELS,
		domain.GrokExecution: pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_EXECUTION,
		domain.GrokHistory:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_HISTORY,
		domain.GrokCleanup:   pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_CLEANUP,
	}
	return &pb.GrokDiagnostic{DetectedVersion: d.DetectedVersion, SupportedVersion: d.SupportedVersion, Phase: phases[d.Phase], Code: string(d.Code), Message: d.Message, Guidance: d.Guidance, CorrelationId: d.CorrelationID}
}
