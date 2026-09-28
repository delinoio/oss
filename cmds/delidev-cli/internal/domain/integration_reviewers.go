package domain

import "strings"

type RepositoryPermission string

const (
	PermissionNone     RepositoryPermission = "NONE"
	PermissionRead     RepositoryPermission = "READ"
	PermissionTriage   RepositoryPermission = "TRIAGE"
	PermissionWrite    RepositoryPermission = "WRITE"
	PermissionMaintain RepositoryPermission = "MAINTAIN"
	PermissionAdmin    RepositoryPermission = "ADMIN"
)

func (p RepositoryPermission) rank() int {
	switch p {
	case PermissionNone:
		return 0
	case PermissionRead:
		return 1
	case PermissionTriage:
		return 2
	case PermissionWrite:
		return 3
	case PermissionMaintain:
		return 4
	case PermissionAdmin:
		return 5
	default:
		return -1
	}
}

// Custom roles retain only the interval proved by GitHub's documented legacy
// base permission. A custom name never becomes a standard role by inference.
func PermissionInterval(legacy, role string) (RepositoryPermission, RepositoryPermission, bool) {
	standard := map[string]struct {
		base  string
		level RepositoryPermission
	}{
		"read": {"read", PermissionRead}, "triage": {"read", PermissionTriage}, "write": {"write", PermissionWrite}, "maintain": {"write", PermissionMaintain}, "admin": {"admin", PermissionAdmin}, "none": {"none", PermissionNone},
	}
	if value, exists := standard[role]; exists {
		return value.level, value.level, value.base == legacy
	}
	if Text(role, "repository role", 256, false) != nil || strings.ContainsAny(role, "\r\n") {
		return "", "", false
	}
	switch legacy {
	case "none":
		if role != "" {
			return "", "", false
		}
		return PermissionNone, PermissionNone, true
	case "read":
		return PermissionRead, PermissionTriage, true
	case "write":
		return PermissionWrite, PermissionMaintain, true
	case "admin":
		return PermissionAdmin, PermissionAdmin, true
	default:
		return "", "", false
	}
}

type ReviewerPermission struct {
	Access           IntegrationAccessState `json:"access"`
	LegacyPermission string                 `json:"legacy_permission,omitempty"`
	RoleName         string                 `json:"role_name,omitempty"`
	Minimum          RepositoryPermission   `json:"minimum,omitempty"`
	Maximum          RepositoryPermission   `json:"maximum,omitempty"`
}

func reviewerAccess(state IntegrationAccessState) bool {
	switch state {
	case IntegrationAccessAvailable, IntegrationAccessInvalidToken, IntegrationAccessDenied, IntegrationAccessNotFound, IntegrationAccessSSORequired, IntegrationAccessRateLimited, IntegrationAccessUnavailable, IntegrationAccessNotEvaluated:
		return true
	default:
		return false
	}
}
func (p ReviewerPermission) Validate() error {
	if !reviewerAccess(p.Access) {
		return invalidPRObservation()
	}
	if p.Access != IntegrationAccessAvailable {
		if p.LegacyPermission != "" || p.RoleName != "" || p.Minimum != "" || p.Maximum != "" {
			return invalidPRObservation()
		}
		return nil
	}
	minimum, maximum, ok := PermissionInterval(p.LegacyPermission, p.RoleName)
	if !ok || p.Minimum != minimum || p.Maximum != maximum {
		return invalidPRObservation()
	}
	return nil
}

type PRReviewerIdentity struct {
	Author         FeedbackAuthor         `json:"author"`
	IdentityAccess IntegrationAccessState `json:"identity_access"`
	Identity       *RepositoryActor       `json:"identity,omitempty"`
	Permission     ReviewerPermission     `json:"permission"`
}

