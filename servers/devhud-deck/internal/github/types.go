// Package github implements Deck's GitHub.com-only provider boundary.
package github

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	WebOrigin = "https://github.com"
	APIOrigin = "https://api.github.com"

	OAuthAuthorizePath = "/login/oauth/authorize"
	OAuthTokenPath     = "/login/oauth/access_token"
	GraphQLPath        = "/graphql"
)

var (
	ErrInvalidConfiguration = errors.New("deck github: invalid configuration")
	ErrUnsupportedHost      = errors.New("deck github: unsupported host")
	ErrInvalidSignature     = errors.New("deck github: invalid signature")
	ErrExpiredState         = errors.New("deck github: callback state expired")
	ErrPermissionDenied     = errors.New("deck github: permission denied")
	ErrRateLimited          = errors.New("deck github: rate limited")
	ErrConcurrencyLimited   = errors.New("deck github: installation concurrency limited")
	ErrUnsupportedAction    = errors.New("deck github: unsupported action")
	ErrConfirmationRequired = errors.New("deck github: merge confirmation required")
	ErrBranchProtected      = errors.New("deck github: repository rules prevent merge")
	ErrStaleRevision        = errors.New("deck github: pull request revision is stale")
	ErrProvider             = errors.New("deck github: provider request failed")
)

type AccountKind uint8

const (
	AccountKindUnknown AccountKind = iota
	AccountKindUser
	AccountKindOrganization
)

type PermissionLevel uint8

const (
	PermissionNone PermissionLevel = iota
	PermissionRead
	PermissionWrite
	PermissionAdmin
)

type Permissions struct {
	Metadata     PermissionLevel
	PullRequests PermissionLevel
	Checks       PermissionLevel
	Members      PermissionLevel
}

// IntersectPermissions returns the effective user-to-server permission set.
// GitHub enforces the same intersection; keeping it explicit prevents action
// metadata from advertising authority held only by the app or only by a user.
func IntersectPermissions(app, user Permissions) Permissions {
	return Permissions{
		Metadata:     minimum(app.Metadata, user.Metadata),
		PullRequests: minimum(app.PullRequests, user.PullRequests),
		Checks:       minimum(app.Checks, user.Checks),
		Members:      minimum(app.Members, user.Members),
	}
}

func minimum(left, right PermissionLevel) PermissionLevel {
	if left < right {
		return left
	}
	return right
}

type OwnerBinding struct {
	Scope uint8
	ID    string
}

func (owner OwnerBinding) Validate() error {
	if (owner.Scope != 1 && owner.Scope != 2) || owner.ID == "" ||
		strings.ContainsAny(owner.ID, " \t\r\n") {
		return ErrInvalidConfiguration
	}
	return nil
}

type Installation struct {
	ID           uint64
	AccountID    uint64
	AccountLogin string
	AccountKind  AccountKind
	Permissions  Permissions
	Suspended    bool
}

type Credential struct {
	// AccessToken is always a GitHub App user authorization token. Deck never
	// accepts an installation token or a personal access token here.
	AccessToken           string
	RefreshToken          string
	ExpiresAt             time.Time
	RefreshTokenExpiresAt time.Time
}

func (credential Credential) Validate(now time.Time) error {
	if credential.AccessToken == "" ||
		strings.ContainsAny(credential.AccessToken, "\r\n") {
		return ErrPermissionDenied
	}
	if !credential.ExpiresAt.IsZero() && !credential.ExpiresAt.After(now) {
		return ErrPermissionDenied
	}
	return nil
}

type Repository struct {
	Owner string
	Name  string
}

func (repository Repository) Validate() error {
	if !safePathSegment(repository.Owner) || !safePathSegment(repository.Name) {
		return ErrUnsupportedHost
	}
	return nil
}

type PullRequestRef struct {
	Repository Repository
	Number     uint64
}

func (reference PullRequestRef) Validate() error {
	if reference.Number == 0 {
		return ErrUnsupportedAction
	}
	return reference.Repository.Validate()
}

func safePathSegment(value string) bool {
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\?#:@ \t\r\n")
}

func apiURL(path string) (*url.URL, error) {
	parsed, err := url.Parse(APIOrigin + path)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "api.github.com" ||
		parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrUnsupportedHost
	}
	return parsed, nil
}

func repositoryPath(repository Repository, suffix string) (string, error) {
	if err := repository.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("/repos/%s/%s%s",
		url.PathEscape(repository.Owner), url.PathEscape(repository.Name), suffix), nil
}

type MutationKind uint8

const (
	MutationUnknown MutationKind = iota
	MutationAssignUsers
	MutationUnassignUsers
	MutationRequestReviewers
	MutationRemoveReviewers
	MutationAddLabels
	MutationRemoveLabels
	MutationMarkDraft
	MutationMarkReady
	MutationClose
	MutationReopen
	MutationMerge
	MutationEnableAutoMerge
	MutationCancelAutoMerge
)

type MergeMethod uint8

const (
	MergeMethodUnknown MergeMethod = iota
	MergeMethodMerge
	MergeMethodSquash
	MergeMethodRebase
)

type User struct {
	Login string
}

type Team struct {
	Organization string
	Slug         string
}

type Mutation struct {
	Kind        MutationKind
	Users       []User
	Teams       []Team
	Labels      []string
	MergeMethod MergeMethod
	Confirmed   bool
}

type CandidateKind uint8

const (
	CandidateUnknown CandidateKind = iota
	CandidateUser
	CandidateTeam
	CandidateLabel
)

type Candidate struct {
	Kind  CandidateKind
	User  User
	Team  Team
	Label string
}

type Page struct {
	Cursor string
	Limit  int
}

type CandidatePage struct {
	Candidates []Candidate
	NextCursor string
	Revision   uint64
}

// SearchPullRequest is emitted only after the current user's GitHub
// authorization has been rechecked for its repository.
type SearchPullRequest struct {
	Repository Repository
	Number     uint64
	Title      string
	Author     User
	UpdatedAt  time.Time
}

type SearchPage struct {
	PullRequests []SearchPullRequest
	NextCursor   string
	// VisibleCount counts only this authorization-filtered page. Deck never
	// returns GitHub's upstream total_count because it may include results the
	// current viewer cannot access.
	VisibleCount int
	Truncated    bool
}

type ActionMetadata struct {
	Revision         uint64
	Permissions      Permissions
	Supported        map[MutationKind]bool
	AvailableMethods map[MergeMethod]bool
	NodeID           string
	HeadSHA          string
	IsDraft          bool
	IsOpen           bool
	IsMerged         bool
	AutoMergeEnabled bool
	Mergeable        bool
	MergeBlocked     bool
}

type MutationResult struct {
	Kind     MutationKind
	Revision uint64
	Metadata ActionMetadata
}
