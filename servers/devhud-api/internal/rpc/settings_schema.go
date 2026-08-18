package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"

	googleuuid "github.com/google/uuid"
)

const (
	legacySettingsSchemaVersion   = 1
	previousSettingsSchemaVersion = 2
	settingsSchemaVersion         = 3
)

var (
	sensitiveSettingsKey    = regexp.MustCompile(`(?i)(^|[-_.])(api[-_.]?url|token|password|passwd|pwd|secret|pat|access[-_.]?key([-_.]?id)?|private[-_.]?key|authorization|cookie|agent[-_.]?(path|version)|autostart|window|widget|draft|cache|pairing|permission)($|[-_.])`)
	sensitiveSettingsValues = []*regexp.Regexp{
		regexp.MustCompile(`(^|[^A-Za-z0-9_])(ghp|github_pat)_[A-Za-z0-9_]+([^A-Za-z0-9_]|$)`),
		regexp.MustCompile(`(^|[^A-Z0-9])AKIA[0-9A-Z]{16}([^A-Z0-9]|$)`),
		regexp.MustCompile(`(^|[^A-Za-z0-9_-])eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+([^A-Za-z0-9_-]|$)`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	}
	safeSettingsIdentifier = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)
	settingsProfileRef     = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
)

func validateDevHudSettings(value []byte, envelopeSchemaVersion uint32) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("canonical_json must contain a valid settings snapshot")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return err
	}
	if err := rejectSensitiveSettings(decoded, "$"); err != nil {
		return err
	}
	root, err := settingsObject(decoded, "$", "schemaVersion", "appearance", "decks", "github", "urlMappings", "shortcuts", "agents", "uploads")
	if err != nil {
		return err
	}
	bodyVersion, err := settingsInteger(root["schemaVersion"], "$.schemaVersion", legacySettingsSchemaVersion, settingsSchemaVersion)
	if err != nil {
		return err
	}
	if uint32(bodyVersion) != envelopeSchemaVersion {
		return errors.New("$.schemaVersion must match the snapshot envelope schema version")
	}
	legacy := bodyVersion == legacySettingsSchemaVersion
	previous := bodyVersion == previousSettingsSchemaVersion

	appearance, err := settingsObject(root["appearance"], "$.appearance", "theme", "language")
	if err != nil {
		return err
	}
	if err := settingsEnum(appearance["theme"], "$.appearance.theme", "system", "light", "dark"); err != nil {
		return err
	}
	if err := settingsEnum(appearance["language"], "$.appearance.language", "system", "en", "ko"); err != nil {
		return err
	}

	decks, err := settingsArray(root["decks"], "$.decks")
	if err != nil {
		return err
	}
	if len(decks) > 25 {
		return errors.New("$.decks must contain at most 25 entries")
	}
	deckProfileRefs := make([]string, 0, len(decks))
	deckIDs := make(map[string]struct{}, len(decks))
	for index, entry := range decks {
		path := fmt.Sprintf("$.decks[%d]", index)
		deck, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		deckID, err := settingsUUIDv7(deck["id"], path+".id")
		if err != nil {
			return err
		}
		if _, exists := deckIDs[deckID]; exists {
			return errors.New("$.decks must contain unique IDs")
		}
		deckIDs[deckID] = struct{}{}
		profileRef, err := validateSettingsDeck(entry, path, legacy, previous)
		if err != nil {
			return err
		}
		if profileRef != "" {
			deckProfileRefs = append(deckProfileRefs, profileRef)
		}
	}

	githubFields := []string{"repositories", "issueTracker"}
	if !legacy {
		githubFields = []string{"profiles", "pendingPatRemovals", "repositories", "issueTracker"}
	}
	github, err := settingsObject(root["github"], "$.github", githubFields...)
	if err != nil {
		return err
	}
	profileIDs := make(map[string]struct{})
	if !legacy {
		profiles, err := settingsArray(github["profiles"], "$.github.profiles")
		if err != nil {
			return err
		}
		if len(profiles) > 25 {
			return errors.New("$.github.profiles must contain at most 25 entries")
		}
		for index, entry := range profiles {
			path := fmt.Sprintf("$.github.profiles[%d]", index)
			profile, err := settingsObject(entry, path, "id", "name", "kind")
			if err != nil {
				return err
			}
			id, err := settingsUUIDv7(profile["id"], path+".id")
			if err != nil {
				return err
			}
			if _, exists := profileIDs[id]; exists {
				return errors.New("$.github.profiles must contain unique IDs")
			}
			profileIDs[id] = struct{}{}
			name, err := settingsText(profile["name"], path+".name", false)
			if err != nil || strings.TrimSpace(name) != name || settingsTextLength(name) > 80 {
				return fmt.Errorf("%s.name must be a trimmed string of at most 80 characters", path)
			}
			if err := settingsEnum(profile["kind"], path+".kind", "fine-grained", "classic"); err != nil {
				return err
			}
		}

		removals, err := settingsArray(github["pendingPatRemovals"], "$.github.pendingPatRemovals")
		if err != nil {
			return err
		}
		if len(removals) > 25 {
			return errors.New("$.github.pendingPatRemovals must contain at most 25 entries")
		}
		seenRemovals := make(map[string]struct{})
		for index, entry := range removals {
			id, err := settingsUUIDv7(entry, fmt.Sprintf("$.github.pendingPatRemovals[%d]", index))
			if err != nil {
				return err
			}
			if _, exists := seenRemovals[id]; exists {
				return errors.New("$.github.pendingPatRemovals must contain unique IDs")
			}
			if _, active := profileIDs[id]; active {
				return errors.New("$.github.pendingPatRemovals must not reference an active GitHub profile")
			}
			seenRemovals[id] = struct{}{}
		}
	}

	githubProfileRefs, err := validateSettingsGitHub(github, legacy)
	if err != nil {
		return err
	}
	for _, profileRef := range append(githubProfileRefs, deckProfileRefs...) {
		if _, exists := profileIDs[profileRef]; !exists {
			return errors.New("GitHub profile reference must reference a configured GitHub profile")
		}
	}

	if err := validateSettingsURLMappings(root["urlMappings"]); err != nil {
		return err
	}
	if err := validateSettingsShortcuts(root["shortcuts"]); err != nil {
		return err
	}
	if err := validateSettingsAgents(root["agents"]); err != nil {
		return err
	}
	return validateSettingsUploads(root["uploads"])
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("canonical_json must contain exactly one settings snapshot")
}

