// Package query owns Deck's canonical raw GitHub PR query and the recognized
// visual-builder projection. Raw text stays authoritative.
package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxQueryBytes = 8192

// Parse canonicalizes raw whitespace, requires the pull-request discriminator,
// and derives the recognized builder projection.
func Parse(raw string) (*deckv1.ViewQuery, error) {
	if len(raw) == 0 || len(raw) > maxQueryBytes {
		return nil, errors.New("query: raw query length is invalid")
	}
	tokens, err := tokenize(raw)
	if err != nil || len(tokens) == 0 {
		return nil, errors.New("query: raw query is invalid")
	}
	hasPullRequest := false
	builder := &deckv1.QueryBuilder{}
	for _, token := range tokens {
		if strings.EqualFold(token, "is:pr") {
			hasPullRequest = true
		}
		clause, ok := parseClause(token)
		if !ok {
			builder.UnrecognizedRawClauses = append(builder.UnrecognizedRawClauses, token)
			continue
		}
		builder.Clauses = append(builder.Clauses, clause)
	}
	if !hasPullRequest {
		tokens = append([]string{"is:pr"}, tokens...)
		builder.UnrecognizedRawClauses = append([]string{"is:pr"}, builder.UnrecognizedRawClauses...)
	}
	return &deckv1.ViewQuery{
		RawQuery: strings.Join(tokens, " "),
		Builder:  builder,
	}, nil
}

// Apply accepts a raw edit as authoritative. When raw is unchanged (or
// omitted), a builder edit rewrites only recognized clauses and carries every
// unknown raw token forward unchanged.
func Apply(existing, input *deckv1.ViewQuery) (*deckv1.ViewQuery, error) {
	if input == nil {
		return nil, errors.New("query: input is required")
	}
	if existing == nil {
		return Parse(input.RawQuery)
	}
	if input.RawQuery != "" {
		parsed, err := Parse(input.RawQuery)
		if err != nil {
			return nil, err
		}
		if parsed.RawQuery != existing.RawQuery {
			return parsed, nil
		}
	}
	if input.Builder == nil {
		return proto.Clone(existing).(*deckv1.ViewQuery), nil
	}
	unknown := []string(nil)
	parsedExisting, err := Parse(existing.RawQuery)
	if err != nil {
		return nil, err
	}
	unknown = append(unknown, parsedExisting.Builder.UnrecognizedRawClauses...)
	rendered := make([]string, 0, len(input.Builder.Clauses)+len(unknown))
	for _, clause := range input.Builder.Clauses {
		token, err := renderClause(clause)
		if err != nil {
			return nil, err
		}
		rendered = append(rendered, token...)
	}
	rendered = append(rendered, unknown...)
	return Parse(strings.Join(rendered, " "))
}

// ResolveViewer replaces only recognized @me identity values. The persisted
// definition remains unchanged, so one organization view resolves differently
// for each viewer.
func ResolveViewer(raw, login string) (string, error) {
	if login == "" || strings.ContainsAny(login, " \t\r\n:\"") {
		return "", errors.New("query: viewer login is invalid")
	}
	parsed, err := Parse(raw)
	if err != nil {
		return "", err
	}
	tokens, err := tokenize(parsed.RawQuery)
	if err != nil {
		return "", err
	}
	for index, token := range tokens {
		negated := strings.HasPrefix(token, "-")
		body := strings.TrimPrefix(token, "-")
		key, value, ok := strings.Cut(body, ":")
		if !ok || !isRelativeIdentityKey(strings.ToLower(key)) ||
			!strings.EqualFold(unquote(value), "@me") {
			continue
		}
		prefix := ""
		if negated {
			prefix = "-"
		}
		tokens[index] = prefix + key + ":" + quote(login)
	}
	return strings.Join(tokens, " "), nil
}

func isRelativeIdentityKey(key string) bool {
	switch key {
	case "author", "assignee", "review-requested":
		return true
	default:
		return false
	}
}

