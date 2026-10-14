// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// GrokPhase is the closed set of Grok Build operation phases. Values map
// one-to-one to the wire GrokDiagnosticPhase enum.
type GrokPhase string

const (
	GrokDiscovery GrokPhase = "discovery"
	GrokVersion   GrokPhase = "version"
	GrokProfile   GrokPhase = "profile"
	GrokRuntime   GrokPhase = "runtime"
	GrokLogin     GrokPhase = "login"
	GrokRefresh   GrokPhase = "refresh"
	GrokLogout    GrokPhase = "logout"
	GrokModels    GrokPhase = "models"
	GrokExecution GrokPhase = "execution"
	GrokHistory   GrokPhase = "history"
	GrokCleanup   GrokPhase = "cleanup"
)

// GrokPhases lists every phase in wire order.
var GrokPhases = []GrokPhase{GrokDiscovery, GrokVersion, GrokProfile, GrokRuntime, GrokLogin, GrokRefresh, GrokLogout, GrokModels, GrokExecution, GrokHistory, GrokCleanup}

func (p GrokPhase) Valid() bool {
	for _, phase := range GrokPhases {
		if p == phase {
			return true
		}
	}
	return false
}

// GrokDiagnostic contains only bounded metadata and locally authored text.
// Native output, executable paths, URLs, identity and credentials are excluded.
// SupportedVersion is always GrokSubscriptionBaselineVersion.
type GrokDiagnostic struct {
	DetectedVersion  string    `json:"detected_version,omitempty"`
	SupportedVersion string    `json:"supported_version,omitempty"`
	Phase            GrokPhase `json:"phase"`
	Code             Code      `json:"code"`
	Message          string    `json:"message"`
	Guidance         string    `json:"guidance"`
	CorrelationID    string    `json:"correlation_id,omitempty"`
}

type grokFailure struct {
	diagnostic GrokDiagnostic
	problem    *Error
	cause      error
}

func (e *grokFailure) Error() string        { return e.problem.Error() }
func (e *grokFailure) Unwrap() error        { return e.problem }
func (e *grokFailure) Is(target error) bool { return errors.Is(e.cause, target) }

// WithGrokDiagnostic preserves the first native failure when outer operations
// add context. SafeError excludes arbitrary provider/filesystem error text.
func WithGrokDiagnostic(version string, phase GrokPhase, err error) error {
	if err == nil {
		return nil
	}
	var original *grokFailure
	if errors.As(err, &original) {
		return err
	}
	if !phase.Valid() {
		phase = GrokProfile
	}
	if !ValidInstallationVersion(version) {
		version = ""
	}
	safe := SafeError(err)
	diagnostic := GrokDiagnostic{DetectedVersion: version, SupportedVersion: GrokSubscriptionBaselineVersion, Phase: phase, Code: safe.Code}
	diagnostic.Message, diagnostic.Guidance = diagnostic.text()
	if safe.Code == Unavailable && (errors.Is(err, context.DeadlineExceeded) || safe.Cause == "timeout") {
		diagnostic.Message = strings.Replace(diagnostic.Message, grokFailedReason, grokTimeoutReason, 1)
	}
	if ID(safe.CorrelationID).Validate() == nil {
		diagnostic.CorrelationID = safe.CorrelationID
	}
	problem := *safe
	problem.Guidance, problem.Cause, problem.CorrelationID = diagnostic.Guidance, "", diagnostic.CorrelationID
	problem.Message = diagnostic.Message
	return &grokFailure{diagnostic: diagnostic, problem: &problem, cause: err}
}

// WithGrokCleanupFailure combines an operation's result with the outcome of its
// owned-process cleanup, keeping the earliest failure authoritative:
//
//	operation result  cleanup      diagnostic            transport error
//	success           confirmed    none                  none
//	success           unconfirmed  cleanup / recovery    recovery_required
//	failed            confirmed    the operation's own   the operation's own
//	failed            unconfirmed  the operation's own   recovery_required
//
// An unconfirmed cleanup never replaces the first failure's diagnostic, so the
// actionable profile or runtime failure stays visible, but it always forces the
// transport error to recovery_required because callers must stop and reconcile
// the owned process before retrying. The returned error still matches the
// operation's own cause (for example context.DeadlineExceeded) with errors.Is.
// phase is the operation's failure phase; cleanup is nil when it was confirmed.
func WithGrokCleanupFailure(version string, phase GrokPhase, err error, cleanup *Error) error {
	if cleanup == nil {
		return WithGrokDiagnostic(version, phase, err)
	}
	if err == nil {
		return WithGrokDiagnostic(version, GrokCleanup, cleanup)
	}
	first, ok := WithGrokDiagnostic(version, phase, err).(*grokFailure)
	if !ok {
		return WithGrokDiagnostic(version, GrokCleanup, cleanup)
	}
	problem := *SafeError(cleanup)
	problem.Cause, problem.CorrelationID = "", ""
	return &grokFailure{diagnostic: first.diagnostic, problem: &problem, cause: errors.Join(first.cause, cleanup)}
}