func validateSettingsDeck(value any, path string, legacy bool, previous bool) (string, error) {
	fields := []string{"id", "title", "query", "repository", "display", "refreshMinutes", "notifications"}
	if previous {
		fields = []string{"id", "title", "query", "repository", "profileRef", "display", "refreshMinutes", "notifications"}
	} else if !legacy {
		fields = []string{"id", "name", "query", "builder", "profileRef", "display", "refreshMinutes", "notifications"}
	}
	deck, err := settingsObject(value, path, fields...)
	if err != nil {
		return "", err
	}
	if _, err := settingsUUIDv7(deck["id"], path+".id"); err != nil {
		return "", err
	}
	nameField := "title"
	if !legacy && !previous {
		nameField = "name"
	}
	if _, err := settingsText(deck[nameField], path+"."+nameField, false); err != nil {
		return "", err
	}
	query, err := settingsText(deck["query"], path+".query", true)
	if err != nil {
		return "", err
	}
	if !legacy && !previous && !hasPositivePullRequestQualifier(query) {
		return "", fmt.Errorf("%s.query must contain a standalone positive is:pr qualifier", path)
	}
	if !legacy && !previous && !hasRepositoryQualifier(query) {
		return "", fmt.Errorf("%s.query must contain a repository qualifier when a credential profile is selected", path)
	}
	if previous && deck["repository"] != nil {
		if _, err = settingsText(deck["repository"], path+".repository", false); err != nil {
			return "", err
		}
	}
	profileRef := ""
	if !legacy {
		profileRef, err = settingsNullableProfileRef(deck["profileRef"], path+".profileRef")
		if err != nil {
			return "", err
		}
		if !previous && profileRef == "" {
			return "", fmt.Errorf("%s.profileRef must be selected", path)
		}
	}
	if !legacy && !previous {
		builder, err := settingsObjectOrNull(deck["builder"], path+".builder", "repository", "author", "review", "label", "state")
		if err != nil {
			return "", err
		}
		if builder != nil {
			actual := settingsDeckBuilder{}
			for _, field := range []string{"repository", "author", "label"} {
				if builder[field] != nil {
					value, err := settingsText(builder[field], path+".builder."+field, false)
					if err != nil {
						return "", err
					}
					if strings.TrimSpace(value) != value {
						return "", fmt.Errorf("%s.builder.%s must be trimmed", path, field)
					}
					actual.set(field, value)
				}
			}
			if builder["review"] != nil {
				if err := settingsEnum(builder["review"], path+".builder.review", "approved", "changes-requested", "required"); err != nil {
					return "", err
				}
				actual.set("review", builder["review"].(string))
			}
			if builder["state"] != nil {
				if err := settingsEnum(builder["state"], path+".builder.state", "open", "closed", "merged"); err != nil {
					return "", err
				}
				actual.set("state", builder["state"].(string))
			}
			if !actual.equal(settingsDeckBuilderProjection(query)) {
				return "", fmt.Errorf("%s.builder must be the lossless projection of the query", path)
			}
		}
	}
	display, err := settingsObject(deck["display"], path+".display", "groupBy", "showDrafts")
	if err != nil {
		return "", err
	}
	if err := settingsEnum(display["groupBy"], path+".display.groupBy", "none", "repository", "author"); err != nil {
		return "", err
	}
	if _, ok := display["showDrafts"].(bool); !ok {
		return "", fmt.Errorf("%s.display.showDrafts must be a boolean", path)
	}
	refresh, err := settingsInteger(deck["refreshMinutes"], path+".refreshMinutes", 1, 30)
	if err != nil {
		return "", err
	}
	if refresh != 1 && refresh != 5 && refresh != 15 && refresh != 30 {
		return "", fmt.Errorf("%s.refreshMinutes must be 1, 5, 15, or 30", path)
	}
	notifications, err := settingsArray(deck["notifications"], path+".notifications")
	if err != nil {
		return "", err
	}
	seenNotifications := make(map[string]struct{}, len(notifications))
	for index, notification := range notifications {
		if err := settingsEnum(notification, fmt.Sprintf("%s.notifications[%d]", path, index), "review", "checks", "merged", "closed"); err != nil {
			return "", err
		}
		if !legacy && !previous {
			value := notification.(string)
			if _, exists := seenNotifications[value]; exists {
				return "", fmt.Errorf("%s.notifications must contain unique values", path)
			}
			seenNotifications[value] = struct{}{}
		}
	}
	return profileRef, nil
}