func tokenize(raw string) ([]string, error) {
	var tokens []string
	var token strings.Builder
	quoted := false
	escaped := false
	flush := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	for _, character := range raw {
		switch {
		case escaped:
			token.WriteRune(character)
			escaped = false
		case character == '\\' && quoted:
			token.WriteRune(character)
			escaped = true
		case character == '"':
			token.WriteRune(character)
			quoted = !quoted
		case unicode.IsSpace(character) && !quoted:
			flush()
		case unicode.IsControl(character):
			return nil, errors.New("query: control character")
		default:
			token.WriteRune(character)
		}
	}
	if quoted || escaped {
		return nil, errors.New("query: unterminated quote")
	}
	flush()
	return tokens, nil
}

func parseClause(token string) (*deckv1.QueryClause, bool) {
	negated := strings.HasPrefix(token, "-")
	body := strings.TrimPrefix(token, "-")
	key, rawValue, ok := strings.Cut(body, ":")
	if !ok {
		return nil, false
	}
	key = strings.ToLower(key)
	value := unquote(rawValue)
	if value == "" {
		return nil, false
	}
	clause := &deckv1.QueryClause{Negated: negated}
	switch key {
	case "org", "user":
		clause.Clause = &deckv1.QueryClause_Owner{Owner: &deckv1.OwnerQualifier{Owner: value}}
	case "repo":
		owner, repository, ok := strings.Cut(value, "/")
		if !ok || owner == "" || repository == "" {
			return nil, false
		}
		clause.Clause = &deckv1.QueryClause_Repository{Repository: &deckv1.RepositoryQualifier{
			Owner: owner, Repository: repository,
		}}
	case "author":
		clause.Clause = &deckv1.QueryClause_Author{Author: &deckv1.AuthorQualifier{
			Author: identity(value, false),
		}}
	case "assignee":
		clause.Clause = &deckv1.QueryClause_Assignee{Assignee: &deckv1.AssigneeQualifier{
			Assignee: identity(value, false),
		}}
	case "review-requested":
		clause.Clause = &deckv1.QueryClause_Reviewer{Reviewer: &deckv1.ReviewerQualifier{
			Reviewer: identity(value, false),
		}}
	case "team-review-requested":
		clause.Clause = &deckv1.QueryClause_Reviewer{Reviewer: &deckv1.ReviewerQualifier{
			Reviewer: identity(value, true),
		}}
	case "label":
		clause.Clause = &deckv1.QueryClause_Label{Label: &deckv1.LabelQualifier{Label: value}}
	case "is":
		var state deckv1.PullRequestState
		switch strings.ToLower(value) {
		case "open":
			state = deckv1.PullRequestState_PULL_REQUEST_STATE_OPEN
		case "closed":
			state = deckv1.PullRequestState_PULL_REQUEST_STATE_CLOSED
		case "draft":
			state = deckv1.PullRequestState_PULL_REQUEST_STATE_DRAFT
		default:
			return nil, false
		}
		clause.Clause = &deckv1.QueryClause_State{State: &deckv1.StateQualifier{State: state}}
	case "base":
		clause.Clause = &deckv1.QueryClause_BaseBranch{BaseBranch: &deckv1.BaseBranchQualifier{Branch: value}}
	case "head":
		clause.Clause = &deckv1.QueryClause_HeadBranch{HeadBranch: &deckv1.HeadBranchQualifier{Branch: value}}
	case "review":
		var decision deckv1.ReviewDecision
		switch strings.ToLower(value) {
		case "required":
			decision = deckv1.ReviewDecision_REVIEW_DECISION_REVIEW_REQUIRED
		case "changes_requested":
			decision = deckv1.ReviewDecision_REVIEW_DECISION_CHANGES_REQUESTED
		case "approved":
			decision = deckv1.ReviewDecision_REVIEW_DECISION_APPROVED
		default:
			return nil, false
		}
		clause.Clause = &deckv1.QueryClause_ReviewDecision{
			ReviewDecision: &deckv1.ReviewDecisionQualifier{Decision: decision},
		}
	case "status":
		var state deckv1.ChecksState
		switch strings.ToLower(value) {
		case "pending":
			state = deckv1.ChecksState_CHECKS_STATE_PENDING
		case "success":
			state = deckv1.ChecksState_CHECKS_STATE_SUCCESS
		case "failure":
			state = deckv1.ChecksState_CHECKS_STATE_FAILURE
		default:
			return nil, false
		}
		clause.Clause = &deckv1.QueryClause_Checks{Checks: &deckv1.ChecksQualifier{State: state}}
	case "updated":
		rangeClause, ok := parseUpdated(value)
		if !ok {
			return nil, false
		}
		clause.Clause = &deckv1.QueryClause_UpdatedRange{UpdatedRange: rangeClause}
	default:
		return nil, false
	}
	return clause, true
}

