// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// The pinned OIDC handshake pushes presentation settings and announcements even
// when its private settings responder returns no configuration. These frames do
// not identify an account, select a model, authorize a tool or configure DeliDev.
// Validate their envelope and bounds, then discard all native presentation text.
// API connections retain their independently validated notification vocabulary.
func (a *apiConnection) consumeManagedPresentation(event nativewire.Event) (bool, error) {
	if a.profile.authentication != managedAuthentication {
		return false, nil
	}
	if event.Method != "_x.ai/settings/update" && event.Method != "_x.ai/announcements/update" {
		return false, nil
	}
	if event.Kind != nativewire.Notification || event.EmittedAtMS != nil || len(event.Params) > 32<<10 || a.presentationCount >= 64 || a.presentationBytes+len(event.Params) > 256<<10 {
		return true, incompatible()
	}
	a.presentationCount++
	a.presentationBytes += len(event.Params)
	if event.Method == "_x.ai/settings/update" {
		return true, validateManagedSettings(event.Params)
	}
	var value struct {
		Generation uint64            `json:"gen"`
		Items      []json.RawMessage `json:"announcements"`
	}
	if decode(event.Params, &value) != nil || value.Generation == 0 || value.Generation <= a.announcementGeneration || len(value.Items) > 32 {
		return true, incompatible()
	}
	for _, item := range value.Items {
		var object map[string]json.RawMessage
		if json.Unmarshal(item, &object) != nil || object == nil || !boundedPresentation(item, 0) {
			return true, incompatible()
		}
	}
	a.announcementGeneration = value.Generation
	return true, nil
}

func validateManagedSettings(raw []byte) error {
	var values map[string]json.RawMessage
	if decode(raw, &values) != nil || len(values) < 23 || len(values) > 24 {
		return incompatible()
	}
	booleans := []string{"show_resolved_model", "sharing_enabled", "privacy_notice_rollout", "session_picker_grouped", "auto_permission_mode_enabled", "prompt_suggestions_enabled", "group_tool_verbs", "collapsed_edit_blocks", "dock_enabled", "terminal_theme_enabled"}
	for _, key := range booleans {
		value, exists := values[key]
		var flag bool
		if !exists || !isNull(value) && decode(value, &flag) != nil {
			return incompatible()
		}
		delete(values, key)
	}
	for _, key := range []string{"privacy_banner_reshow_days", "subscription_watch_interval_secs"} {
		value, exists := values[key]
		var number uint64
		if !exists || !isNull(value) && (decode(value, &number) != nil || number > 1_000_000_000) {
			return incompatible()
		}
		delete(values, key)
	}
	// A native gate or denied access is never bypassed by a successful cached
	// authentication acknowledgment. Its URL/message is not a product diagnostic.
	for _, key := range []string{"gate_message", "gate_url", "gate_label", "consent_gate"} {
		if value, exists := values[key]; !exists || !isNull(value) {
			return incompatible()
		}
		delete(values, key)
	}
	access, exists := values["allow_access"]
	var allowed bool
	if !exists || !isNull(access) && (decode(access, &allowed) != nil || !allowed) {
		return incompatible()
	}
	delete(values, "allow_access")
	for _, key := range []string{"subscription_tier_display", "permission_mode"} {
		value, exists := values[key]
		var descriptor string
		if !exists || !isNull(value) && (decode(value, &descriptor) != nil || !text(descriptor, 256)) {
			return incompatible()
		}
		delete(values, key)
	}
	for _, key := range []string{"tips", "announcements", "campaigns"} {
		value, exists := values[key]
		var items []json.RawMessage
		if !exists || !isNull(value) && (json.Unmarshal(value, &items) != nil || items == nil || len(items) > 32 || !boundedPresentation(value, 0)) {
			return incompatible()
		}
		delete(values, key)
	}
	value, exists := values["slash_command_tags"]
	var tags map[string]string
	if !exists || !isNull(value) && (json.Unmarshal(value, &tags) != nil || tags == nil || len(tags) > 32 || !boundedPresentation(value, 0)) {
		return incompatible()
	}
	delete(values, "slash_command_tags")
	if inheritance, exists := values["subagent_model_inheritance_enabled"]; exists {
		var flag bool
		if !isNull(inheritance) && decode(inheritance, &flag) != nil {
			return incompatible()
		}
		delete(values, "subagent_model_inheritance_enabled")
	}
	if len(values) != 0 {
		return incompatible()
	}
	return nil
}

// Opaque presentation descendants have deliberately no execution interpretation.
// Bounding their depth, strings and collection sizes prevents a discarded native
// banner from consuming unbounded resources; nothing is rendered or persisted.
func boundedPresentation(raw []byte, depth int) bool {
	if depth > 4 || len(raw) > 16<<10 || !utf8.Valid(raw) {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value := value.(type) {
	case string:
		return len(value) <= 4096
	case []any:
		if len(value) > 32 {
			return false
		}
		for _, child := range value {
			encoded, _ := json.Marshal(child)
			if !boundedPresentation(encoded, depth+1) {
				return false
			}
		}
	case map[string]any:
		if len(value) > 32 {
			return false
		}
		for key, child := range value {
			encoded, _ := json.Marshal(child)
			if !text(key, 128) || !boundedPresentation(encoded, depth+1) {
				return false
			}
		}
	}
	return true
}