func (v PRReviewerIdentity) Validate() error {
	if v.Author.Validate() != nil || !reviewerAccess(v.IdentityAccess) || v.Permission.Validate() != nil {
		return invalidPRObservation()
	}
	if v.IdentityAccess != IntegrationAccessAvailable {
		if v.Identity != nil || v.Permission.Access != IntegrationAccessNotEvaluated {
			return invalidPRObservation()
		}
		return nil
	}
	if v.Identity == nil || v.Identity.Validate() != nil || (v.Identity.Kind != RepositoryUser && v.Identity.Kind != RepositoryBot) || v.Identity.Kind != v.Author.Kind || v.Identity.ID != v.Author.ID || v.Identity.NodeID != v.Author.NodeID {
		return invalidPRObservation()
	}
	return nil
}

type FeedbackAppState string

const (
	FeedbackAppAttributed FeedbackAppState = "attributed"
	FeedbackAppNone       FeedbackAppState = "none"
	FeedbackAppUnknown    FeedbackAppState = "unknown"
)

type FeedbackApplication struct {
	FeedbackNodeID string                 `json:"feedback_node_id"`
	ContentVersion string                 `json:"content_version"`
	Access         IntegrationAccessState `json:"access"`
	State          FeedbackAppState       `json:"state"`
	Application    *CheckApplication      `json:"application,omitempty"`
}

func (v FeedbackApplication) Validate(entry PRFeedback) error {
	if v.FeedbackNodeID != entry.NodeID || v.ContentVersion != entry.ContentVersion || !reviewerAccess(v.Access) {
		return invalidPRObservation()
	}
	if entry.Kind != PRConversationComment && (v.Access != IntegrationAccessNotEvaluated || v.State != FeedbackAppUnknown) {
		return invalidPRObservation()
	}
	switch v.State {
	case FeedbackAppUnknown:
		if v.Application != nil {
			return invalidPRObservation()
		}
	case FeedbackAppNone:
		if v.Access != IntegrationAccessAvailable || v.Application != nil {
			return invalidPRObservation()
		}
	case FeedbackAppAttributed:
		a := v.Application
		if v.Access != IntegrationAccessAvailable || a == nil || !PositiveDecimal(a.ID) || Text(a.NodeID, "App node", 256, true) != nil || Text(a.Slug, "App slug", 100, true) != nil || strings.ContainsAny(a.Slug, "\r\n") {
			return invalidPRObservation()
		}
	default:
		return invalidPRObservation()
	}
	return nil
}

type PullRequestReviewers struct {
	Feedback     PullRequestFeedback   `json:"feedback"`
	Actors       []PRReviewerIdentity  `json:"actors"`
	Applications []FeedbackApplication `json:"applications"`
}