func identity(value string, team bool) *deckv1.QueryIdentity {
	if strings.EqualFold(value, "@me") && !team {
		return &deckv1.QueryIdentity{Kind: deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_VIEWER}
	}
	if team {
		organization, slug, ok := strings.Cut(value, "/")
		if ok && organization != "" && slug != "" {
			return &deckv1.QueryIdentity{
				Kind:         deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_TEAM,
				Organization: organization,
				TeamSlug:     slug,
			}
		}
	}
	return &deckv1.QueryIdentity{
		Kind:  deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_USER,
		Login: value,
	}
}

func parseUpdated(value string) (*deckv1.UpdatedRangeQualifier, bool) {
	result := &deckv1.UpdatedRangeQualifier{}
	switch {
	case strings.HasPrefix(value, ">="):
		result.UpdatedAfter = parseTime(strings.TrimPrefix(value, ">="))
	case strings.HasPrefix(value, "<="):
		result.UpdatedBefore = parseTime(strings.TrimPrefix(value, "<="))
	case strings.Contains(value, ".."):
		after, before, _ := strings.Cut(value, "..")
		result.UpdatedAfter = parseTime(after)
		result.UpdatedBefore = parseTime(before)
	default:
		return nil, false
	}
	if (result.UpdatedAfter == nil || !result.UpdatedAfter.IsValid()) &&
		(result.UpdatedBefore == nil || !result.UpdatedBefore.IsValid()) {
		return nil, false
	}
	return result, true
}

func parseTime(value string) *timestamppb.Timestamp {
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return timestamppb.New(parsed.UTC())
		}
	}
	return nil
}

