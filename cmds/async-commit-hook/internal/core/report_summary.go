package core

// Keep only a bounded summary prefix while the report parser validates the entire
// report. Redact complete extracted fields before truncating them, so a secret
// crossing the display boundary cannot leak a retained prefix.
type reportFailures struct {
	failures  []Failure
	secrets   []string
	used      int
	truncated bool
	full      bool
}

func redactFailure(f *Failure, secrets []string) (truncated bool) {
	for _, field := range []*string{&f.Message, &f.File, &f.Test, &f.Command} {
		text, cut := redactedFailureText(*field, secrets)
		*field = text
		truncated = truncated || cut
	}
	return
}

func (s *reportFailures) add(f Failure) {
	if s.full {
		return
	}
	s.truncated = redactFailure(&f, s.secrets) || s.truncated
	bounded, used, cut := boundFailures([]Failure{f}, runFailureBytes-s.used)
	s.truncated = s.truncated || cut
	if len(bounded) == 0 {
		s.full = true
		return
	}
	s.failures = append(s.failures, bounded[0])
	s.used += used
}

func parseReportSummaries(kind ReportKind, b []byte, check, command, logID string, secrets []string) ([]Failure, bool, error) {
	if kind == JUnit {
		return parseJUnitSummaries(b, check, command, logID, secrets)
	}
	if kind == GoTest {
		return parseGoTestSummaries(b, check, command, logID, secrets)
	}
	failures, err := ParseReport(kind, b, check, command, logID)
	return failures, false, err
}
