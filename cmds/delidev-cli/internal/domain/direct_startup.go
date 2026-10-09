package domain

import "slices"

const ExecutionStartupV1 WorkerCapability = "execution-startup-v1"

// The selection is immutable. Runtime observations never replace an account,
// Worker or history source, and never claim installation or protocol readiness.
type ExecutionStartupSelection struct {
	Harness          Harness `json:"harness"`
	ExplicitPath     string  `json:"explicit_path,omitempty"`
	ExecutableSHA256 string  `json:"executable_sha256,omitempty"`
}

func (s ExecutionStartupSelection) Validate(h Harness) error {
	if s.Harness != h || ProtocolFor(h) == "" || Text(s.ExplicitPath, "selected executable", 4096, false) != nil || (s.ExecutableSHA256 != "" && !lowerDigest(s.ExecutableSHA256)) {
		return Fail(InvalidArgument, "Invalid execution startup selection.", "Keep the original harness and executable selection.")
	}
	return nil
}

type ExecutionStartupState int32
type ExecutionStartupPhase int32
type ExecutionStartupInputDelivery int32
type ExecutionStartupCleanup int32

const (
	StartupReady             ExecutionStartupState         = 1
	StartupFailed            ExecutionStartupState         = 2
	StartupUncertain         ExecutionStartupState         = 3
	StartupResolve           ExecutionStartupPhase         = 1
	StartupLaunch            ExecutionStartupPhase         = 2
	StartupInitialize        ExecutionStartupPhase         = 3
	StartupSettings          ExecutionStartupPhase         = 4
	StartupInput             ExecutionStartupPhase         = 5
	StartupExecution         ExecutionStartupPhase         = 6
	StartupCleanupPhase      ExecutionStartupPhase         = 7
	StartupNotSent           ExecutionStartupInputDelivery = 1
	StartupClaimed           ExecutionStartupInputDelivery = 2
	StartupAcknowledged      ExecutionStartupInputDelivery = 3
	StartupDeliveryUncertain ExecutionStartupInputDelivery = 4
	StartupCleanupConfirmed  ExecutionStartupCleanup       = 1
	StartupCleanupUncertain  ExecutionStartupCleanup       = 2
)

type ExecutionStartupFailureKind uint32

const (
	StartupFailureUnspecified ExecutionStartupFailureKind = 0
	StartupImageInputRejected ExecutionStartupFailureKind = 1
)

type ExecutionStartupObservation struct {
	NativeDefaults   *NativeHarnessDefaults        `json:"native_defaults,omitempty"`
	FailureKind      ExecutionStartupFailureKind   `json:"failure_kind,omitempty"`
	State            ExecutionStartupState         `json:"state"`
	Phase            ExecutionStartupPhase         `json:"phase"`
	Harness          Harness                       `json:"harness"`
	NativeVersion    string                        `json:"native_version,omitempty"`
	ExecutableSHA256 string                        `json:"executable_sha256,omitempty"`
	Protocol         NativeProtocol                `json:"protocol,omitempty"`
	ProblemCode      Code                          `json:"problem_code,omitempty"`
	CorrelationID    ID                            `json:"correlation_id"`
	InputDelivery    ExecutionStartupInputDelivery `json:"input_delivery"`
	Cleanup          ExecutionStartupCleanup       `json:"cleanup,omitempty"`
}

// Metadata accepts bounded identifiers only, never diagnostic text, URLs,
// paths, whitespace or credentials. An absent version stays absent.
func ValidNativeVersionMetadata(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '-' || c == '+' || c == '_') {
			return false
		}
	}
	return true
}

func (o ExecutionStartupObservation) Validate() error {
	if o.NativeDefaults != nil && (o.State != StartupReady || o.Harness != Codex || o.NativeDefaults.Validate() != nil) {
		return StartupRejectionUncertain()
	}
	if o.FailureKind != StartupFailureUnspecified && (o.FailureKind != StartupImageInputRejected || o.State != StartupFailed || o.Phase != StartupInput || o.Harness != Codex || o.ProblemCode != Unsupported || o.InputDelivery != StartupNotSent || o.Cleanup != StartupCleanupConfirmed) {
		return StartupRejectionUncertain()
	}

	if o.CorrelationID.Validate() != nil || o.Phase < StartupResolve || o.Phase > StartupCleanupPhase || o.InputDelivery < StartupNotSent || o.InputDelivery > StartupDeliveryUncertain || ProtocolFor(o.Harness) == "" || o.NativeVersion != "" && !ValidNativeVersionMetadata(o.NativeVersion) || o.ExecutableSHA256 != "" && !lowerDigest(o.ExecutableSHA256) || o.Protocol != "" && o.Protocol != ProtocolFor(o.Harness) {
		return StartupRejectionUncertain()
	}
	if o.State == StartupReady {
		if o.Phase != StartupSettings || o.ProblemCode != "" || o.Cleanup != 0 || o.InputDelivery != StartupNotSent || o.ExecutableSHA256 == "" || o.Protocol != ProtocolFor(o.Harness) {
			return StartupRejectionUncertain()
		}
		return nil
	}
	if !slices.Contains([]ExecutionStartupState{StartupFailed, StartupUncertain}, o.State) || !slices.Contains([]Code{NotFound, PermissionDenied, Unsupported, InvalidArgument, Unavailable, Canceled, Conflict, RecoveryRequired, ResourceExhausted, ProviderDisabled}, o.ProblemCode) || !slices.Contains([]ExecutionStartupCleanup{StartupCleanupConfirmed, StartupCleanupUncertain}, o.Cleanup) || o.State == StartupFailed && (o.InputDelivery != StartupNotSent || o.Cleanup != StartupCleanupConfirmed) {
		return StartupRejectionUncertain()
	}
	return nil
}

type ExecutionStartupRecord struct {
	JobID       ID                           `json:"job_id"`
	ExecutionID ID                           `json:"execution_id"`
	Ready       *ExecutionStartupObservation `json:"ready,omitempty"`
	Failure     *ExecutionStartupObservation `json:"failure,omitempty"`
}

// Retry names only a settled original attempt. It carries no new selection.
type ExecutionStartupRetry struct {
	JobID       ID `json:"job_id"`
	ExecutionID ID `json:"execution_id"`
	InputID     ID `json:"input_id"`
}