type settingsDeckBuilder struct {
	repository *string
	author     *string
	review     *string
	label      *string
	state      *string
}

func (builder *settingsDeckBuilder) set(field string, value string) {
	switch field {
	case "repository":
		builder.repository = &value
	case "author":
		builder.author = &value
	case "review":
		builder.review = &value
	case "label":
		builder.label = &value
	case "state":
		builder.state = &value
	}
}

func (builder settingsDeckBuilder) equal(other settingsDeckBuilder) bool {
	return settingsDeckBuilderValueEqual(builder.repository, other.repository) &&
		settingsDeckBuilderValueEqual(builder.author, other.author) &&
		settingsDeckBuilderValueEqual(builder.review, other.review) &&
		settingsDeckBuilderValueEqual(builder.label, other.label) &&
		settingsDeckBuilderValueEqual(builder.state, other.state)
}

func settingsDeckBuilderValueEqual(left *string, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func settingsDeckBuilderProjection(query string) settingsDeckBuilder {
	projection := settingsDeckBuilder{}
	for _, token := range deckQueryTokens(query) {
		for _, qualifier := range []struct {
			prefix string
			field  string
		}{
			{"repo:", "repository"},
			{"author:", "author"},
			{"review:", "review"},
			{"label:", "label"},
			{"is:", "state"},
		} {
			if len(token) < len(qualifier.prefix) || !strings.EqualFold(token[:len(qualifier.prefix)], qualifier.prefix) {
				continue
			}
			value := token[len(qualifier.prefix):]
			if qualifier.field == "review" {
				value = strings.ToLower(value)
				if value == "changes_requested" {
					value = "changes-requested"
				} else if value != "approved" && value != "required" {
					continue
				}
			} else if qualifier.field == "state" {
				value = strings.ToLower(value)
				if value != "open" && value != "closed" && value != "merged" {
					continue
				}
			} else {
				if value == "" {
					continue
				}
				value = unquoteSettingsDeckQualifier(value)
			}
			switch qualifier.field {
			case "repository":
				if projection.repository != nil {
					continue
				}
			case "author":
				if projection.author != nil {
					continue
				}
			case "review":
				if projection.review != nil {
					continue
				}
			case "label":
				if projection.label != nil {
					continue
				}
			case "state":
				if projection.state != nil {
					continue
				}
			}
			projection.set(qualifier.field, value)
		}
	}
	return projection
}

func unquoteSettingsDeckQualifier(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		var unquoted strings.Builder
		unquoted.Grow(len(value) - 2)
		escaped := false
		for _, character := range value[1 : len(value)-1] {
			if escaped {
				unquoted.WriteRune(character)
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			unquoted.WriteRune(character)
		}
		if escaped {
			unquoted.WriteByte('\\')
		}
		return unquoted.String()
	}
	return value
}

func hasPositivePullRequestQualifier(query string) bool {
	for _, token := range deckQueryTokens(query) {
		if strings.EqualFold(token, "is:pr") {
			return true
		}
	}
	return false
}

func hasRepositoryQualifier(query string) bool {
	found := false
	for _, token := range deckQueryTokens(query) {
		if len(token) < len("repo:") || !strings.EqualFold(token[:len("repo:")], "repo:") {
			continue
		}
		value := token[len("repo:"):]
		if strings.Count(value, "/") != 1 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\" \t\n\r") {
			return false
		}
		found = true
	}
	return found
}

