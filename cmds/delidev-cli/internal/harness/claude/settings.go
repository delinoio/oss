package claude

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// AppliedSettings is the native applied selection, not the raw settings cascade
// or a copy of the requested flags. Nil effort means the native response
// explicitly reported no effort; it cannot satisfy an explicit selection.
type AppliedSettings struct {
	Model  string        `json:"model"`
	Effort *NativeEffort `json:"effort"`
}

func settingsUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Claude Code did not apply the selected native settings.", "Retain the original selection and reconcile its native configuration before sending input.")
}

type appliedSettingsWire struct {
	Model     *string         `json:"model"`
	Effort    json.RawMessage `json:"effort"`
	Advisor   json.RawMessage `json:"advisor"`
	Ultracode *bool           `json:"ultracode"`
}

func (s *appliedSettingsWire) UnmarshalJSON(raw []byte) error {
	type plain appliedSettingsWire
	var value plain
	if decodeNativeObject(raw, &value) != nil {
		return incompatible()
	}
	*s = appliedSettingsWire(value)
	return nil
}

func decodeAppliedSettings(raw []byte, model string, effort NativeEffort) (AppliedSettings, error) {
	var value struct {
		Effective map[string]json.RawMessage `json:"effective"`
		Sources   []json.RawMessage          `json:"sources"`
		Applied   *appliedSettingsWire       `json:"applied"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Applied == nil || value.Applied.Model == nil || domain.Text(*value.Applied.Model, "native applied model", 256, true) != nil || value.Applied.Ultracode == nil || len(value.Applied.Effort) == 0 || len(value.Applied.Advisor) == 0 || value.Effective == nil || value.Sources == nil {
		return AppliedSettings{}, incompatible()
	}
	// The initial API profile has no settings files or additional model-driven
	// orchestration. A raw cascade is not proof of applied selection, and an
	// unexpected policy/source must be reconciled rather than silently ignored.
	if len(value.Effective) != 0 || len(value.Sources) != 0 || *value.Applied.Ultracode || !bytes.Equal(bytes.TrimSpace(value.Applied.Advisor), []byte("null")) {
		return AppliedSettings{}, settingsUncertain()
	}
	var observed *NativeEffort
	if json.Unmarshal(value.Applied.Effort, &observed) != nil || (observed != nil && !validNativeEffort(*observed, false)) {
		return AppliedSettings{}, incompatible()
	}
	if *value.Applied.Model != model || (effort != "" && (observed == nil || *observed != effort)) {
		return AppliedSettings{}, settingsUncertain()
	}
	return AppliedSettings{Model: *value.Applied.Model, Effort: observed}, nil
}

func validNativeEffort(effort NativeEffort, allowDefault bool) bool {
	return (allowDefault && effort == "") || domain.Text(string(effort), "native effort", 256, true) == nil
}

// ReadAppliedSettings performs one exact correlated native read and never
// changes settings, sends input, retries, or grants permission/account authority.
// The caller still owns lifecycle serialization and must recheck before a send
// if a native configuration-changing operation occurred after this observation.
func (s *Stream) ReadAppliedSettings(ctx context.Context, request domain.ID, model string, effort NativeEffort) (settings AppliedSettings, returned error) {
	if request.Validate() != nil || domain.Text(model, "native model", 256, true) != nil || !validNativeEffort(effort, true) {
		return AppliedSettings{}, apiConfigurationError()
	}
	defer func() {
		if returned != nil {
			s.logger.WarnContext(ctx, "Claude Code applied settings validation failed", "owner_id", s.owner, "code", domain.SafeError(returned).Code)
		} else {
			s.logger.DebugContext(ctx, "Claude Code applied settings verified", "owner_id", s.owner, "effort_observed", settings.Effort != nil)
		}
	}()
	response, err := s.Call(ctx, request, map[string]any{"subtype": "get_settings"})
	if err != nil {
		return AppliedSettings{}, err
	}
	if response.Failed {
		return AppliedSettings{}, settingsUncertain()
	}
	return decodeAppliedSettings(response.Result, model, effort)
}

// InitialAppliedSettings returns an immutable historical observation from API
// startup. It is not a live readiness check or permission to send another input.
func (s *Stream) InitialAppliedSettings() (AppliedSettings, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialApplied == nil {
		return AppliedSettings{}, false
	}
	value := *s.initialApplied
	if value.Effort != nil {
		effort := *value.Effort
		value.Effort = &effort
	}
	return value, true
}