// GrokErrorDiagnostic returns the original diagnostic carried by err, if any.
func GrokErrorDiagnostic(err error) *GrokDiagnostic {
	var failure *grokFailure
	if !errors.As(err, &failure) {
		return nil
	}
	value := failure.diagnostic
	return &value
}

// GrokVersionFailure reports a Grok Build version that cannot satisfy the
// supported baseline, or version metadata that is not valid SemVer.
func GrokVersionFailure(version string) error {
	return WithGrokDiagnostic(version, GrokVersion, Fail(Unsupported, "The native version is not supported.", "Install the supported Grok Build release and refresh protocol discovery."))
}

const (
	grokFailedReason  = "The native operation failed or timed out."
	grokTimeoutReason = "The native operation timed out."
)

// text reconstructs safe explanations locally, including for Worker observations.
// Arbitrary native/provider error strings never become durable presentation.
func (d GrokDiagnostic) text() (string, string) {
	label := d.DetectedVersion
	if label == "" {
		label = "not detected"
	}
	steps := map[GrokPhase]string{GrokDiscovery: "executable discovery", GrokVersion: "version validation", GrokProfile: "profile validation", GrokRuntime: "runtime preparation", GrokLogin: "sign-in", GrokRefresh: "credential refresh", GrokLogout: "sign-out", GrokModels: "model discovery", GrokExecution: "execution", GrokHistory: "history verification", GrokCleanup: "cleanup"}
	reason := map[Code]string{InvalidArgument: "The native configuration is invalid.", NotFound: "The Grok Build executable was not found.", Conflict: "The operation conflicts with an existing owner.", Unauthenticated: "Native authentication was not accepted.", PermissionDenied: "Native access was denied.", Unavailable: grokFailedReason, ServerUnavailable: "The native service is unavailable.", Unsupported: "The native protocol or version is incompatible.", RecoveryRequired: "The native operation requires recovery.", ResourceExhausted: "The native output exceeded its bound.", Canceled: "The native operation was canceled.", Internal: "The native operation failed."}[d.Code]
	if reason == "" {
		reason = "The native operation did not complete."
	}
	if d.Phase == GrokVersion {
		reason = "The native version metadata is invalid."
		if semver.IsValid("v" + d.DetectedVersion) {
			reason = "Grok Build requires valid SemVer at or above " + GrokSubscriptionBaselineVersion + "."
		}
	}
	return fmt.Sprintf("Grok Build %s did not complete %s. %s", label, steps[d.Phase], reason), "Check the Grok Build installation in Worker diagnostics, then refresh protocol discovery before another sign-in or native operation."
}

func (d GrokDiagnostic) Validate() error {
	validCode := false
	switch d.Code {
	case InvalidArgument, NotFound, Conflict, Unauthenticated, PermissionDenied, Unavailable, ServerUnavailable, ConfirmationRequired, MissingInput, Unsupported, RecoveryRequired, BudgetReached, ResourceExhausted, CursorExpired, ProviderDisabled, Canceled, Internal:
		validCode = true
	}
	message, guidance := d.text()
	timeout := d.Code == Unavailable && d.Message == strings.Replace(message, grokFailedReason, grokTimeoutReason, 1)
	if !d.Phase.Valid() || !validCode || d.SupportedVersion != GrokSubscriptionBaselineVersion || (d.DetectedVersion != "" && !ValidInstallationVersion(d.DetectedVersion)) || (d.Message != message && !timeout) || d.Guidance != guidance || (d.CorrelationID != "" && ID(d.CorrelationID).Validate() != nil) || strings.ContainsAny(d.DetectedVersion, "\r\n") {
		return Fail(InvalidArgument, "Invalid Grok diagnostic metadata.", "Retain only locally reconstructed bounded diagnostic metadata.")
	}
	return nil
}

// RestoreGrokDiagnostic rebuilds an error from stored validated metadata.
func RestoreGrokDiagnostic(d GrokDiagnostic) error {
	if d.Validate() != nil {
		return WithGrokDiagnostic("", GrokProfile, Fail(Unsupported, "Invalid diagnostic metadata.", "Refresh native discovery."))
	}
	return &grokFailure{diagnostic: d, problem: Fail(d.Code, d.Message, d.Guidance)}
}
