package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	googleuuid "github.com/google/uuid"
	"golang.org/x/net/idna"
)

const (
	legacySettingsSchemaVersion = 1
	prefixMappingsSchemaVersion = 2
	settingsSchemaVersion       = 3
	maximumMappingPathSegments  = 32
	maximumMappingGlobstars     = 8
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
	settingsMappingPattern = regexp.MustCompile(`^(https?|\*)://(\[[^\]]+\]|[^/:]+)(?::([^/]*))?(/.*)?$`)
	settingsMappingPort    = regexp.MustCompile(`^[1-9]\d{0,4}$`)
	settingsTimestamp      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	// Browser URL parsing permits non-STD3 labels, while the profile retains IDNA label validation.
	settingsIDNAProfile = idna.New(idna.MapForLookup(), idna.StrictDomainName(false))
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
	for index, entry := range decks {
		profileRef, err := validateSettingsDeck(entry, fmt.Sprintf("$.decks[%d]", index), legacy)
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
	mappingProfileRefs, err := validateSettingsURLMappings(root["urlMappings"], bodyVersion <= prefixMappingsSchemaVersion)
	if err != nil {
		return err
	}
	for _, profileRef := range append(append(githubProfileRefs, deckProfileRefs...), mappingProfileRefs...) {
		if _, exists := profileIDs[profileRef]; !exists {
			return errors.New("GitHub profile reference must reference a configured GitHub profile")
		}
	}
	if err := validateSettingsShortcuts(root["shortcuts"], bodyVersion == settingsSchemaVersion); err != nil {
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

func validateSettingsDeck(value any, path string, legacy bool) (string, error) {
	fields := []string{"id", "title", "query", "repository", "display", "refreshMinutes", "notifications"}
	if !legacy {
		fields = []string{"id", "title", "query", "repository", "profileRef", "display", "refreshMinutes", "notifications"}
	}
	deck, err := settingsObject(value, path, fields...)
	if err != nil {
		return "", err
	}
	if _, err := settingsUUIDv7(deck["id"], path+".id"); err != nil {
		return "", err
	}
	if _, err := settingsText(deck["title"], path+".title", false); err != nil {
		return "", err
	}
	if _, err := settingsText(deck["query"], path+".query", true); err != nil {
		return "", err
	}
	repository := ""
	if deck["repository"] != nil {
		repository, err = settingsText(deck["repository"], path+".repository", false)
		if err != nil {
			return "", err
		}
	}
	profileRef := ""
	if !legacy {
		profileRef, err = settingsNullableProfileRef(deck["profileRef"], path+".profileRef")
		if err != nil {
			return "", err
		}
		if repository == "" && profileRef != "" {
			return "", fmt.Errorf("%s.profileRef must be null when repository is null", path)
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
	for index, notification := range notifications {
		if err := settingsEnum(notification, fmt.Sprintf("%s.notifications[%d]", path, index), "review", "checks", "merged", "closed"); err != nil {
			return "", err
		}
	}
	return profileRef, nil
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

func validateSettingsURLMappings(value any, prefixMappings bool) ([]string, error) {
	mappings, err := settingsArray(value, "$.urlMappings")
	if err != nil {
		return nil, err
	}
	if len(mappings) > 100 {
		return nil, errors.New("$.urlMappings must contain at most 100 entries")
	}
	if prefixMappings {
		for index, entry := range mappings {
			path := fmt.Sprintf("$.urlMappings[%d]", index)
			mapping, err := settingsObject(entry, path, "sourcePrefix", "destinationPrefix")
			if err != nil {
				return nil, err
			}
			for _, field := range []string{"sourcePrefix", "destinationPrefix"} {
				if err := settingsURL(mapping[field], path+"."+field, false); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}
	ids := make(map[string]struct{}, len(mappings))
	profileRefs := make([]string, 0, len(mappings))
	for index, entry := range mappings {
		path := fmt.Sprintf("$.urlMappings[%d]", index)
		mapping, err := settingsObject(entry, path, "id", "pattern", "repository", "credentialProfileRef", "priority", "chromeOrigin", "updatedAt")
		if err != nil {
			return nil, err
		}
		id, err := settingsUUIDv7(mapping["id"], path+".id")
		if err != nil {
			return nil, err
		}
		if _, exists := ids[id]; exists {
			return nil, errors.New("$.urlMappings must not contain duplicate mapping IDs")
		}
		ids[id] = struct{}{}
		if err := settingsURLMappingPattern(mapping["pattern"], path+".pattern"); err != nil {
			return nil, err
		}
		repository, err := settingsObject(mapping["repository"], path+".repository", "owner", "name")
		if err != nil {
			return nil, err
		}
		for _, field := range []string{"owner", "name"} {
			if _, err := settingsText(repository[field], path+".repository."+field, false); err != nil {
				return nil, err
			}
		}
		profileRef, err := settingsText(mapping["credentialProfileRef"], path+".credentialProfileRef", false)
		if err != nil || !settingsProfileRef.MatchString(profileRef) {
			return nil, fmt.Errorf("%s.credentialProfileRef is invalid", path)
		}
		profileRefs = append(profileRefs, profileRef)
		if _, err := settingsInteger(mapping["priority"], path+".priority", -1_000_000, 1_000_000); err != nil {
			return nil, err
		}
		if mapping["chromeOrigin"] != nil {
			if err := settingsChromeOrigin(mapping["chromeOrigin"], path+".chromeOrigin"); err != nil {
				return nil, err
			}
		}
		if err := settingsCanonicalTimestamp(mapping["updatedAt"], path+".updatedAt"); err != nil {
			return nil, err
		}
	}
	return profileRefs, nil
}

func validateSettingsShortcuts(value any, structured bool) error {
	shortcuts, err := settingsObject(value, "$.shortcuts", "desktop", "ios", "android")
	if err != nil {
		return err
	}
	if structured {
		if err := validateStructuredDesktopShortcuts(shortcuts["desktop"]); err != nil {
			return err
		}
		for _, platform := range []string{"ios", "android"} {
			items, ok := shortcuts[platform].(map[string]any)
			if !ok || len(items) != 0 {
				return fmt.Errorf("$.shortcuts.%s must be an empty object", platform)
			}
		}
		return nil
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

func validateStructuredDesktopShortcuts(value any) error {
	bindings, ok := value.(map[string]any)
	if !ok {
		return errors.New("$.shortcuts.desktop must be an object")
	}
	actions := []string{"shell.command-palette", "realqa.capture.display", "realqa.capture.active-window", "realqa.capture.all-displays", "realqa.capture.selection", "realqa.capture.toolbar"}
	if len(bindings) != len(actions) {
		return errors.New("$.shortcuts.desktop must contain every contracted shortcut action")
	}
	keys := map[string]struct{}{"key-k": {}, "digit-1": {}, "digit-2": {}, "digit-3": {}, "digit-4": {}, "digit-5": {}, "space": {}, "tab": {}, "key-q": {}, "delete": {}, "backspace": {}}
	modifiers := map[string]struct{}{"right-primary": {}, "shift": {}, "alt": {}}
	bareKeys := map[string]struct{}{"digit-1": {}, "digit-2": {}, "digit-3": {}, "digit-4": {}, "digit-5": {}}
	seen := make(map[string]struct{})
	for _, action := range actions {
		binding, err := settingsObject(bindings[action], "$.shortcuts.desktop."+action, "enabled", "modifiers", "key")
		if err != nil {
			return err
		}
		enabled, ok := binding["enabled"].(bool)
		if !ok {
			return fmt.Errorf("$.shortcuts.desktop.%s.enabled must be a boolean", action)
		}
		key, ok := binding["key"].(string)
		if !ok {
			return fmt.Errorf("$.shortcuts.desktop.%s.key must be a shortcut key", action)
		}
		if _, ok := keys[key]; !ok {
			return fmt.Errorf("$.shortcuts.desktop.%s.key must be a shortcut key", action)
		}
		values, err := settingsArray(binding["modifiers"], "$.shortcuts.desktop."+action+".modifiers")
		if err != nil {
			return err
		}
		seenModifiers := make(map[string]struct{})
		for _, value := range values {
			modifier, ok := value.(string)
			if !ok {
				return fmt.Errorf("$.shortcuts.desktop.%s.modifiers must contain modifier enums", action)
			}
			if _, ok := modifiers[modifier]; !ok {
				return fmt.Errorf("$.shortcuts.desktop.%s.modifiers must contain modifier enums", action)
			}
			if _, duplicate := seenModifiers[modifier]; duplicate {
				return fmt.Errorf("$.shortcuts.desktop.%s.modifiers must be unique", action)
			}
			seenModifiers[modifier] = struct{}{}
		}
		if enabled && len(values) == 0 {
			if _, ok := bareKeys[key]; !ok {
				return fmt.Errorf("$.shortcuts.desktop.%s requires a modifier", action)
			}
		}
		if enabled {
			modifierNames := make([]string, 0, len(values))
			for modifier := range seenModifiers {
				modifierNames = append(modifierNames, modifier)
			}
			sort.Strings(modifierNames)
			chord := strings.Join(modifierNames, "+") + "+" + key
			if _, duplicate := seen[chord]; duplicate {
				return errors.New("$.shortcuts.desktop must not contain duplicate enabled chords")
			}
			seen[chord] = struct{}{}
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

func settingsURLMappingPattern(value any, path string) error {
	pattern, err := settingsText(value, path, false)
	if err != nil || pattern != strings.TrimSpace(pattern) || strings.ContainsAny(pattern, "?#") {
		return fmt.Errorf("%s must be a URL pattern without credentials, query, or fragment", path)
	}
	match := settingsMappingPattern.FindStringSubmatch(pattern)
	if match == nil || strings.ContainsAny(match[2], "@\\") {
		return fmt.Errorf("%s must be a URL pattern without credentials, query, or fragment", path)
	}
	if !settingsValidURLHost(match[2], true) {
		return fmt.Errorf("%s has an invalid host", path)
	}
	port := match[3]
	if port != "" && port != "*" {
		if !settingsMappingPort.MatchString(port) {
			return fmt.Errorf("%s has an invalid port", path)
		}
		parsed, err := strconv.ParseUint(port, 10, 16)
		if err != nil || parsed == 0 || parsed > 65535 {
			return fmt.Errorf("%s has an invalid port", path)
		}
	}
	pathText := match[4]
	if strings.Contains(pathText, "\\") {
		return fmt.Errorf("%s has an invalid path", path)
	}
	segments := strings.Split(pathText, "/")[1:]
	for _, segment := range segments {
		if strings.Contains(segment, "*") && segment != "*" && segment != "**" {
			return fmt.Errorf("%s has invalid path wildcards", path)
		}
	}
	segments = settingsCanonicalMappingPathSegments(segments)
	if len(segments) > maximumMappingPathSegments {
		return fmt.Errorf("%s must contain at most %d path segments", path, maximumMappingPathSegments)
	}
	globstars := 0
	for _, segment := range segments {
		if segment == "**" {
			globstars++
		}
	}
	if globstars > maximumMappingGlobstars {
		return fmt.Errorf("%s must contain at most %d globstar segments", path, maximumMappingGlobstars)
	}
	return nil
}

// settingsCanonicalMappingPathSegments mirrors WHATWG URL dot-segment removal
// before applying complexity bounds, including percent-encoded dot segments.
func settingsCanonicalMappingPathSegments(segments []string) []string {
	canonical := make([]string, 0, len(segments))
	for index, segment := range segments {
		switch strings.ToLower(segment) {
		case ".", "%2e":
			if len(canonical) > 0 && index == len(segments)-1 {
				canonical = append(canonical, "")
			}
			continue
		case "..", ".%2e", "%2e.", "%2e%2e":
			if len(canonical) > 0 {
				canonical = canonical[:len(canonical)-1]
				if index == len(segments)-1 && len(canonical) > 0 {
					canonical = append(canonical, "")
				}
			}
		default:
			canonical = append(canonical, segment)
		}
	}
	return canonical
}

func settingsChromeOrigin(value any, path string) error {
	text, err := settingsText(value, path, false)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(text)
	port := ""
	if err == nil {
		port = parsed.Port()
	}
	parsedPort, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || !settingsValidURLHost(parsed.Hostname(), false) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || strings.Contains(parsed.Hostname(), "*") || (port != "" && (portErr != nil || parsedPort > 65535)) {
		return fmt.Errorf("%s must be a concrete HTTP(S) origin without credentials, path, query, or fragment", path)
	}
	return nil
}

func settingsValidURLHost(host string, allowWildcard bool) bool {
	if strings.HasPrefix(host, "[") {
		if !strings.HasSuffix(host, "]") {
			return false
		}
		address, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
		return err == nil && address.Is6() && address.Zone() == ""
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) == 0 || strings.TrimSuffix(host, ".") == "" {
		return false
	}
	for index, label := range labels {
		if label == "" || (strings.Contains(label, "*") && (!allowWildcard || label != "*")) {
			return false
		}
		if label == "*" {
			labels[index] = fmt.Sprintf("devhud-wildcard-%d", index)
		}
	}
	concreteHost := strings.Join(labels, ".")
	if numeric, valid := settingsWhatwgIPv4(concreteHost); numeric {
		return valid
	}
	asciiHost, err := settingsIDNAProfile.ToASCII(concreteHost)
	if err != nil {
		return false
	}
	// IDNA maps several Unicode full-stop variants to '.'. Wildcards are
	// label-based, so accepting a mapping that gains labels during that
	// canonicalization would make the synchronized pattern unparsable by the
	// browser client.
	if len(strings.Split(strings.TrimSuffix(asciiHost, "."), ".")) != len(labels) {
		return false
	}
	for _, label := range labels {
		if strings.HasPrefix(strings.ToLower(label), "xn--") {
			decoded, err := settingsIDNAProfile.ToUnicode(label)
			if err != nil || decoded == "" {
				return false
			}
		}
	}
	return true
}

// settingsWhatwgIPv4 preserves the numeric IPv4 forms accepted by new URL while
// rejecting out-of-range abbreviated forms before they can reach a browser client.
func settingsWhatwgIPv4(host string) (numeric bool, valid bool) {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(parts) == 0 {
		return false, false
	}
	if _, ok := settingsWhatwgIPv4Number(parts[len(parts)-1]); !ok {
		return false, false
	}
	if len(parts) > 4 {
		return true, false
	}
	numbers := make([]uint64, len(parts))
	for index, part := range parts {
		number, ok := settingsWhatwgIPv4Number(part)
		if !ok {
			return true, false
		}
		numbers[index] = number
	}
	for _, number := range numbers[:len(numbers)-1] {
		if number > 255 {
			return true, false
		}
	}
	return true, numbers[len(numbers)-1] < uint64(1)<<(8*(5-len(numbers)))
}

func settingsWhatwgIPv4Number(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	base := 10
	digits := value
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "0x") {
		base, digits = 16, value[2:]
	} else if len(value) > 1 && value[0] == '0' {
		base, digits = 8, value[1:]
	}
	if digits == "" {
		return 0, true
	}
	number, err := strconv.ParseUint(digits, base, 32)
	return number, err == nil
}

func settingsCanonicalTimestamp(value any, path string) error {
	timestamp, err := settingsText(value, path, false)
	if err != nil || !settingsTimestamp.MatchString(timestamp) {
		return fmt.Errorf("%s must be a canonical UTC timestamp", path)
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		return fmt.Errorf("%s must be a canonical UTC timestamp", path)
	}
	return nil
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
			if sensitiveSettingsKey.MatchString(key) && !contractedDesktopShortcutAction(path, key) {
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

func contractedDesktopShortcutAction(path, key string) bool {
	if path != "$.shortcuts.desktop" {
		return false
	}
	switch key {
	case "shell.command-palette", "realqa.capture.display", "realqa.capture.active-window", "realqa.capture.all-displays", "realqa.capture.selection", "realqa.capture.toolbar":
		return true
	default:
		return false
	}
}
