package opencode

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type sessionModel struct {
	ID       string `json:"id"`
	Provider string `json:"providerID"`
}

type inputModel struct {
	Provider string `json:"providerID"`
	Model    string `json:"modelID"`
}

type inputPart struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

type sessionMarker struct {
	Request domain.ID `json:"request_id"`
}

type sessionMetadata struct {
	DeliDev sessionMarker `json:"delidev"`
}

func shape(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	fields, err := object(raw)
	if err != nil {
		return nil, sessionProblem()
	}
	for key, value := range fields {
		if string(value) == "null" || !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return nil, sessionProblem()
		}
	}
	for _, key := range required {
		if _, present := fields[key]; !present {
			return nil, sessionProblem()
		}
	}
	return fields, nil
}

func boundedString(raw json.RawMessage, max int, required bool) (string, bool) {
	var text *string
	if json.Unmarshal(raw, &text) != nil || text == nil || domain.Text(*text, "native field", max, required) != nil {
		return "", false
	}
	return *text, true
}

func nativeCount(raw json.RawMessage) (int64, bool) {
	count, err := strconv.ParseInt(string(raw), 10, 64)
	return count, err == nil && count >= 0 && count <= 9007199254740991
}

func validateCounters(raw json.RawMessage) bool {
	fields, err := shape(raw, []string{"input", "output", "reasoning", "cache"}, nil)
	if err != nil {
		return false
	}
	for _, key := range []string{"input", "output", "reasoning"} {
		if _, ok := nativeCount(fields[key]); !ok {
			return false
		}
	}
	cache, err := shape(fields["cache"], []string{"read", "write"}, nil)
	if err != nil {
		return false
	}
	for _, key := range []string{"read", "write"} {
		if _, ok := nativeCount(cache[key]); !ok {
			return false
		}
	}
	return true
}

func nonnegativeDecimal(raw json.RawMessage) bool {
	return domain.NativeNonnegativeDecimal(string(raw))
}

func validateDiffs(raw json.RawMessage) bool {
	var diffs []json.RawMessage
	if json.Unmarshal(raw, &diffs) != nil || diffs == nil || len(diffs) > 4096 {
		return false
	}
	for _, diff := range diffs {
		fields, err := shape(diff, []string{"additions", "deletions"}, []string{"file", "patch", "status"})
		if err != nil {
			return false
		}
		for _, key := range []string{"additions", "deletions"} {
			if _, ok := nativeCount(fields[key]); !ok {
				return false
			}
		}
		for _, key := range []string{"file", "patch"} {
			if value, exists := fields[key]; exists {
				if _, ok := boundedString(value, maxHTTPBody, false); !ok {
					return false
				}
			}
		}
		if value, exists := fields["status"]; exists && !scalar(value, "added") && !scalar(value, "deleted") && !scalar(value, "modified") {
			return false
		}
	}
	return true
}

