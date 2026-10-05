// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type CodexPhase string

const (
	CodexDiscovery  CodexPhase = "discovery"
	CodexVersion    CodexPhase = "version"
	CodexProfile    CodexPhase = "profile"
	CodexRuntime    CodexPhase = "runtime"
	CodexLaunch     CodexPhase = "launch"
	CodexInitialize CodexPhase = "initialize"
	CodexConfirm    CodexPhase = "confirm"
	CodexLogin      CodexPhase = "login"
	CodexModels     CodexPhase = "models"
	CodexExecution  CodexPhase = "execution"
	CodexHistory    CodexPhase = "history"
	CodexCleanup    CodexPhase = "cleanup"
)

func (p CodexPhase) Valid() bool {
	switch p {
	case CodexDiscovery, CodexVersion, CodexProfile, CodexRuntime, CodexLaunch, CodexInitialize, CodexConfirm, CodexLogin, CodexModels, CodexExecution, CodexHistory, CodexCleanup:
		return true
	}
	return false
}

// CodexDiagnostic contains only bounded metadata and locally authored errors.
// Native output, executable paths and authentication presentation are excluded.
type CodexDiagnostic struct {
	DetectedVersion string     `json:"detected_version,omitempty"`
	MinimumVersion  string     `json:"minimum_version"`
	Phase           CodexPhase `json:"phase"`
	Code            Code       `json:"code"`
	Message         string     `json:"message"`
	Guidance        string     `json:"guidance"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
}

type codexFailure struct {
	diagnostic CodexDiagnostic
	problem    *Error
	cause      error
}

func (e *codexFailure) Error() string        { return e.problem.Error() }
func (e *codexFailure) Unwrap() error        { return e.problem }
func (e *codexFailure) Is(target error) bool { return errors.Is(e.cause, target) }

// WithCodexDiagnostic preserves the first native failure when outer operations
// add context. SafeError excludes arbitrary provider/filesystem error text.
func WithCodexDiagnostic(version string, phase CodexPhase, err error) error {
	if err == nil {
		return nil
	}
	var original *codexFailure
	if errors.As(err, &original) {
		return err
	}
	if !phase.Valid() {
		phase = CodexProfile
	}
	if !ValidInstallationVersion(version) {
		version = ""
	}
	safe := SafeError(err)
	diagnostic := CodexDiagnostic{DetectedVersion: version, MinimumVersion: CodexMinimumVersion, Phase: phase, Code: safe.Code}
	diagnostic.Message, diagnostic.Guidance = diagnostic.text()
	if safe.Code == Unavailable && (errors.Is(err, context.DeadlineExceeded) || safe.Cause == "timeout") {
		diagnostic.Message = strings.Replace(diagnostic.Message, "The native operation failed or timed out.", "The native operation timed out.", 1)
	}
	if ID(safe.CorrelationID).Validate() == nil {
		diagnostic.CorrelationID = safe.CorrelationID
	}
	label := version
	if label == "" {
		label = "not detected"
	}

	problem := *safe
	problem.Guidance, problem.Cause, problem.CorrelationID = diagnostic.Guidance, "", diagnostic.CorrelationID
	problem.Message = fmt.Sprintf("Codex %s failed during %s (minimum %s): %s", label, phase, CodexMinimumVersion, diagnostic.Message)
	return &codexFailure{diagnostic: diagnostic, problem: &problem, cause: err}
}

func CodexErrorDiagnostic(err error) *CodexDiagnostic {
	var failure *codexFailure
	if !errors.As(err, &failure) {
		return nil
	}
	value := failure.diagnostic
	return &value
}

func CodexVersionFailure(version string) error {
	return WithCodexDiagnostic(version, CodexVersion, Fail(Unsupported, "The installed Codex version is below the minimum or is not valid SemVer.", "Install Codex "+CodexMinimumVersion+" or newer and repeat native discovery."))
}

// text reconstructs safe explanations locally, including for Worker observations.
// Arbitrary native/provider error strings never become durable presentation.
func (d CodexDiagnostic) text() (string, string) {
	label := d.DetectedVersion
	if label == "" {
		label = "not detected"
	}
	steps := map[CodexPhase]string{CodexDiscovery: "executable discovery", CodexVersion: "version validation", CodexProfile: "profile validation", CodexRuntime: "runtime preparation", CodexLaunch: "native launch", CodexInitialize: "initialization", CodexConfirm: "initialization confirmation", CodexLogin: "sign-in", CodexModels: "model discovery", CodexExecution: "execution", CodexHistory: "history verification", CodexCleanup: "cleanup"}
	reason := map[Code]string{InvalidArgument: "The native configuration is invalid.", NotFound: "The Codex executable was not found.", Conflict: "The operation conflicts with an existing owner.", Unauthenticated: "Native authentication was not accepted.", PermissionDenied: "Native access was denied.", Unavailable: "The native operation failed or timed out.", ServerUnavailable: "The native service is unavailable.", Unsupported: "The native protocol or version is incompatible.", RecoveryRequired: "The native operation requires recovery.", Canceled: "The native operation was canceled.", Internal: "The native operation failed."}[d.Code]
	if reason == "" {
		reason = "The native operation did not complete."
	}
	if d.Phase == CodexVersion {
		reason = "Codex requires valid SemVer at or above " + CodexMinimumVersion + "."
	}
	return fmt.Sprintf("Codex %s did not complete %s. %s", label, steps[d.Phase], reason), "Check Connection & diagnostics before starting another sign-in or native operation."
}

func (d CodexDiagnostic) Validate() error {
	validCode := false
	switch d.Code {
	case InvalidArgument, NotFound, Conflict, Unauthenticated, PermissionDenied, Unavailable, ServerUnavailable, ConfirmationRequired, MissingInput, Unsupported, RecoveryRequired, BudgetReached, ResourceExhausted, CursorExpired, ProviderDisabled, Canceled, Internal:
		validCode = true
	}
	message, guidance := d.text()
	if !d.Phase.Valid() || !validCode || d.MinimumVersion != CodexMinimumVersion || (d.DetectedVersion != "" && !ValidInstallationVersion(d.DetectedVersion)) || (d.Message != message && !(d.Code == Unavailable && d.Message == strings.Replace(message, "The native operation failed or timed out.", "The native operation timed out.", 1))) || d.Guidance != guidance || (d.CorrelationID != "" && ID(d.CorrelationID).Validate() != nil) || strings.ContainsAny(d.DetectedVersion, "\r\n") {
		return Fail(InvalidArgument, "Invalid Codex diagnostic metadata.", "Retain only locally reconstructed bounded diagnostic metadata.")
	}
	return nil
}

// CodexRecoveryFailure keeps the original failure separate from a later cleanup
// failure. Recovery remains authoritative and must never authorize a resend.
func CodexRecoveryFailure(version string, phase CodexPhase, original, recovery error) error {
	if original == nil {
		return WithCodexDiagnostic(version, CodexCleanup, recovery)
	}
	first := CodexErrorDiagnostic(WithCodexDiagnostic(version, phase, original))
	problem := *SafeError(recovery)
	problem.Message = first.Message + " Owned cleanup or recovery could not be confirmed."
	problem.Guidance, problem.Cause, problem.CorrelationID = first.Guidance, "", first.CorrelationID
	return &codexFailure{diagnostic: *first, problem: &problem, cause: original}
}

func RestoreCodexDiagnostic(d CodexDiagnostic) error {
	if d.Validate() != nil {
		return WithCodexDiagnostic("", CodexProfile, Fail(Unsupported, "Invalid diagnostic metadata.", "Refresh native discovery."))
	}
	return &codexFailure{diagnostic: d, problem: Fail(d.Code, d.Message, d.Guidance)}
}