func (v PullRequestReviewers) Validate(item RepositoryItem) error {
	if v.Feedback.Validate(item) != nil || v.Actors == nil || v.Applications == nil || len(v.Actors) > MaxPRFeedback || len(v.Applications) != len(v.Feedback.Entries) {
		return invalidPRObservation()
	}
	authors := map[string]FeedbackAuthor{}
	entries := map[string]PRFeedback{}
	for _, entry := range v.Feedback.Entries {
		entries[entry.NodeID] = entry
		if entry.Author != nil {
			if old, exists := authors[entry.Author.NodeID]; exists && old != *entry.Author {
				return invalidPRObservation()
			}
			authors[entry.Author.NodeID] = *entry.Author
		}
	}
	if len(authors) != len(v.Actors) {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	for _, actor := range v.Actors {
		author, exists := authors[actor.Author.NodeID]
		if !exists || seen[author.NodeID] || actor.Author != author || actor.Validate() != nil {
			return invalidPRObservation()
		}
		seen[author.NodeID] = true
	}
	seen = map[string]bool{}
	for _, app := range v.Applications {
		entry, exists := entries[app.FeedbackNodeID]
		if !exists || seen[entry.NodeID] || app.Validate(entry) != nil {
			return invalidPRObservation()
		}
		seen[entry.NodeID] = true
	}
	return nil
}

type ReviewerSelectorKind string

const (
	ReviewerUser              ReviewerSelectorKind = "user"
	ReviewerBot               ReviewerSelectorKind = "bot"
	ReviewerApp               ReviewerSelectorKind = "app"
	ReviewerMinimumPermission ReviewerSelectorKind = "minimum-permission"
)

type ReviewerSelector struct {
	Kind       ReviewerSelectorKind `json:"kind"`
	ID         string               `json:"id,omitempty"`
	NodeID     string               `json:"node_id,omitempty"`
	Permission RepositoryPermission `json:"permission,omitempty"`
}

func ValidateReviewerSelectors(selectors []ReviewerSelector) error {
	if len(selectors) > 100 {
		return Fail(InvalidArgument, "Too many reviewer selectors.", "Use at most 100 exact selectors.")
	}
	seen := map[string]bool{}
	for _, s := range selectors {
		valid, key := false, ""
		switch s.Kind {
		case ReviewerUser, ReviewerBot, ReviewerApp:
			valid = PositiveDecimal(s.ID) && Text(s.NodeID, "reviewer node", 256, true) == nil && s.Permission == ""
			key = string(s.Kind) + ":" + s.ID
		case ReviewerMinimumPermission:
			valid = s.ID == "" && s.NodeID == "" && s.Permission.rank() > 0
			key = string(s.Kind) + ":" + string(s.Permission)
		}
		if !valid || seen[key] {
			return Fail(InvalidArgument, "Invalid or duplicate reviewer selector.", "Select an exact GitHub user, bot or App identity, or a minimum repository permission.")
		}
		seen[key] = true
	}
	return nil
}

type ReviewerMatch string

const (
	ReviewerMatches      ReviewerMatch = "matches"
	ReviewerDoesNotMatch ReviewerMatch = "does-not-match"
	ReviewerUnknown      ReviewerMatch = "unknown"
)

// Match reports only what this read proves. An execution controller must collect
// fresh evidence and recheck its own current policy immediately before acting.
func (v PullRequestReviewers) Match(feedbackNodeID string, selectors []ReviewerSelector) ReviewerMatch {
	var entry *PRFeedback
	for i := range v.Feedback.Entries {
		if v.Feedback.Entries[i].NodeID == feedbackNodeID {
			if entry != nil {
				return ReviewerUnknown
			}
			entry = &v.Feedback.Entries[i]
		}
	}
	if entry == nil {
		return ReviewerUnknown
	}
	if ValidateReviewerSelectors(selectors) != nil {
		return ReviewerUnknown
	}
	if len(selectors) == 0 {
		return ReviewerDoesNotMatch
	}
	var actor *PRReviewerIdentity
	for i := range v.Actors {
		if entry.Author != nil && v.Actors[i].Author.NodeID == entry.Author.NodeID {
			actor = &v.Actors[i]
			break
		}
	}
	if actor == nil || actor.IdentityAccess != IntegrationAccessAvailable || actor.Validate() != nil {
		return ReviewerUnknown
	}
	unknown := false
	for _, selector := range selectors {
		switch selector.Kind {
		case ReviewerUser, ReviewerBot:
			kind := RepositoryUser
			if selector.Kind == ReviewerBot {
				kind = RepositoryBot
			}
			if actor.Identity.Kind == kind && actor.Identity.ID == selector.ID && actor.Identity.NodeID == selector.NodeID {
				return ReviewerMatches
			}
		case ReviewerMinimumPermission:
			if actor.Permission.Access != IntegrationAccessAvailable {
				unknown = true
				continue
			}
			if selector.Permission.rank() <= actor.Permission.Minimum.rank() {
				return ReviewerMatches
			}
			if selector.Permission.rank() <= actor.Permission.Maximum.rank() {
				unknown = true
			}
		case ReviewerApp:
			found := false
			for _, app := range v.Applications {
				if app.FeedbackNodeID != entry.NodeID {
					continue
				}
				found = true
				if app.Validate(*entry) != nil || app.State == FeedbackAppUnknown {
					unknown = true
					continue
				}
				if app.State == FeedbackAppAttributed && app.Application.ID == selector.ID && app.Application.NodeID == selector.NodeID {
					return ReviewerMatches
				}
			}
			if !found {
				unknown = true
			}
		}
	}
	if unknown {
		return ReviewerUnknown
	}
	return ReviewerDoesNotMatch
}
