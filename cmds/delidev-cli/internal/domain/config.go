package domain

import (
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Harness string

const (
	Codex      Harness = "codex"
	ClaudeCode Harness = "claude-code"
	OpenCode   Harness = "opencode"
	GrokBuild  Harness = "grok-build"
)

func (h Harness) Valid() bool {
	return slices.Contains([]Harness{Codex, ClaudeCode, OpenCode, GrokBuild}, h)
}

type WorkspaceType string

const (
	Worktree    WorkspaceType = "worktree"
	Local       WorkspaceType = "local"
	GeneralChat WorkspaceType = "general-chat"
)

func (w WorkspaceType) Valid() bool { return w == Worktree || w == Local || w == GeneralChat }

type RoutingPolicy string

const (
	Fixed                RoutingPolicy = "fixed"
	Priority             RoutingPolicy = "priority"
	RoundRobin           RoutingPolicy = "round-robin"
	RemainingQuota       RoutingPolicy = "remaining-quota"
	ResetWindow          RoutingPolicy = "reset-window"
	SequentialExhaustion RoutingPolicy = "sequential-exhaustion"
)

func (p RoutingPolicy) Valid() bool {
	return slices.Contains([]RoutingPolicy{Fixed, Priority, RoundRobin, RemainingQuota, ResetWindow, SequentialExhaustion}, p)
}

type Restriction struct {
	Configured bool `json:"configured"`
	IDs        []ID `json:"ids"`
}

func (r Restriction) Allows(id ID) bool { return !r.Configured || slices.Contains(r.IDs, id) }
func (r Restriction) Validate() error {
	if !r.Configured && len(r.IDs) > 0 {
		return Fail(InvalidArgument, "Unconfigured restrictions cannot include IDs.", "Set configured=true to restrict selection.")
	}
	return UniqueIDs(r.IDs)
}
func UniqueIDs(ids []ID) error {
	seen := map[ID]bool{}
	if len(ids) > 1000 {
		return Fail(InvalidArgument, "Too many linked entities.", "Use at most 1000 links.")
	}
	for _, id := range ids {
		if err := id.Validate(); err != nil {
			return err
		}
		if seen[id] {
			return Fail(InvalidArgument, "Duplicate entity reference.", "Include each ID once.")
		}
		seen[id] = true
	}
	return nil
}

type Project struct {
	Name              string      `json:"name"`
	Repositories      []ID        `json:"repositories"`
	PrimaryRepository ID          `json:"primary_repository"`
	Agents            Restriction `json:"agents"`
	Accounts          Restriction `json:"accounts"`
}

func (p Project) Validate() error {
	if err := Text(p.Name, "project name", 256, true); err != nil {
		return err
	}
	if err := UniqueIDs(p.Repositories); err != nil {
		return err
	}
	if len(p.Repositories) == 0 || !slices.Contains(p.Repositories, p.PrimaryRepository) {
		return Fail(InvalidArgument, "A project needs ordered repositories and a primary repository.", "Select a primary repository from the configured list.")
	}
	if err := p.Agents.Validate(); err != nil {
		return err
	}
	return p.Accounts.Validate()
}

type ReferenceType string

const (
	LocalBranch     ReferenceType = "local-branch"
	RemoteBranch    ReferenceType = "remote-branch"
	CommitReference ReferenceType = "commit"
)

type Reference struct {
	Type   ReferenceType `json:"type"`
	Name   string        `json:"name"`
	Remote string        `json:"remote,omitempty"`
}

func (r Reference) Validate(optional bool) error {
	if optional && r.Type == "" && r.Name == "" && r.Remote == "" {
		return nil
	}
	if r.Type != LocalBranch && r.Type != RemoteBranch && r.Type != CommitReference {
		return Fail(InvalidArgument, "Unknown Git reference type.", "Select local-branch, remote-branch, or commit.")
	}
	if err := Text(r.Name, "Git reference", 1024, true); err != nil {
		return err
	}
	if strings.HasPrefix(r.Name, "-") || strings.ContainsAny(r.Name, "\r\n") {
		return Fail(InvalidArgument, "Invalid Git reference.", "Use an explicit branch or full commit identity.")
	}
	if r.Type == RemoteBranch {
		if err := Text(r.Remote, "Git remote", 256, true); err != nil {
			return err
		}
		if strings.HasPrefix(r.Remote, "-") || strings.ContainsAny(r.Remote, " /\\:\r\n") {
			return Fail(InvalidArgument, "Invalid Git remote name.", "Use the name from repository inspection.")
		}
	} else if r.Remote != "" {
		return Fail(InvalidArgument, "Only remote-branch references have a remote.", "Remove the remote field.")
	}
	return nil
}

type Checkout struct {
	MachineID ID     `json:"machine_id"`
	Path      string `json:"path"`
}
type Repository struct {
	Name            string     `json:"name"`
	Checkouts       []Checkout `json:"checkouts"`
	PreferredRemote string     `json:"preferred_remote,omitempty"`
	Base            Reference  `json:"base"`
	Starting        Reference  `json:"starting"`
	AutoFetch       bool       `json:"auto_fetch"`
	GitHubOwner     string     `json:"github_owner,omitempty"`
	GitHubName      string     `json:"github_name,omitempty"`
	IntegrationID   ID         `json:"integration_id,omitempty"`
}

func (r Repository) Validate() error {
	if err := Text(r.Name, "repository name", 256, true); err != nil {
		return err
	}
	if len(r.Checkouts) == 0 || len(r.Checkouts) > 1000 {
		return Fail(InvalidArgument, "A repository requires an execution-machine checkout.", "Inspect a checkout on its Worker first.")
	}
	ids := make([]ID, 0, len(r.Checkouts))
	for _, c := range r.Checkouts {
		ids = append(ids, c.MachineID)
		if err := Text(c.Path, "checkout path", 4096, true); err != nil {
			return err
		}
		if !filepath.IsAbs(c.Path) && !windowsAbsolute(c.Path) {
			return Fail(InvalidArgument, "A checkout path must be absolute on its Worker.", "Use the canonical root returned by repository inspection.")
		}
	}
	if err := UniqueIDs(ids); err != nil {
		return err
	}
	if err := r.Base.Validate(true); err != nil {
		return err
	}
	if err := r.Starting.Validate(true); err != nil {
		return err
	}
	if r.IntegrationID != "" {
		if err := r.IntegrationID.Validate(); err != nil {
			return err
		}
	}
	if err := Text(r.PreferredRemote, "preferred remote", 256, false); err != nil {
		return err
	}
	if strings.ContainsAny(r.PreferredRemote, " /\\:\r\n") || strings.HasPrefix(r.PreferredRemote, "-") {
		return Fail(InvalidArgument, "Invalid preferred remote name.", "Use a remote returned by Worker inspection.")
	}
	return nil
}
func windowsAbsolute(s string) bool {
	return len(s) > 2 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

type PermissionMode string

const (
	PermissionDefault        PermissionMode = "default"
	PermissionReadOnly       PermissionMode = "read-only"
	PermissionWorkspaceWrite PermissionMode = "workspace-write"
	PermissionFullAccess     PermissionMode = "full-access"
)

type AgentOptions struct {
	SubagentModel       string         `json:"subagent_model,omitempty"`
	SubagentEffort      string         `json:"subagent_effort,omitempty"`
	MaxConcurrency      uint32         `json:"max_concurrency,omitempty"`
	Permission          PermissionMode `json:"permission"`
	ApprovalPolicy      string         `json:"approval_policy,omitempty"`
	ApprovalReviewModel string         `json:"approval_review_model,omitempty"`
	ServiceTier         string         `json:"service_tier,omitempty"`
}
type WeightedAccount struct {
	ID     ID     `json:"id"`
	Weight uint32 `json:"weight"`
}
type Agent struct {
	Name      string            `json:"name"`
	Harness   Harness           `json:"harness"`
	ModelID   ID                `json:"model_id"`
	Effort    string            `json:"effort,omitempty"`
	Accounts  []WeightedAccount `json:"accounts"`
	Routing   *RoutingPolicy    `json:"routing,omitempty"`
	Templates []ID              `json:"templates"`
	Options   AgentOptions      `json:"options"`
}

func (a Agent) Validate() error {
	if err := Text(a.Name, "Agent Worker name", 256, true); err != nil {
		return err
	}
	if !a.Harness.Valid() {
		return Fail(InvalidArgument, "Unknown harness.", "Choose codex, claude-code, opencode, or grok-build.")
	}
	if err := a.ModelID.Validate(); err != nil {
		return err
	}
	if a.Routing != nil && !a.Routing.Valid() {
		return Fail(InvalidArgument, "Unknown routing policy.", "Select one of the six supported policies.")
	}
	ids := make([]ID, 0, len(a.Accounts))
	for _, c := range a.Accounts {
		ids = append(ids, c.ID)
		if c.Weight < 1 || c.Weight > 1000 {
			return Fail(InvalidArgument, "Invalid account weight.", "Use relative weights from 1 through 1000.")
		}
	}
	if err := UniqueIDs(ids); err != nil {
		return err
	}
	if err := UniqueIDs(a.Templates); err != nil {
		return err
	}
	if a.Routing != nil && *a.Routing == Fixed && len(a.Accounts) > 1 {
		return Fail(InvalidArgument, "Fixed routing accepts one account.", "Select one account or save an account-less draft.")
	}
	if a.Options.Permission != PermissionDefault && a.Options.Permission != PermissionReadOnly && a.Options.Permission != PermissionWorkspaceWrite && a.Options.Permission != PermissionFullAccess {
		return Fail(InvalidArgument, "Unknown native permission mode.", "Choose an explicit supported permission mode.")
	}
	if a.Options.MaxConcurrency > 64 {
		return Fail(InvalidArgument, "Invalid native concurrency limit.", "Use at most 64; installed harness limits are checked before dispatch.")
	}
	for _, value := range []string{a.Effort, a.Options.SubagentModel, a.Options.SubagentEffort, a.Options.ApprovalPolicy, a.Options.ApprovalReviewModel, a.Options.ServiceTier} {
		if err := Text(value, "native option", 256, false); err != nil {
			return err
		}
	}
	return nil
}

type Template struct {
	Name     string `json:"name"`
	Contents string `json:"contents"`
}

func (t Template) Validate() error {
	if err := Text(t.Name, "template name", 256, true); err != nil {
		return err
	}
	return Text(t.Contents, "template contents", 128<<10, true)
}

type APIProtocol string

const (
	OpenAIResponses    APIProtocol = "openai-responses"
	OpenAIChat         APIProtocol = "openai-chat"
	AnthropicMessages  APIProtocol = "anthropic-messages"
	NativeSubscription APIProtocol = "native-subscription"
)

func (p APIProtocol) Valid() bool {
	return p == OpenAIResponses || p == OpenAIChat || p == AnthropicMessages || p == NativeSubscription
}

type Authentication string

const (
	BearerAuth       Authentication = "bearer"
	APIKeyAuth       Authentication = "api-key"
	KeylessAuth      Authentication = "keyless"
	SubscriptionAuth Authentication = "subscription"
)

type Provider struct {
	Name           string         `json:"name"`
	Endpoint       string         `json:"endpoint"`
	Protocol       APIProtocol    `json:"protocol"`
	Authentication Authentication `json:"authentication"`
	Discovery      bool           `json:"discovery"`
}

func (p Provider) Validate() error {
	if err := Text(p.Name, "provider name", 256, true); err != nil {
		return err
	}
	if !p.Protocol.Valid() {
		return Fail(InvalidArgument, "Unsupported provider protocol.", "Select a directly compatible native protocol.")
	}
	if p.Protocol == NativeSubscription {
		if p.Authentication != SubscriptionAuth || p.Endpoint != "" {
			return Fail(InvalidArgument, "Native subscription providers cannot configure an API endpoint.", "Use an isolated official account login.")
		}
		return nil
	}
	if p.Authentication != BearerAuth && p.Authentication != APIKeyAuth && p.Authentication != KeylessAuth {
		return Fail(InvalidArgument, "Unsupported API authentication.", "Use bearer, api-key, or a keyless local endpoint.")
	}
	return ValidateEndpoint(p.Endpoint, p.Authentication == KeylessAuth)
}
func ValidateEndpoint(value string, keyless bool) error {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || u.Opaque != "" {
		return Fail(InvalidArgument, "Invalid provider endpoint.", "Use an absolute URL without credentials, query, or fragment.")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return Fail(InvalidArgument, "Provider endpoints require HTTPS except on loopback.", "Configure a secure endpoint; localhost is the server machine.")
	}
	if keyless && !loopback {
		return Fail(InvalidArgument, "Keyless providers must be local endpoints.", "Choose a loopback endpoint on the server machine.")
	}
	if strings.Contains(u.EscapedPath(), "%") || strings.Contains(u.Path, "\\") {
		return Fail(InvalidArgument, "The endpoint path is ambiguous.", "Use a plain API base path without encoded separators.")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return Fail(InvalidArgument, "The endpoint path contains traversal segments.", "Use the provider's canonical API base path.")
		}
	}
	return nil
}

type EvidenceSource string

const (
	Known        EvidenceSource = "known"
	UserDeclared EvidenceSource = "user-declared"
	Unknown      EvidenceSource = "unknown"
)

type Model struct {
	ProviderID       ID             `json:"provider_id"`
	NativeID         string         `json:"native_id"`
	Name             string         `json:"name"`
	Alias            string         `json:"alias,omitempty"`
	Harnesses        []Harness      `json:"harnesses"`
	Hidden           bool           `json:"hidden"`
	Order            int32          `json:"order"`
	Manual           bool           `json:"manual"`
	New              bool           `json:"new"`
	ContextLimit     *uint64        `json:"context_limit,omitempty"`
	MetadataSource   EvidenceSource `json:"metadata_source"`
	InputModalities  []string       `json:"input_modalities,omitempty"`
	OutputModalities []string       `json:"output_modalities,omitempty"`
	Tools            *bool          `json:"tools,omitempty"`
	Reasoning        *bool          `json:"reasoning,omitempty"`
}

func (m Model) Validate() error {
	if err := m.ProviderID.Validate(); err != nil {
		return err
	}
	for _, s := range []string{m.NativeID, m.Name} {
		if err := Text(s, "model identity", 256, true); err != nil {
			return err
		}
	}
	if err := Text(m.Alias, "model alias", 128, false); err != nil {
		return err
	}
	if strings.ContainsAny(m.Alias, " \t\n/:\\") {
		return Fail(InvalidArgument, "Invalid model alias.", "Use a unique single-word CLI alias.")
	}
	if len(m.Harnesses) == 0 || len(m.Harnesses) > 4 {
		return Fail(InvalidArgument, "A model needs explicit harness compatibility.", "Select compatible harnesses; metadata never grants compatibility.")
	}
	seen := map[Harness]bool{}
	for _, h := range m.Harnesses {
		if !h.Valid() || seen[h] {
			return Fail(InvalidArgument, "Invalid model harness compatibility.", "Select each supported harness once.")
		}
		seen[h] = true
	}
	if m.MetadataSource != Known && m.MetadataSource != UserDeclared && m.MetadataSource != Unknown {
		return Fail(InvalidArgument, "Unknown model metadata source.", "Distinguish known, user-declared, and unknown metadata.")
	}
	return nil
}

type AccountType string

const (
	APIAccount          AccountType = "api"
	SubscriptionAccount AccountType = "subscription"
)

type AccountHealth string

const (
	AccountDisconnected AccountHealth = "disconnected"
	AccountUnverified   AccountHealth = "unverified"
	AccountReady        AccountHealth = "ready"
	AccountExpired      AccountHealth = "expired"
	AccountRevoked      AccountHealth = "revoked"
	AccountFailed       AccountHealth = "failed"
)

type ObservationState string

const (
	Observed               ObservationState = "observed"
	ObservationUnknown     ObservationState = "unknown"
	ObservationStale       ObservationState = "stale"
	ObservationFailed      ObservationState = "failed"
	ObservationUnsupported ObservationState = "unsupported"
)

type QuotaWindow struct {
	ID              string           `json:"id"`
	ComparisonGroup string           `json:"comparison_group"`
	Blocking        bool             `json:"blocking"`
	Remaining       *float64         `json:"remaining,omitempty"`
	ResetAt         *time.Time       `json:"reset_at,omitempty"`
	ObservedAt      time.Time        `json:"observed_at"`
	State           ObservationState `json:"state"`
}
type AccountConnection struct {
	ID             ID             `json:"id"`
	Authentication Authentication `json:"authentication"`
	ConnectedAt    time.Time      `json:"connected_at"`
}

// Removal is independent of health: disconnected immediately blocks execution,
// while this marker keeps native deletion retryable and blocks reconnection.
type AccountRemoval struct {
	RequestID        ID     `json:"request_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
}
type Account struct {
	Alias                 string             `json:"alias"`
	ProviderID            ID                 `json:"provider_id"`
	Type                  AccountType        `json:"type"`
	Enabled               bool               `json:"enabled"`
	ExcludeAutomatic      bool               `json:"exclude_automatic"`
	RecoveryNotifications bool               `json:"recovery_notifications"`
	Health                AccountHealth      `json:"health"`
	Quota                 []QuotaWindow      `json:"quota"`
	ConfirmedExhausted    bool               `json:"confirmed_exhausted"`
	Connection            *AccountConnection `json:"connection,omitempty"`
	Removal               *AccountRemoval    `json:"removal,omitempty"`
	Validation            *AccountValidation `json:"validation,omitempty"`
}

func (a Account) Validate() error {
	if err := Text(a.Alias, "account alias", 256, true); err != nil {
		return err
	}
	if err := a.ProviderID.Validate(); err != nil {
		return err
	}
	if a.Type != APIAccount && a.Type != SubscriptionAccount {
		return Fail(InvalidArgument, "Unknown account type.", "Select api or subscription.")
	}
	if !slices.Contains([]AccountHealth{AccountDisconnected, AccountUnverified, AccountReady, AccountExpired, AccountRevoked, AccountFailed}, a.Health) {
		return Fail(InvalidArgument, "Unknown account health.", "Refresh account health through the server.")
	}
	if a.Connection != nil {
		if err := a.Connection.ID.Validate(); err != nil {
			return err
		}
		if a.Connection.ConnectedAt.IsZero() || a.Health == AccountDisconnected || a.Removal != nil {
			return Fail(InvalidArgument, "Invalid account connection state.", "Use the account lifecycle operations.")
		}
		if !slices.Contains([]Authentication{BearerAuth, APIKeyAuth, KeylessAuth, SubscriptionAuth}, a.Connection.Authentication) {
			return Fail(InvalidArgument, "Invalid account authentication mode.", "Use the provider's authentication mode.")
		}
	}
	if (a.Health == AccountUnverified || a.Health == AccountReady) && a.Connection == nil {
		return Fail(InvalidArgument, "Authenticated account states require a connection.", "Connect the account through its lifecycle operation.")
	}
	if a.Removal != nil {
		if err := a.Removal.RequestID.Validate(); err != nil {
			return err
		}
		if a.Removal.ExpectedRevision == 0 {
			return Fail(InvalidArgument, "Credential removal requires its original revision.", "Use the account disconnect operation.")
		}
		if a.Health != AccountDisconnected {
			return Fail(InvalidArgument, "Credential removal requires a disconnected account.", "Disconnect the account before removing protected resources.")
		}
	}
	if a.Validation != nil {
		v := a.Validation
		if v.RequestID.Validate() != nil || a.Connection == nil || v.ConnectionID != a.Connection.ID || v.ObservedAt.IsZero() {
			return Fail(InvalidArgument, "Invalid account validation generation.", "Validate the current account connection through its lifecycle operation.")
		}
		if !slices.Contains([]AuthenticationEvidence{CredentialAccepted, KeylessEndpoint, AuthenticationUnknown}, v.Authentication) || !slices.Contains([]ObservationState{Observed, ObservationFailed, ObservationUnsupported}, v.State) {
			return Fail(InvalidArgument, "Invalid account validation evidence.", "Use a supported provider validation result.")
		}
	}
	return nil
}

type Capability string

const (
	CapabilityExecute   Capability = "execute"
	CapabilitySteer     Capability = "steer"
	CapabilityFork      Capability = "fork"
	CapabilityPlan      Capability = "plan"
	CapabilityCompact   Capability = "compact"
	CapabilityQuestions Capability = "questions"
	CapabilityApprovals Capability = "approvals"
	CapabilityUsage     Capability = "usage"
	CapabilitySubagents Capability = "subagents"
	CapabilityReadOnly  Capability = "read-only"
	CapabilityModels    Capability = "models"
)

type Installation struct {
	Harness          Harness              `json:"harness"`
	State            InstallationState    `json:"state"`
	ExplicitPath     string               `json:"explicit_path,omitempty"`
	ResolvedPath     string               `json:"resolved_path,omitempty"`
	Version          string               `json:"version,omitempty"`
	Capabilities     []Capability         `json:"capabilities"`
	Problem          *Error               `json:"problem,omitempty"`
	ObservedAt       *time.Time           `json:"observed_at,omitempty"`
	ProtocolVerified bool                 `json:"protocol_verified"`
	Protocol         *ProtocolObservation `json:"protocol,omitempty"`
}
type Machine struct {
	Name              string         `json:"name"`
	OS                string         `json:"os"`
	Architecture      string         `json:"architecture"`
	Version           string         `json:"version"`
	Installations     []Installation `json:"installations"`
	DiscoveryRevision uint64         `json:"discovery_revision,omitempty"`
	LastSeen          time.Time      `json:"last_seen"`
	Disabled          bool           `json:"disabled"`
}

func (m Machine) Validate() error {
	if err := Text(m.Name, "machine name", 256, true); err != nil {
		return err
	}
	if !slices.Contains([]string{"darwin", "linux", "windows"}, m.OS) {
		return Fail(InvalidArgument, "Unsupported Worker operating system.", "Use macOS, Linux, or Windows.")
	}
	if m.Architecture != "amd64" && m.Architecture != "arm64" {
		return Fail(InvalidArgument, "Unsupported Worker architecture.", "Use amd64 or arm64.")
	}
	seen := map[Harness]bool{}
	for _, i := range m.Installations {
		if !i.Harness.Valid() || seen[i.Harness] {
			return Fail(InvalidArgument, "Invalid harness installation list.", "Configure each harness once.")
		}
		seen[i.Harness] = true
		if err := Text(i.ExplicitPath, "executable path", 4096, false); err != nil {
			return err
		}
	}
	return nil
}

type Settings struct {
	DefaultRouting RoutingPolicy     `json:"default_routing"`
	Notifications  bool              `json:"notifications"`
	AutomaticFetch bool              `json:"automatic_fetch"`
	Remediation    RemediationPolicy `json:"remediation"`
}
type ConflictStrategy string

const (
	MergeConflictStrategy  ConflictStrategy = "merge"
	RebaseConflictStrategy ConflictStrategy = "rebase"
)

type SessionStrategy string

const (
	ReuseSession     SessionStrategy = "reuse"
	DedicatedSession SessionStrategy = "dedicated"
)

type RemediationPolicy struct {
	CIFailure        bool             `json:"ci_failure"`
	ReviewFeedback   bool             `json:"review_feedback"`
	MergeConflict    bool             `json:"merge_conflict"`
	ConflictStrategy ConflictStrategy `json:"conflict_strategy"`
	SessionStrategy  SessionStrategy  `json:"session_strategy"`
	AttemptLimit     uint32           `json:"attempt_limit"`
	AgentID          ID               `json:"agent_id,omitempty"`
	MachineID        ID               `json:"machine_id,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{DefaultRouting: SequentialExhaustion, Notifications: true, AutomaticFetch: true, Remediation: RemediationPolicy{ConflictStrategy: MergeConflictStrategy, SessionStrategy: ReuseSession, AttemptLimit: 3}}
}
func (s Settings) Validate() error {
	if !s.DefaultRouting.Valid() {
		return Fail(InvalidArgument, "Invalid default routing policy.", "Select one of the six supported policies.")
	}
	if s.Remediation.ConflictStrategy != MergeConflictStrategy && s.Remediation.ConflictStrategy != RebaseConflictStrategy {
		return Fail(InvalidArgument, "Invalid conflict strategy.", "Select merge or rebase.")
	}
	if s.Remediation.SessionStrategy != ReuseSession && s.Remediation.SessionStrategy != DedicatedSession {
		return Fail(InvalidArgument, "Invalid remediation session strategy.", "Select reuse or dedicated.")
	}
	if s.Remediation.AttemptLimit < 1 || s.Remediation.AttemptLimit > 100 {
		return Fail(InvalidArgument, "Invalid remediation attempt limit.", "Use 1 through 100 attempts; the default is 3.")
	}
	if s.Remediation.AgentID != "" {
		if err := s.Remediation.AgentID.Validate(); err != nil {
			return err
		}
	}
	if s.Remediation.MachineID != "" {
		if err := s.Remediation.MachineID.Validate(); err != nil {
			return err
		}
	}
	return nil
}
