package opencode

import (
	"context"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// The native loader treats missing/unreadable instructions as empty. Retain an
// exclusive, synchronized private file and verify it before launch mutations
// so that this native fallback cannot silently omit accepted Agent instructions.
// Retained files are never overwritten or deleted to manufacture a fresh run.
func (p *nativeAPIProfile) writeInstructions() error {
	if p.InstructionsPath == "" {
		return nil
	}
	file, err := os.OpenFile(p.InstructionsPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return sessionInvalid()
	}
	n, written := file.WriteString(p.Instructions)
	synced := file.Sync()
	closed := file.Close()
	if written != nil || n != len(p.Instructions) || synced != nil || closed != nil || security.SyncParent(p.InstructionsPath) != nil {
		return sessionUncertain()
	}
	return p.inspectInstructions()
}

func (p *nativeAPIProfile) inspectInstructions() error {
	if p.InstructionsPath == "" {
		return nil
	}
	raw, err := security.ReadPrivate(p.InstructionsPath, 256<<10)
	if err != nil || string(raw) != p.Instructions {
		return sessionProblem()
	}
	return nil
}

// Called under the original session gate. A later restoration of bytes cannot
// erase an already observed contradiction or authorize a different send.
func (s *sessionAPI) verifyInstructions(ctx context.Context, phase string) error {
	if s.apiProfile == nil {
		return nil
	}
	if err := s.apiProfile.inspectInstructions(); err != nil {
		s.problem, s.apiVerified = sessionProblem(), false
		if s.logger != nil {
			s.logger.WarnContext(ctx, "OpenCode original instructions require reconciliation", "owner_id", s.owner, "phase", phase, "code", s.problem.Code)
		}
		return s.problem
	}
	return nil
}
