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
	"unicode/utf8"

	googleuuid "github.com/google/uuid"
	"golang.org/x/net/idna"
)

const (
	legacySettingsSchemaVersion     = 1
	previousSettingsSchemaVersion   = 2
	collidingSettingsSchemaVersion  = 3
	structuredSettingsSchemaVersion = 4
	settingsSchemaVersion           = 5
	maximumMappingPathSegments      = 32
	maximumMappingGlobstars         = 8
)

var (
	sensitiveSettingsKey    = regexp.MustCompile(`(?i)(^|[-_.])(api[-_.]?url|token|password|passwd|pwd|secret|pat|access[-_.]?key([-_.]?id)?|private[-_.]?key|authorization|cookie|agent[-_.]?(path|version)|autostart|window|widget|draft|cache|pairing|permission)($|[-_.])`)
	sensitiveSettingsValues = []*regexp.Regexp{
		regexp.MustCompile(`(^|[^A-Za-z0-9_])(ghp|github_pat)_[A-Za-z0-9_]+([^A-Za-z0-9_]|$)`),
		regexp.MustCompile(`(^|[^A-Z0-9])AKIA[0-9A-Z]{16}([^A-Z0-9]|$)`),
		regexp.MustCompile(`(^|[^A-Za-z0-9_-])eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+([^A-Za-z0-9_-]|$)`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	}
	safeSettingsIdentifier     = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)
	settingsProfileRef         = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
	githubOwnerIdentifier      = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)
	githubRepositoryIdentifier = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	settingsMappingPattern     = regexp.MustCompile(`^(https?|\*)://(\[[^\]]+\]|[^/:]+)(?::([^/]*))?(/.*)?$`)
	settingsMappingPort        = regexp.MustCompile(`^[1-9]\d{0,4}$`)
	settingsTimestamp          = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
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
	if bodyVersion == collidingSettingsSchemaVersion && (len(decks) == 0 || !hasCurrentDeckShape(decks[0])) {
		previous = true
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
	prefixMappings := bodyVersion <= previousSettingsSchemaVersion || bodyVersion == collidingSettingsSchemaVersion && !hasStructuredURLMappingShape(root["urlMappings"])
	mappingProfileRefs, err := validateSettingsURLMappings(root["urlMappings"], prefixMappings)
	if err != nil {
		return err
	}
	for _, profileRef := range append(append(githubProfileRefs, deckProfileRefs...), mappingProfileRefs...) {
		if _, exists := profileIDs[profileRef]; !exists {
			return errors.New("GitHub profile reference must reference a configured GitHub profile")
		}
	}
	structuredShortcuts := bodyVersion >= structuredSettingsSchemaVersion || bodyVersion == collidingSettingsSchemaVersion && hasStructuredDesktopShortcutShape(root["shortcuts"])
	if err := validateSettingsShortcuts(root["shortcuts"], structuredShortcuts); err != nil {
		return err
	}
	if err := validateSettingsAgents(root["agents"]); err != nil {
		return err
	}
	return validateSettingsUploads(root["uploads"], bodyVersion)
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
	name, err := settingsText(deck[nameField], path+"."+nameField, false)
	if err != nil {
		return "", err
	}
	normalizedName := strings.TrimFunc(name, deckQueryWhitespace)
	if normalizedName == "" || (!legacy && !previous && normalizedName != name) {
		return "", fmt.Errorf("%s.name must be a trimmed nonblank string", path)
	}
	query, err := settingsText(deck["query"], path+".query", true)
	if err != nil {
		return "", err
	}
	hasPullRequestQualifier, hasRepositoryQualifier := deckQueryQualifiers(query)
	if !legacy && !previous && !hasPullRequestQualifier {
		return "", fmt.Errorf("%s.query must contain a standalone positive is:pr qualifier", path)
	}
	if !legacy && !previous && !hasRepositoryQualifier {
		return "", fmt.Errorf("%s.query must contain a repository qualifier when a credential profile is selected", path)
	}
	if !legacy && !previous && !deckQueryWithinGitHubSearchLimits(query) {
		return "", fmt.Errorf("%s.query exceeds GitHub Search limits", path)
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
	if previous && deck["repository"] == nil && profileRef != "" {
		return "", fmt.Errorf("%s.repository must be selected when a credential profile is selected", path)
	}
	if previous && deck["repository"] != nil {
		repository, repositoryErr := settingsText(deck["repository"], path+".repository", false)
		if repositoryErr != nil {
			return "", repositoryErr
		}
		if profileRef != "" {
			migratedQuery := query
			if !deckQueryHasPositivePullRequestQualifier(migratedQuery) {
				migratedQuery = appendSettingsDeckQualifier(migratedQuery, "is:pr")
			}
			if !deckQueryHasExactRepositoryQualifier(migratedQuery, repository) {
				migratedQuery = appendSettingsDeckQualifier(migratedQuery, "repo:"+repository)
			}
			hasPullRequestQualifier, hasRepositoryQualifier = deckQueryQualifiers(migratedQuery)
			if !hasPullRequestQualifier || !hasRepositoryQualifier {
				return "", fmt.Errorf("%s.repository must produce a valid repository-scoped pull-request query", path)
			}
			if !deckQueryWithinGitHubSearchLimits(migratedQuery) {
				return "", fmt.Errorf("%s.query exceeds GitHub Search limits", path)
			}
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
					if strings.TrimFunc(value, deckQueryWhitespace) != value {
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
	if deckQueryHasBooleanSyntax(query) {
		return settingsDeckBuilder{}
	}
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

func deckQueryHasBooleanSyntax(query string) bool {
	tokens, valid := deckBooleanTokens(query)
	if !valid {
		return false
	}
	for _, token := range tokens {
		if token.kind != deckBooleanTerm || strings.EqualFold(token.value, "and") || strings.EqualFold(token.value, "or") {
			return true
		}
	}
	return false
}

func deckQueryWithinGitHubSearchLimits(query string) bool {
	operators := 0
	excludedLength := 0
	for _, token := range deckQueryTokens(query) {
		if strings.EqualFold(token, "and") || strings.EqualFold(token, "or") || strings.EqualFold(token, "not") {
			operators++
			excludedLength += settingsTextLength(token)
		} else if deckQueryGitHubSearchQualifier(token) {
			excludedLength += settingsTextLength(token)
		}
	}
	return operators <= 5 && settingsTextLength(query)-excludedLength <= 256
}

func deckQueryGitHubSearchQualifier(value string) bool {
	value = strings.TrimPrefix(value, "-")
	separator := strings.IndexByte(value, ':')
	if separator < 1 {
		return false
	}
	for _, character := range value[:separator] {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-') {
			return false
		}
	}
	first := value[0]
	return first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z'
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

const deckQueryBranchLimit = 100

type deckQueryBranch struct {
	repositories            map[string]struct{}
	hasPullRequestQualifier bool
}

type deckBooleanTokenKind uint8

const (
	deckBooleanTerm deckBooleanTokenKind = iota
	deckBooleanOpen
	deckBooleanClose
	deckBooleanNot
)

type deckBooleanToken struct {
	kind  deckBooleanTokenKind
	value string
}

// deckQueryQualifiers proves every executable Boolean query branch is scoped to
// both a GitHub repository and pull requests, matching the frontend decoder.
func deckQueryQualifiers(query string) (bool, bool) {
	branches, valid := deckQueryBranches(query)
	if !valid {
		return false, false
	}
	repositories := make(map[string]struct{})
	for _, branch := range branches {
		if !branch.hasPullRequestQualifier {
			return false, false
		}
		if len(branch.repositories) == 0 {
			return true, false
		}
		for repository := range branch.repositories {
			repositories[repository] = struct{}{}
			if len(repositories) > 10 {
				return true, false
			}
		}
	}
	return true, len(repositories) > 0
}

func deckQueryHasPositivePullRequestQualifier(query string) bool {
	branches, valid := deckQueryBranches(query)
	if !valid || len(branches) == 0 {
		return false
	}
	for _, branch := range branches {
		if !branch.hasPullRequestQualifier {
			return false
		}
	}
	return true
}

func deckQueryHasExactRepositoryQualifier(query string, repository string) bool {
	branches, valid := deckQueryBranches(query)
	if !valid {
		return false
	}
	_, valid = deckRepositoryQualifier("repo:" + repository)
	if !valid {
		return false
	}
	for _, branch := range branches {
		if _, exists := branch.repositories[strings.ToLower(repository)]; exists {
			return true
		}
	}
	return false
}

func appendSettingsDeckQualifier(query string, qualifier string) string {
	if deckQueryHasBooleanSyntax(query) {
		query = "(" + query + ")"
	}
	if query == "" {
		return qualifier
	}
	return query + " " + qualifier
}

func deckQueryBranches(query string) ([]deckQueryBranch, bool) {
	tokens, valid := deckBooleanTokens(query)
	if !valid || len(tokens) == 0 {
		return nil, false
	}
	index := 0
	peek := func() *deckBooleanToken {
		if index >= len(tokens) {
			return nil
		}
		return &tokens[index]
	}
	isOperator := func(token *deckBooleanToken, value string) bool {
		return token != nil && token.kind == deckBooleanTerm && strings.EqualFold(token.value, value)
	}
	isPrimary := func(token *deckBooleanToken) bool {
		return token != nil && (token.kind == deckBooleanOpen || token.kind == deckBooleanNot || token.kind == deckBooleanTerm && !isOperator(token, "and") && !isOperator(token, "or"))
	}
	combineAnd := func(left []deckQueryBranch, right []deckQueryBranch) ([]deckQueryBranch, bool) {
		combined := make([]deckQueryBranch, 0, len(left)*len(right))
		for _, leftBranch := range left {
			for _, rightBranch := range right {
				repositories := make(map[string]struct{}, len(leftBranch.repositories)+len(rightBranch.repositories))
				for repository := range leftBranch.repositories {
					repositories[repository] = struct{}{}
				}
				for repository := range rightBranch.repositories {
					repositories[repository] = struct{}{}
				}
				combined = append(combined, deckQueryBranch{repositories: repositories, hasPullRequestQualifier: leftBranch.hasPullRequestQualifier || rightBranch.hasPullRequestQualifier})
				if len(combined) > deckQueryBranchLimit {
					return nil, false
				}
			}
		}
		return combined, true
	}
	var parseOr func() ([]deckQueryBranch, bool)
	var parseAnd func() ([]deckQueryBranch, bool)
	var parsePrimary func() ([]deckQueryBranch, bool)
	parsePrimary = func() ([]deckQueryBranch, bool) {
		token := peek()
		if token != nil && token.kind == deckBooleanNot {
			index++
			if _, valid := parsePrimary(); !valid {
				return nil, false
			}
			return []deckQueryBranch{{repositories: map[string]struct{}{}}}, true
		}
		if token != nil && token.kind == deckBooleanOpen {
			index++
			nested, valid := parseOr()
			if !valid || peek() == nil || peek().kind != deckBooleanClose {
				return nil, false
			}
			index++
			return nested, true
		}
		if token == nil || token.kind != deckBooleanTerm || isOperator(token, "and") || isOperator(token, "or") {
			return nil, false
		}
		index++
		repository, valid := deckRepositoryQualifier(token.value)
		if !valid {
			return nil, false
		}
		repositories := make(map[string]struct{})
		if repository != "" {
			repositories[repository] = struct{}{}
		}
		return []deckQueryBranch{{repositories: repositories, hasPullRequestQualifier: strings.EqualFold(token.value, "is:pr")}}, true
	}
	parseAnd = func() ([]deckQueryBranch, bool) {
		result, valid := parsePrimary()
		if !valid {
			return nil, false
		}
		for {
			if isOperator(peek(), "and") {
				index++
			} else if !isPrimary(peek()) {
				break
			}
			next, valid := parsePrimary()
			if !valid {
				return nil, false
			}
			result, valid = combineAnd(result, next)
			if !valid {
				return nil, false
			}
		}
		return result, true
	}
	parseOr = func() ([]deckQueryBranch, bool) {
		result, valid := parseAnd()
		if !valid {
			return nil, false
		}
		for isOperator(peek(), "or") {
			index++
			next, valid := parseAnd()
			if !valid || len(result)+len(next) > deckQueryBranchLimit {
				return nil, false
			}
			result = append(result, next...)
		}
		return result, true
	}
	branches, valid := parseOr()
	return branches, valid && index == len(tokens)
}

// deckRepositoryQualifier returns an empty string for a non-repository term.
func deckRepositoryQualifier(value string) (string, bool) {
	if len(value) < len("repo:") || !strings.EqualFold(value[:len("repo:")], "repo:") {
		return "", true
	}
	repository := value[len("repo:"):]
	if strings.Count(repository, "/") != 1 || strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return "", false
	}
	parts := strings.SplitN(repository, "/", 2)
	if !validGitHubOwnerIdentifier(parts[0]) || !githubRepositoryIdentifier.MatchString(parts[1]) {
		return "", false
	}
	return strings.ToLower(repository), true
}

func deckBooleanTokens(query string) ([]deckBooleanToken, bool) {
	tokens := make([]deckBooleanToken, 0)
	for index := 0; index < len(query); {
		character, width := utf8.DecodeRuneInString(query[index:])
		if deckQueryWhitespace(character) {
			index += width
			continue
		}
		if character == '(' {
			tokens = append(tokens, deckBooleanToken{kind: deckBooleanOpen})
			index += width
			continue
		}
		if character == ')' {
			tokens = append(tokens, deckBooleanToken{kind: deckBooleanClose})
			index += width
			continue
		}
		var value strings.Builder
		quoted := false
		escaped := false
		for index < len(query) {
			next, nextWidth := utf8.DecodeRuneInString(query[index:])
			if escaped {
				value.WriteString(query[index : index+nextWidth])
				escaped = false
				index += nextWidth
				continue
			}
			if quoted {
				value.WriteString(query[index : index+nextWidth])
				if next == '\\' {
					escaped = true
				} else if next == '"' {
					quoted = false
				}
				index += nextWidth
				continue
			}
			if next == '"' {
				value.WriteString(query[index : index+nextWidth])
				quoted = true
				index += nextWidth
				continue
			}
			if deckQueryWhitespace(next) || next == '(' || next == ')' {
				break
			}
			value.WriteString(query[index : index+nextWidth])
			index += nextWidth
		}
		if quoted || value.Len() == 0 {
			return nil, false
		}
		term := value.String()
		if strings.EqualFold(term, "not") {
			tokens = append(tokens, deckBooleanToken{kind: deckBooleanNot})
		} else {
			tokens = append(tokens, deckBooleanToken{kind: deckBooleanTerm, value: term})
		}
	}
	return tokens, true
}

// deckQueryWhitespace matches ECMAScript's explicit Deck query whitespace set.
func deckQueryWhitespace(character rune) bool {
	switch character {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return character >= '\u2000' && character <= '\u200a'
}

func validGitHubOwnerIdentifier(value string) bool {
	return githubOwnerIdentifier.MatchString(value) && !strings.HasPrefix(value, "-") && !strings.HasSuffix(value, "-") && !strings.Contains(value, "--")
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
		} else if deckQueryWhitespace(character) {
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

func hasStructuredURLMappingShape(value any) bool {
	mappings, err := settingsArray(value, "$.urlMappings")
	if err != nil {
		return false
	}
	for _, entry := range mappings {
		mapping, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if _, exists := mapping["id"]; exists {
			return true
		}
	}
	return false
}

func hasCurrentDeckShape(value any) bool {
	deck, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, ok = deck["name"]
	return ok
}

func hasStructuredDesktopShortcutShape(value any) bool {
	shortcuts, ok := value.(map[string]any)
	if !ok {
		return false
	}
	desktop, ok := shortcuts["desktop"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = desktop["shell.command-palette"].(map[string]any)
	return ok
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

func validateSettingsUploads(value any, bodyVersion int64) error {
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
	fields := []string{"profileRef", "bucket", "endpoint", "region", "publicBaseUrl"}
	if bodyVersion >= settingsSchemaVersion {
		fields = []string{"profileRef", "name", "endpoint", "accountId", "bucket", "publicBaseUrl", "prefix"}
	}
	r2, err := settingsObject(uploads["r2"], "$.uploads.r2", fields...)
	if err != nil {
		return err
	}
	profileRef, err := settingsText(r2["profileRef"], "$.uploads.r2.profileRef", false)
	if err != nil || !settingsProfileRef.MatchString(profileRef) {
		return errors.New("$.uploads.r2.profileRef is invalid")
	}
	if bodyVersion >= settingsSchemaVersion {
		name, err := settingsText(r2["name"], "$.uploads.r2.name", false)
		if err != nil || strings.TrimSpace(name) != name || settingsTextLength(name) > 80 {
			return errors.New("$.uploads.r2.name must be a trimmed string of at most 80 characters")
		}
	}
	for _, field := range []string{"bucket"} {
		value, err := settingsText(r2[field], "$.uploads.r2."+field, false)
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(value) {
			return fmt.Errorf("$.uploads.r2.%s is invalid", field)
		}
	}
	if bodyVersion < settingsSchemaVersion {
		if _, err := settingsText(r2["region"], "$.uploads.r2.region", false); err != nil {
			return err
		}
	} else {
		if r2["accountId"] != nil {
			accountID, err := settingsText(r2["accountId"], "$.uploads.r2.accountId", false)
			if err != nil || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(accountID) {
				return errors.New("$.uploads.r2.accountId is invalid")
			}
		}
		prefix, ok := r2["prefix"].(string)
		if !ok || len(prefix) > 512 || strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") || strings.Contains(prefix, `\`) {
			return errors.New("$.uploads.r2.prefix must be an empty or normalized relative object-key prefix")
		}
		if prefix != "" {
			for _, segment := range strings.Split(prefix, "/") {
				if segment == "" || segment == "." || segment == ".." {
					return errors.New("$.uploads.r2.prefix must be an empty or normalized relative object-key prefix")
				}
			}
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
			if sensitiveSettingsKey.MatchString(key) && !isContractedShortcutAction(path, key) {
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

func isContractedShortcutAction(path string, key string) bool {
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
