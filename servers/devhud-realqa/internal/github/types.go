package github

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	WebOrigin     = "https://github.com"
	APIOrigin     = "https://api.github.com"
	BodyByteLimit = 60_000
)

var (
	safeNamePattern = regexp.MustCompile(`\A[A-Za-z0-9_.-]+\z`)
	nodeIDPattern   = regexp.MustCompile(`\A[A-Za-z0-9_=-]{1,255}\z`)
)

type AccountKind string

const (
	AccountKindUser         AccountKind = "User"
	AccountKindOrganization AccountKind = "Organization"
)

type OwnerKind string

const (
	OwnerKindPersonal     OwnerKind = "personal"
	OwnerKindOrganization OwnerKind = "organization"
)

type Owner struct {
	Kind OwnerKind
	ID   uuid.UUID
}

func (owner Owner) Validate() error {
	if owner.ID == uuid.Nil {
		return errors.New("realqa github: owner ID is required")
	}
	switch owner.Kind {
	case OwnerKindPersonal, OwnerKindOrganization:
		return nil
	default:
		return errors.New("realqa github: owner kind is invalid")
	}
}

type PermissionLevel string

const (
	PermissionNone  PermissionLevel = ""
	PermissionRead  PermissionLevel = "read"
	PermissionWrite PermissionLevel = "write"
)

type ProjectPermission string

const (
	ProjectPermissionNone         ProjectPermission = "none"
	ProjectPermissionRepository   ProjectPermission = "repository"
	ProjectPermissionOrganization ProjectPermission = "organization"
)

type Permissions struct {
	Issues               PermissionLevel `json:"issues"`
	Metadata             PermissionLevel `json:"metadata"`
	Contents             PermissionLevel `json:"contents"`
	RepositoryProjects   PermissionLevel `json:"repository_projects,omitempty"`
	OrganizationProjects PermissionLevel `json:"organization_projects,omitempty"`
}

func RequiredPermissions(project ProjectPermission) (Permissions, error) {
	permissions := Permissions{
		Issues: PermissionWrite, Metadata: PermissionRead, Contents: PermissionRead,
	}
	switch project {
	case ProjectPermissionNone:
	case ProjectPermissionRepository:
		permissions.RepositoryProjects = PermissionWrite
	case ProjectPermissionOrganization:
		permissions.OrganizationProjects = PermissionWrite
	default:
		return Permissions{}, errors.New("realqa github: project permission is invalid")
	}
	return permissions, nil
}

func (permissions Permissions) Validate(project ProjectPermission) error {
	required, err := RequiredPermissions(project)
	if err != nil {
		return err
	}
	if permissions.Issues != required.Issues ||
		permissions.Metadata != required.Metadata ||
		permissions.Contents != required.Contents ||
		permissions.RepositoryProjects != required.RepositoryProjects ||
		permissions.OrganizationProjects != required.OrganizationProjects {
		return errors.New("realqa github: installation permissions do not match the configured minimum")
	}
	return nil
}

type Installation struct {
	ID           int64
	AccountID    int64
	AccountLogin string
	AccountKind  AccountKind
	Permissions  Permissions
}

func (installation Installation) Validate(project ProjectPermission) error {
	if installation.ID <= 0 || installation.AccountID <= 0 ||
		!safeNamePattern.MatchString(installation.AccountLogin) {
		return errors.New("realqa github: installation identity is invalid")
	}
	switch installation.AccountKind {
	case AccountKindUser, AccountKindOrganization:
	default:
		return errors.New("realqa github: installation account kind is invalid")
	}
	return installation.Permissions.Validate(project)
}

type Repository struct {
	ID            int64
	NodeID        string
	Owner         string
	Name          string
	IssuesEnabled bool
	CanSubmit     bool
}

type RepositoryPageRequest struct {
	Query    string
	Cursor   string
	PageSize int
}