func validateSession(raw []byte, cwd string, creation *sessionCreation, fresh bool) (sessionIdentity, error) {
	bad := func() (sessionIdentity, error) { return sessionIdentity{}, sessionProblem() }
	fields, err := shape(raw, []string{"id", "slug", "projectID", "directory", "path", "cost", "tokens", "title", "agent", "model", "version", "metadata", "time", "permission"}, []string{"summary"})
	if err != nil || !scalar(fields["directory"], cwd) || !scalar(fields["agent"], string(creation.settings.Agent)) || !scalar(fields["version"], SupportedVersion) || !nonnegativeDecimal(fields["cost"]) || !validateCounters(fields["tokens"]) {
		return bad()
	}
	id, ok := boundedString(fields["id"], 30, true)
	if !ok || !nativeID(id, "ses") {
		return bad()
	}
	slug, ok := boundedString(fields["slug"], 256, true)
	if !ok {
		return bad()
	}
	project, ok := boundedString(fields["projectID"], 256, true)
	if !ok {
		return bad()
	}
	// Native sessionPath is relative to the worktree and is exactly empty when
	// cwd is the Git root. Absolute directory/context checks retain authority;
	// this descriptive relative value cannot require a fabricated nonempty path.
	if _, ok := boundedString(fields["path"], 32768, false); !ok {
		return bad()
	}
	if _, ok := boundedString(fields["title"], 4096, true); !ok || fresh && !scalar(fields["title"], creation.settings.Title) {
		return bad()
	}
	model, err := shape(fields["model"], []string{"id", "providerID"}, []string{"variant"})
	if err != nil || !scalar(model["id"], creation.settings.Model) || !scalar(model["providerID"], creation.settings.Provider) {
		return bad()
	}
	// The pinned native setAgentModel stores the literal default variant on
	// a per-input agent transition. This is its absent-variant equivalent,
	// never permission to accept an explicit alternate model variant.
	if variant, ok := model["variant"]; ok && !scalar(variant, "default") {
		return bad()
	}
	metadata, err := shape(fields["metadata"], []string{"delidev"}, nil)
	if err != nil {
		return bad()
	}
	marker, err := shape(metadata["delidev"], []string{"request_id"}, nil)
	if err != nil || !scalar(marker["request_id"], string(creation.request)) {
		return bad()
	}
	times, err := shape(fields["time"], []string{"created", "updated"}, nil)
	if err != nil {
		return bad()
	}
	created, ok := nativeCount(times["created"])
	updated, valid := nativeCount(times["updated"])
	// The pinned native constructor reads Date.now() separately for each
	// timestamp. Crossing a millisecond boundary is valid even on creation;
	// equality is not an ownership or freshness guarantee.
	if !ok || !valid || created == 0 || updated < created {
		return bad()
	}
	var permissions []json.RawMessage
	if json.Unmarshal(fields["permission"], &permissions) != nil || len(permissions) != len(creation.settings.Permission) {
		return bad()
	}
	for i, rule := range permissions {
		fields, err := shape(rule, []string{"permission", "pattern", "action"}, nil)
		want := creation.settings.Permission[i]
		if err != nil || !scalar(fields["permission"], want.Permission) || !scalar(fields["pattern"], want.Pattern) || !scalar(fields["action"], string(want.Action)) {
			return bad()
		}
	}
	if summary, exists := fields["summary"]; exists {
		fields, err := shape(summary, []string{"additions", "deletions", "files"}, []string{"diffs"})
		if err != nil {
			return bad()
		}
		for _, key := range []string{"additions", "deletions", "files"} {
			if _, ok := nativeCount(fields[key]); !ok {
				return bad()
			}
		}
		if diffs, exists := fields["diffs"]; exists && !validateDiffs(diffs) {
			return bad()
		}
	}
	return sessionIdentity{id: id, project: project, slug: slug, created: created}, nil
}

func validateStoredInput(raw []byte, settings SessionSettings, input sessionInput) (bool, error) {
	root, err := shape(raw, []string{"info", "parts"}, nil)
	if err != nil {
		return false, sessionProblem()
	}
	message, err := decodeNativeMessage(root["info"])
	if err != nil || message.Role != UserMessageRole || message.ID != input.receipt.MessageID || message.SessionID != input.receipt.SessionID || message.User == nil {
		return false, sessionProblem()
	}
	user := message.User
	if user.Agent != string(settings.Agent) || user.Provider != settings.Provider || user.Model != settings.Model || user.Variant != nil || user.System != nil || user.Tools != nil || user.Format != nil {
		return false, sessionProblem()
	}
	var parts []json.RawMessage
	if json.Unmarshal(root["parts"], &parts) != nil || parts == nil || len(parts) > 1 {
		return false, sessionProblem()
	}
	// The pinned native implementation synchronizes the user-message row before
	// each part. A valid original row with an empty array is a real intermediate
	// state, not complete storage and not evidence of rejection or permission to
	// resend. Once storage was confirmed, the caller treats this as regression.
	if len(parts) == 0 {
		return false, nil
	}
	part, err := shape(parts[0], []string{"id", "sessionID", "messageID", "type", "text"}, nil)
	if err != nil || !scalar(part["id"], input.receipt.PartID) || !scalar(part["sessionID"], input.receipt.SessionID) || !scalar(part["messageID"], input.receipt.MessageID) || !scalar(part["type"], "text") {
		return false, sessionProblem()
	}
	text, ok := boundedString(part["text"], 256<<10, true)
	if !ok || sha256.Sum256([]byte(text)) != input.digest {
		return false, sessionProblem()
	}
	return true, nil
}