func renderClause(clause *deckv1.QueryClause) ([]string, error) {
	if clause == nil || clause.Clause == nil {
		return nil, errors.New("query: empty builder clause")
	}
	prefix := ""
	if clause.Negated {
		prefix = "-"
	}
	var key, value string
	switch typed := clause.Clause.(type) {
	case *deckv1.QueryClause_Owner:
		key, value = "org", typed.Owner.GetOwner()
	case *deckv1.QueryClause_Repository:
		key, value = "repo", typed.Repository.GetOwner()+"/"+typed.Repository.GetRepository()
	case *deckv1.QueryClause_Author:
		key, value = "author", renderIdentity(typed.Author.GetAuthor())
	case *deckv1.QueryClause_Assignee:
		key, value = "assignee", renderIdentity(typed.Assignee.GetAssignee())
	case *deckv1.QueryClause_Reviewer:
		identity := typed.Reviewer.GetReviewer()
		if identity.GetKind() == deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_TEAM {
			key, value = "team-review-requested", renderIdentity(identity)
		} else {
			key, value = "review-requested", renderIdentity(identity)
		}
	case *deckv1.QueryClause_Label:
		key, value = "label", typed.Label.GetLabel()
	case *deckv1.QueryClause_State:
		key = "is"
		value = map[deckv1.PullRequestState]string{
			deckv1.PullRequestState_PULL_REQUEST_STATE_OPEN:   "open",
			deckv1.PullRequestState_PULL_REQUEST_STATE_CLOSED: "closed",
			deckv1.PullRequestState_PULL_REQUEST_STATE_DRAFT:  "draft",
		}[typed.State.GetState()]
	case *deckv1.QueryClause_BaseBranch:
		key, value = "base", typed.BaseBranch.GetBranch()
	case *deckv1.QueryClause_HeadBranch:
		key, value = "head", typed.HeadBranch.GetBranch()
	case *deckv1.QueryClause_ReviewDecision:
		key = "review"
		value = map[deckv1.ReviewDecision]string{
			deckv1.ReviewDecision_REVIEW_DECISION_REVIEW_REQUIRED:   "required",
			deckv1.ReviewDecision_REVIEW_DECISION_CHANGES_REQUESTED: "changes_requested",
			deckv1.ReviewDecision_REVIEW_DECISION_APPROVED:          "approved",
		}[typed.ReviewDecision.GetDecision()]
	case *deckv1.QueryClause_Checks:
		key = "status"
		value = map[deckv1.ChecksState]string{
			deckv1.ChecksState_CHECKS_STATE_PENDING: "pending",
			deckv1.ChecksState_CHECKS_STATE_SUCCESS: "success",
			deckv1.ChecksState_CHECKS_STATE_FAILURE: "failure",
		}[typed.Checks.GetState()]
	case *deckv1.QueryClause_UpdatedRange:
		rangeValue := typed.UpdatedRange
		if rangeValue.GetUpdatedAfter() != nil && rangeValue.GetUpdatedBefore() != nil {
			return []string{prefix + "updated:" +
				formatTime(rangeValue.GetUpdatedAfter()) + ".." +
				formatTime(rangeValue.GetUpdatedBefore())}, nil
		}
		key = "updated"
		if rangeValue.GetUpdatedAfter() != nil {
			value = ">=" + formatTime(rangeValue.GetUpdatedAfter())
		} else if rangeValue.GetUpdatedBefore() != nil {
			value = "<=" + formatTime(rangeValue.GetUpdatedBefore())
		}
	default:
		return nil, errors.New("query: unsupported builder clause")
	}
	if key == "" || value == "" {
		return nil, errors.New("query: invalid builder clause")
	}
	return []string{prefix + key + ":" + quote(value)}, nil
}

func renderIdentity(identity *deckv1.QueryIdentity) string {
	if identity == nil {
		return ""
	}
	switch identity.Kind {
	case deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_VIEWER:
		return "@me"
	case deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_USER:
		return identity.Login
	case deckv1.QueryIdentityKind_QUERY_IDENTITY_KIND_TEAM:
		return identity.Organization + "/" + identity.TeamSlug
	default:
		return ""
	}
}

func formatTime(value *timestamppb.Timestamp) string {
	if value == nil || !value.IsValid() {
		return ""
	}
	timestamp := value.AsTime().UTC()
	if timestamp.Hour() == 0 && timestamp.Minute() == 0 &&
		timestamp.Second() == 0 && timestamp.Nanosecond() == 0 {
		return timestamp.Format(time.DateOnly)
	}
	return timestamp.Format(time.RFC3339)
}

func quote(value string) string {
	if value != "" && !strings.ContainsFunc(value, unicode.IsSpace) &&
		!strings.ContainsAny(value, `"\\`) {
		return value
	}
	return strconv.Quote(value)
}

func unquote(value string) string {
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		if err == nil {
			return decoded
		}
	}
	return value
}

func Debug(query *deckv1.ViewQuery) string {
	if query == nil {
		return ""
	}
	return fmt.Sprintf("%d recognized, %d preserved",
		len(query.GetBuilder().GetClauses()),
		len(query.GetBuilder().GetUnrecognizedRawClauses()))
}