type RepositoryPage struct {
	Repositories []Repository
	NextCursor   string
	scanned      []Repository
}

func (repository Repository) Validate() error {
	if repository.ID <= 0 || !nodeIDPattern.MatchString(repository.NodeID) ||
		!safeNamePattern.MatchString(repository.Owner) ||
		!safeNamePattern.MatchString(repository.Name) {
		return errors.New("realqa github: repository identity is invalid")
	}
	return nil
}

type DefinitionKind string

const (
	DefinitionMarkdown DefinitionKind = "markdown_template"
	DefinitionForm     DefinitionKind = "issue_form"
)

type DefinitionRef struct {
	Kind DefinitionKind
	ID   string
	Name string
	Path string
	ETag string
}

type MarkdownTemplate struct {
	Definition       DefinitionRef
	TitlePrefix      string
	DefaultLabels    []string
	DefaultAssignees []string
	Body             string
}

type FormFieldKind string

const (
	FormFieldMarkdown   FormFieldKind = "markdown"
	FormFieldInput      FormFieldKind = "input"
	FormFieldTextarea   FormFieldKind = "textarea"
	FormFieldDropdown   FormFieldKind = "dropdown"
	FormFieldCheckboxes FormFieldKind = "checkboxes"
)

type FormOption struct {
	Label    string
	Required bool
}

type FormField struct {
	ID          string
	Kind        FormFieldKind
	Label       string
	Description string
	Placeholder string
	Required    bool
	Multiple    bool
	Render      string
	Options     []FormOption
	Markdown    string
}

type IssueForm struct {
	Definition       DefinitionRef
	TitlePrefix      string
	DefaultLabels    []string
	DefaultAssignees []string
	Fields           []FormField
}

type RepositoryDefinitions struct {
	Markdown []MarkdownTemplate
	Forms    []IssueForm
}

type FormAnswer struct {
	FieldID string
	Values  []string
}

type Label struct{ Name string }
type Assignee struct{ Login string }
type Milestone struct{ Number int64 }
type Project struct {
	NodeID     string
	Permission ProjectPermission
}

type ProviderExtension struct {
	Milestone *Milestone
	Projects  []Project
}

type CaptureField struct {
	Key   string
	Value string
}

type DOMMetadata struct {
	CSSSelector    string
	Tag            string
	Role           string
	AccessibleName string
	ViewportWidth  int32
	ViewportHeight int32
}

type CaptureMetadata struct {
	Environment  []CaptureField
	SanitizedURL string
	DOM          *DOMMetadata
}

type InlineImage struct {
	AltText string
	URL     string
}

type IssueInput struct {
	SubmissionID       uuid.UUID
	Title              string
	RepositoryResponse string
	Capture            CaptureMetadata
	Images             []InlineImage
	Labels             []Label
	Assignees          []Assignee
	Extension          ProviderExtension
}

type ProjectAssignmentDisposition string

const (
	ProjectAssignmentApplied ProjectAssignmentDisposition = "applied"
	ProjectAssignmentFailed  ProjectAssignmentDisposition = "failed"
)

type ProjectAssignment struct {
	ProjectNodeID string
	Disposition   ProjectAssignmentDisposition
}

type Issue struct {
	ID                 int64
	NodeID             string
	Number             int64
	URL                string
	Body               string
	CreatedAt          time.Time
	ProjectAssignments []ProjectAssignment
}

func validateGitHubIssueURL(value, owner, repository string, number int64) error {
	parsed, err := url.Parse(value)
	expectedPath := fmt.Sprintf("/%s/%s/issues/%d", owner, repository, number)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Path != expectedPath {
		return errors.New("realqa github: provider returned an invalid issue URL")
	}
	return nil
}

func cleanName(value string) (string, error) {
	if !safeNamePattern.MatchString(value) || strings.Contains(value, "..") {
		return "", errors.New("realqa github: name is invalid")
	}
	return value, nil
}
