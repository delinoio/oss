package domain

import (
	"encoding/json"
	"log/slog"
)

// MaxExecutionJobDocumentBytes is the serialized store limit of one immutable
// execution Job document. It is the ordinary entity bound and is never raised
// for execution assignments; callers must fit inside it instead.
const MaxExecutionJobDocumentBytes = 1 << 20

const (
	// ExecutionJobWrapperReserveBytes covers the Job wrapper fields and every
	// field the store adds to the same document after queueing: claim identity,
	// startup records, problems and timestamps.
	ExecutionJobWrapperReserveBytes = 16 << 10
	// ExecutionJobInputScalarReserveBytes covers the bounded scalar fields of
	// ExecutionJobInput other than the explicit executable path: generated
	// identities, the configuration digest, retry and capability flags.
	ExecutionJobInputScalarReserveBytes = 12 << 10
	// ExecutionExecutablePathReserveBytes covers an explicit executable path at
	// its accepted 4,096-byte maximum after JSON escaping, where each '<', '>' or
	// '&' byte expands to a six-byte escape.
	ExecutionExecutablePathReserveBytes = 4096 * 6
	// ExecutionJobInputReserveBytes is the total reserve for the bounded scalar
	// fields of ExecutionJobInput, including the startup selection.
	ExecutionJobInputReserveBytes = ExecutionJobInputScalarReserveBytes + ExecutionExecutablePathReserveBytes
	// ExecutionEscapedTextBytes is the serialized size of one accepted
	// 256-byte text field whose every byte is a '<', '>' or '&' that
	// encoding/json expands to a six-byte escape.
	ExecutionEscapedTextBytes = 256 * 6
	// ExecutionConfigurationReserveBytes covers the bounded model, provider,
	// native identity, routing, option and branch-prefix fields of the frozen
	// execution configuration that are resolved only at dispatch. It is sized
	// from a complete worst-case configuration: eight accepted 256-byte text
	// fields (native model, effort, child model, child effort, approval policy,
	// approval review model, service tier and branch prefix) at their escaped
	// maximum plus two base64-encoded source model keys that each wrap an
	// escaped native identity, with 4 KiB left for fixed identities, field names,
	// revisions and context metadata. The domain test marshals that worst case
	// and fails if the reserve stops covering it.
	ExecutionConfigurationReserveBytes = 8*ExecutionEscapedTextBytes + 2*(ExecutionEscapedTextBytes+256)*4/3 + 4<<10
)

// ExecutionJobStage identifies which enforcement point rejected an oversized
// execution Job document, because the retained ownership differs by stage.
type ExecutionJobStage string

const (
	// ExecutionJobAdmission runs before any workspace job or session write.
	ExecutionJobAdmission ExecutionJobStage = "admission"
	// ExecutionJobDispatch runs after workspace preparation succeeded and the
	// real manifest was loaded.
	ExecutionJobDispatch ExecutionJobStage = "dispatch"
)

// ExecutionJobBudget is the shared upper-bound calculation for one execution
// Job document. Admission builds it from accepted inputs before any workspace
// preparation exists; dispatch rechecks the actual serialized bytes.
type ExecutionJobBudget struct {
	// PreparationBytes is the serialized workspace preparation request.
	PreparationBytes int
	// ManifestBytes is the serialized (or worst-case) workspace manifest.
	ManifestBytes int
	// InputBytes is the serialized SessionInput. Prompt text is measured after
	// JSON escaping, never as raw bytes.
	InputBytes int
	// ConfigurationBytes is the serialized (or bounded) frozen configuration
	// including instructions and applied templates.
	ConfigurationBytes int
	// ExtraBytes covers optional embedded documents such as a PR remediation
	// target.
	ExtraBytes int
}

// InputDocumentBytes is the upper bound of the serialized ExecutionJobInput.
func (b ExecutionJobBudget) InputDocumentBytes() int {
	return b.PreparationBytes + b.ManifestBytes + b.InputBytes + b.ConfigurationBytes + b.ExtraBytes + ExecutionJobInputReserveBytes + ExecutionConfigurationReserveBytes
}

// JobDocumentBytes is the upper bound of the serialized Job document.
func (b ExecutionJobBudget) JobDocumentBytes() int {
	return ExecutionJobDocumentBytes(b.InputDocumentBytes())
}

// Require rejects a budget that cannot fit the execution Job document limit.
func (b ExecutionJobBudget) Require(logger *slog.Logger) error {
	return requireExecutionJobBytes(ExecutionJobAdmission, b.JobDocumentBytes(), logger, b)
}

// ExecutionJobDocumentBytes returns the Job document size reserved for an
// execution input of the given serialized size.
func ExecutionJobDocumentBytes(inputBytes int) int {
	return inputBytes + ExecutionJobWrapperReserveBytes
}

// RequireExecutionJobInput rechecks the actual serialized execution input
// right before atomic dispatch publication.
func RequireExecutionJobInput(raw json.RawMessage, logger *slog.Logger) error {
	return requireExecutionJobBytes(ExecutionJobDispatch, ExecutionJobDocumentBytes(len(raw)), logger, ExecutionJobBudget{})
}

func requireExecutionJobBytes(stage ExecutionJobStage, total int, logger *slog.Logger, budget ExecutionJobBudget) error {
	if total <= MaxExecutionJobDocumentBytes {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("execution_job_budget_exceeded", "stage", string(stage), "document_bytes", total, "limit_bytes", MaxExecutionJobDocumentBytes, "preparation_bytes", budget.PreparationBytes, "manifest_bytes", budget.ManifestBytes, "input_bytes", budget.InputBytes, "configuration_bytes", budget.ConfigurationBytes)
	if stage == ExecutionJobDispatch {
		return Fail(ResourceExhausted, "The workspace and input are too large for one execution assignment.", "Reduce the prompt, applied instructions or the number of project repositories; the prepared workspace remains retained, and the saved input and configuration claim was rolled back.")
	}
	return Fail(ResourceExhausted, "The workspace and input are too large for one execution assignment.", "Reduce the prompt, applied instructions or the number of project repositories; the saved input and configuration were not changed and no workspace was prepared.")
}
