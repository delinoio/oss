package apiproxy

import (
	"encoding/json"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func diagnosticProviderRequestID(header http.Header, protocol domain.APIProtocol, guard secretGuard) string {
	name := "X-Request-Id"
	if protocol == domain.AnthropicMessages {
		name = "Request-Id"
	}
	values := header.Values(name)
	if len(values) == 1 && domain.SafeDiagnosticID(values[0]) && !guard.contains(values[0]) {
		return values[0]
	}
	return ""
}

func diagnosticOperation(operation Operation) domain.RequestDiagnosticOperation {
	switch operation {
	case ChatCompletion:
		return domain.DiagnosticChat
	case ResponseCreate:
		return domain.DiagnosticResponse
	case ResponseCompact:
		return domain.DiagnosticCompact
	case MessageCreate:
		return domain.DiagnosticMessage
	case MessageCountTokens:
		return domain.DiagnosticCount
	}
	return 0
}

func diagnosticString(raw json.RawMessage, filter func(string) *string, guard secretGuard) *string {
	var value string
	if json.Unmarshal(raw, &value) != nil || guard.contains(value) {
		return nil
	}
	return filter(value)
}

func diagnosticRequestSettings(raw []byte, operation Operation, value *domain.RequestDiagnostic, guard secretGuard) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return
	}
	value.RequestedServiceTier = diagnosticString(object["service_tier"], domain.DiagnosticServiceTier, guard)
	if operation == ResponseCreate || operation == ResponseCompact {
		var reasoning map[string]json.RawMessage
		if json.Unmarshal(object["reasoning"], &reasoning) == nil {
			value.RequestedEffort = diagnosticString(reasoning["effort"], domain.DiagnosticEffort, guard)
		}
	} else if operation == ChatCompletion {
		value.RequestedEffort = diagnosticString(object["reasoning_effort"], domain.DiagnosticEffort, guard)
	} else if operation == MessageCreate {
		var config map[string]json.RawMessage
		if json.Unmarshal(object["output_config"], &config) == nil {
			value.RequestedEffort = diagnosticString(config["effort"], domain.DiagnosticEffort, guard)
		}
	}
}

// Stream observations must agree for the lifetime of this exact HTTP request.
// Once identities or settings conflict, later frames cannot restore certainty.
type diagnosticObservations struct {
	value                                          *domain.RequestDiagnostic
	identityConflict, effortConflict, tierConflict bool
}

func diagnosticResponse(object map[string]json.RawMessage, observations *diagnosticObservations, guard secretGuard) {
	value := observations.value
	var id string
	if json.Unmarshal(object["id"], &id) == nil && domain.SafeDiagnosticID(id) && !guard.contains(id) {
		if !observations.identityConflict && value.NativeResponseID == "" {
			value.NativeResponseID = id
		} else if value.NativeResponseID != id {
			value.NativeResponseID = ""
			value.EffectiveEffort, value.EffectiveServiceTier = nil, nil
			observations.identityConflict = true
		}
	}
	if observations.identityConflict {
		return
	}
	tier := object["service_tier"]
	if value.Operation == domain.DiagnosticMessage {
		var usage map[string]json.RawMessage
		if json.Unmarshal(object["usage"], &usage) == nil {
			tier = usage["service_tier"]
		} else {
			tier = nil
		}
	}
	diagnosticSetting(tier, &value.EffectiveServiceTier, &observations.tierConflict, domain.DiagnosticServiceTier, guard)
	var reasoning map[string]json.RawMessage
	if json.Unmarshal(object["reasoning"], &reasoning) == nil {
		diagnosticSetting(reasoning["effort"], &value.EffectiveEffort, &observations.effortConflict, domain.DiagnosticEffort, guard)
	}
}

func diagnosticSetting(raw json.RawMessage, target **string, conflict *bool, filter func(string) *string, guard secretGuard) {
	if len(raw) == 0 || *conflict {
		return
	}
	observed := diagnosticString(raw, filter, guard)
	if observed == nil || *target != nil && **target != *observed {
		*target = nil
		*conflict = true
		return
	}
	*target = observed
}