// deckQueryTokens keeps quoted search phrases from being interpreted as qualifiers.
func deckQueryTokens(query string) []string {
	tokens := make([]string, 0)
	var token strings.Builder
	quoted := false
	escaped := false
	flush := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	for _, character := range query {
		if escaped {
			token.WriteRune(character)
			escaped = false
			continue
		}
		if quoted {
			token.WriteRune(character)
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				quoted = false
			}
			continue
		}
		if character == '"' {
			token.WriteRune(character)
			quoted = true
		} else if unicode.IsSpace(character) {
			flush()
		} else {
			token.WriteRune(character)
		}
	}
	flush()
	return tokens
}

func validateSettingsGitHub(github map[string]any, legacy bool) ([]string, error) {
	repositories, err := settingsArray(github["repositories"], "$.github.repositories")
	if err != nil {
		return nil, err
	}
	profileRefs := make([]string, 0, len(repositories)+1)
	for index, entry := range repositories {
		path := fmt.Sprintf("$.github.repositories[%d]", index)
		fields := []string{"owner", "name"}
		if !legacy {
			fields = []string{"owner", "name", "profileRef"}
		}
		repository, err := settingsObject(entry, path, fields...)
		if err != nil {
			return nil, err
		}
		for _, field := range []string{"owner", "name"} {
			if _, err := settingsText(repository[field], path+"."+field, false); err != nil {
				return nil, err
			}
		}
		if !legacy {
			profileRef, err := settingsNullableProfileRef(repository["profileRef"], path+".profileRef")
			if err != nil {
				return nil, err
			}
			if profileRef != "" {
				profileRefs = append(profileRefs, profileRef)
			}
		}
	}
	if github["issueTracker"] == nil {
		return profileRefs, nil
	}
	fields := []string{"owner", "repository", "labels"}
	if !legacy {
		fields = []string{"owner", "repository", "labels", "profileRef"}
	}
	tracker, err := settingsObject(github["issueTracker"], "$.github.issueTracker", fields...)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"owner", "repository"} {
		if _, err := settingsText(tracker[field], "$.github.issueTracker."+field, false); err != nil {
			return nil, err
		}
	}
	labels, err := settingsArray(tracker["labels"], "$.github.issueTracker.labels")
	if err != nil {
		return nil, err
	}
	for index, label := range labels {
		if _, err := settingsText(label, fmt.Sprintf("$.github.issueTracker.labels[%d]", index), false); err != nil {
			return nil, err
		}
	}
	if !legacy {
		profileRef, err := settingsNullableProfileRef(tracker["profileRef"], "$.github.issueTracker.profileRef")
		if err != nil {
			return nil, err
		}
		if profileRef != "" {
			profileRefs = append(profileRefs, profileRef)
		}
	}
	return profileRefs, nil
}

