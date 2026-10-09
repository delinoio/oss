package runmoor

// RunnerQuarantineCause is private ownership state. Public problems can use the
// same code for absence and identity conflicts, so their text is not authority.
type RunnerQuarantineCause string

const (
	QuarantineUnknown            RunnerQuarantineCause = "unknown"
	QuarantineRegistrationAbsent RunnerQuarantineCause = "registration_absent"
	QuarantineIdentityConflict   RunnerQuarantineCause = "identity_conflict"
)

func runnerQuarantineCause(s Snapshot, id string) RunnerQuarantineCause {
	switch cause := s.RunnerQuarantines[id]; cause {
	case QuarantineRegistrationAbsent, QuarantineIdentityConflict:
		return cause
	default:
		return QuarantineUnknown
	}
}

func recordRunnerQuarantine(s *Snapshot, id string, cause RunnerQuarantineCause) {
	if s.RunnerQuarantines == nil {
		s.RunnerQuarantines = map[string]RunnerQuarantineCause{}
	}
	// A later uncertain error cannot downgrade an established identity conflict.
	if r := s.Runners[id]; r != nil && r.Phase == Quarantined && runnerQuarantineCause(*s, id) == QuarantineIdentityConflict {
		cause = QuarantineIdentityConflict
	}
	s.RunnerQuarantines[id] = cause
}

func initializeRunnerQuarantines(s *Snapshot) {
	for id, r := range s.Runners {
		if r.Phase == Quarantined {
			recordRunnerQuarantine(s, id, runnerQuarantineCause(*s, id))
		}
	}
}