func validateSettingsURLMappings(value any) error {
	mappings, err := settingsArray(value, "$.urlMappings")
	if err != nil {
		return err
	}
	for index, entry := range mappings {
		path := fmt.Sprintf("$.urlMappings[%d]", index)
		mapping, err := settingsObject(entry, path, "sourcePrefix", "destinationPrefix")
		if err != nil {
			return err
		}
		for _, field := range []string{"sourcePrefix", "destinationPrefix"} {
			if err := settingsURL(mapping[field], path+"."+field, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettingsShortcuts(value any) error {
	shortcuts, err := settingsObject(value, "$.shortcuts", "desktop", "ios", "android")
	if err != nil {
		return err
	}
	for _, platform := range []string{"desktop", "ios", "android"} {
		path := "$.shortcuts." + platform
		items, ok := shortcuts[platform].(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		for key, value := range items {
			if !safeSettingsIdentifier.MatchString(key) || sensitiveSettingsKey.MatchString(key) || key == "__proto__" || key == "constructor" || key == "prototype" {
				return fmt.Errorf("%s.%s is not an allowed shortcut action", path, key)
			}
			if _, err := settingsText(value, path+"."+key, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettingsAgents(value any) error {
	agents, err := settingsArray(value, "$.agents")
	if err != nil {
		return err
	}
	for index, entry := range agents {
		path := fmt.Sprintf("$.agents[%d]", index)
		agent, err := settingsObject(entry, path, "id", "enabled", "kind", "mode", "repositoryPrompts", "profileRef")
		if err != nil {
			return err
		}
		id, err := settingsText(agent["id"], path+".id", false)
		if err != nil || !safeSettingsIdentifier.MatchString(id) {
			return fmt.Errorf("%s.id is invalid", path)
		}
		if _, ok := agent["enabled"].(bool); !ok {
			return fmt.Errorf("%s.enabled must be a boolean", path)
		}
		if err := settingsEnum(agent["kind"], path+".kind", "codex", "claude-code", "opencode"); err != nil {
			return err
		}
		if err := settingsEnum(agent["mode"], path+".mode", "draft", "direct"); err != nil {
			return err
		}
		if _, ok := agent["repositoryPrompts"].(bool); !ok {
			return fmt.Errorf("%s.repositoryPrompts must be a boolean", path)
		}
		if _, err := settingsNullableProfileRef(agent["profileRef"], path+".profileRef"); err != nil {
			return err
		}
	}
	return nil
}

func validateSettingsUploads(value any) error {
	uploads, err := settingsObject(value, "$.uploads", "provider", "r2")
	if err != nil {
		return err
	}
	if err := settingsEnum(uploads["provider"], "$.uploads.provider", "official", "r2"); err != nil {
		return err
	}
	if uploads["r2"] == nil {
		return nil
	}
	r2, err := settingsObject(uploads["r2"], "$.uploads.r2", "profileRef", "bucket", "endpoint", "region", "publicBaseUrl")
	if err != nil {
		return err
	}
	profileRef, err := settingsText(r2["profileRef"], "$.uploads.r2.profileRef", false)
	if err != nil || !settingsProfileRef.MatchString(profileRef) {
		return errors.New("$.uploads.r2.profileRef is invalid")
	}
	for _, field := range []string{"bucket", "region"} {
		if _, err := settingsText(r2[field], "$.uploads.r2."+field, false); err != nil {
			return err
		}
	}
	if err := settingsURL(r2["endpoint"], "$.uploads.r2.endpoint", true); err != nil {
		return err
	}
	if r2["publicBaseUrl"] != nil {
		return settingsURL(r2["publicBaseUrl"], "$.uploads.r2.publicBaseUrl", true)
	}
	return nil
}

func settingsObject(value any, path string, allowed ...string) (map[string]any, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
		if _, exists := record[key]; !exists {
			return nil, fmt.Errorf("%s.%s is required", path, key)
		}
	}
	for key := range record {
		if _, exists := allowedSet[key]; !exists {
			return nil, fmt.Errorf("%s.%s is an unknown field", path, key)
		}
	}
	return record, nil
}

func settingsObjectOrNull(value any, path string, allowed ...string) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	return settingsObject(value, path, allowed...)
}

func settingsArray(value any, path string) ([]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	return items, nil
}

func settingsText(value any, path string, allowEmpty bool) (string, error) {
	text, ok := value.(string)
	if !ok || (!allowEmpty && text == "") || settingsTextLength(text) > 4096 {
		return "", fmt.Errorf("%s must be a bounded string", path)
	}
	return text, nil
}

func settingsTextLength(value string) int {
	length := 0
	for _, character := range value {
		length += utf16.RuneLen(character)
	}
	return length
}

func settingsInteger(value any, path string, minimum, maximum int64) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer from %d through %d", path, minimum, maximum)
	}
	parsed, err := number.Int64()
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer from %d through %d", path, minimum, maximum)
	}
	return parsed, nil
}

func settingsEnum(value any, path string, allowed ...string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be one of %s", path, strings.Join(allowed, ", "))
	}
	for _, candidate := range allowed {
		if text == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", path, strings.Join(allowed, ", "))
}

func settingsUUIDv7(value any, path string) (string, error) {
	text, err := settingsText(value, path, false)
	if err != nil {
		return "", err
	}
	parsed, err := googleuuid.Parse(text)
	if err != nil || parsed.Version() != 7 || parsed.String() != text {
		return "", fmt.Errorf("%s must be a canonical lowercase RFC 9562 UUID v7", path)
	}
	return text, nil
}

func settingsNullableProfileRef(value any, path string) (string, error) {
	if value == nil {
		return "", nil
	}
	profileRef, err := settingsText(value, path, false)
	if err != nil || !settingsProfileRef.MatchString(profileRef) {
		return "", fmt.Errorf("%s is invalid", path)
	}
	return profileRef, nil
}

func settingsURL(value any, path string, httpsOnly bool) error {
	text, err := settingsText(value, path, false)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(text)
	invalid := err != nil || !parsed.IsAbs() || parsed.User != nil || strings.ContainsAny(text, "?#") || (httpsOnly && parsed.Scheme != "https")
	if invalid {
		if httpsOnly {
			return fmt.Errorf("%s must be an HTTPS URL without credentials, query, or fragment", path)
		}
		return fmt.Errorf("%s must be a URL without credentials, query, or fragment", path)
	}
	return nil
}

func rejectSensitiveSettings(value any, path string) error {
	switch typed := value.(type) {
	case string:
		for _, pattern := range sensitiveSettingsValues {
			if pattern.MatchString(typed) {
				return fmt.Errorf("%s contains secret material", path)
			}
		}
	case map[string]any:
		for key, item := range typed {
			if sensitiveSettingsKey.MatchString(key) {
				return fmt.Errorf("%s.%s is a device-local or secret field", path, key)
			}
			if err := rejectSensitiveSettings(item, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range typed {
			if err := rejectSensitiveSettings(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}
