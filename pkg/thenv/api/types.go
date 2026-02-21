package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProcedurePushBundleVersion          = "/thenv.v1.BundleService/PushBundleVersion"
	ProcedurePullActiveBundle           = "/thenv.v1.BundleService/PullActiveBundle"
	ProcedureListBundleVersions         = "/thenv.v1.BundleService/ListBundleVersions"
	ProcedureActivateBundle             = "/thenv.v1.BundleService/ActivateBundleVersion"
	ProcedureRotateBundleVersion        = "/thenv.v1.BundleService/RotateBundleVersion"
	ProcedureGetPolicy                  = "/thenv.v1.PolicyService/GetPolicy"
	ProcedureSetPolicy                  = "/thenv.v1.PolicyService/SetPolicy"
	ProcedureListAuditEvents            = "/thenv.v1.AuditService/ListAuditEvents"
	DefaultListLimit             uint32 = 20
)

type JSONCodec struct{}

func (JSONCodec) Name() string { return "json" }

func (JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

type FileType int32

const (
	FileTypeUnspecified FileType = iota
	FileTypeEnv
	FileTypeDevVars
)

func (f FileType) String() string {
	switch f {
	case FileTypeEnv:
		return "env"
	case FileTypeDevVars:
		return "dev-vars"
	default:
		return "unspecified"
	}
}

type Role int32

const (
	RoleUnspecified Role = iota
	RoleReader
	RoleWriter
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleReader:
		return "reader"
	case RoleWriter:
		return "writer"
	case RoleAdmin:
		return "admin"
	default:
		return "unspecified"
	}
}

func ParseRole(value string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "reader":
		return RoleReader, nil
	case "writer":
		return RoleWriter, nil
	case "admin":
		return RoleAdmin, nil
	default:
		return RoleUnspecified, fmt.Errorf("unknown role: %s", value)
	}
}

type BundleStatus int32

const (
	BundleStatusUnspecified BundleStatus = iota
	BundleStatusActive
	BundleStatusArchived
)

func (s BundleStatus) String() string {
	switch s {
	case BundleStatusActive:
		return "active"
	case BundleStatusArchived:
		return "archived"
	default:
		return "unspecified"
	}
}

type ConflictPolicy int32

const (
	ConflictPolicyUnspecified ConflictPolicy = iota
	ConflictPolicyFailClosed
	ConflictPolicyForceOverwrite
)

func (c ConflictPolicy) String() string {
	switch c {
	case ConflictPolicyFailClosed:
		return "fail-closed"
	case ConflictPolicyForceOverwrite:
		return "force-overwrite"
	default:
		return "unspecified"
	}
}

type AuditEventType int32

const (
	AuditEventTypeUnspecified AuditEventType = iota
	AuditEventTypePush
	AuditEventTypePull
	AuditEventTypeList
	AuditEventTypeRotate
	AuditEventTypeActivate
	AuditEventTypePolicyUpdate
)

func (e AuditEventType) String() string {
	switch e {
	case AuditEventTypePush:
		return "push"
	case AuditEventTypePull:
		return "pull"
	case AuditEventTypeList:
		return "list"
	case AuditEventTypeRotate:
		return "rotate"
	case AuditEventTypeActivate:
		return "activate"
	case AuditEventTypePolicyUpdate:
		return "policy-update"
	default:
		return "unspecified"
	}
}

type Scope struct {
	WorkspaceID   string `json:"workspaceId"`
	ProjectID     string `json:"projectId"`
	EnvironmentID string `json:"environmentId"`
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.WorkspaceID) == "" {
		return errors.New("workspaceId is required")
	}
	if strings.TrimSpace(s.ProjectID) == "" {
		return errors.New("projectId is required")
	}
	if strings.TrimSpace(s.EnvironmentID) == "" {
		return errors.New("environmentId is required")
	}
	return nil
}

type BundleFilePayload struct {
	FileType   FileType `json:"fileType"`
	Content    []byte   `json:"content,omitempty"`
	Checksum   string   `json:"checksum,omitempty"`
	ByteLength int64    `json:"byteLength,omitempty"`
}

type BundleVersionSummary struct {
	BundleVersionID string       `json:"bundleVersionId"`
	Scope           Scope        `json:"scope"`
	Status          BundleStatus `json:"status"`
	CreatedBy       string       `json:"createdBy"`
	CreatedAt       time.Time    `json:"createdAt"`
	SourceVersionID string       `json:"sourceVersionId,omitempty"`
}

type PushBundleVersionRequest struct {
	Scope    Scope               `json:"scope"`
	Files    []BundleFilePayload `json:"files"`
	Metadata string              `json:"metadata,omitempty"`
}

type PushBundleVersionResponse struct {
	BundleVersionID string       `json:"bundleVersionId"`
	CreatedAt       time.Time    `json:"createdAt"`
	Status          BundleStatus `json:"status"`
}

type PullActiveBundleRequest struct {
	Scope           Scope  `json:"scope"`
	BundleVersionID string `json:"bundleVersionId,omitempty"`
}

type PullActiveBundleResponse struct {
	Version BundleVersionSummary `json:"version"`
	Files   []BundleFilePayload  `json:"files"`
}

type ListBundleVersionsRequest struct {
	Scope  Scope  `json:"scope"`
	Limit  uint32 `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type ListBundleVersionsResponse struct {
	Versions   []BundleVersionSummary `json:"versions"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

type ActivateBundleVersionRequest struct {
	Scope           Scope  `json:"scope"`
	BundleVersionID string `json:"bundleVersionId"`
}

type ActivateBundleVersionResponse struct {
	Previous *BundleVersionSummary `json:"previous,omitempty"`
	Current  BundleVersionSummary  `json:"current"`
}

type RotateBundleVersionRequest struct {
	Scope         Scope  `json:"scope"`
	FromVersionID string `json:"fromVersionId,omitempty"`
}

type RotateBundleVersionResponse struct {
	BundleVersionID string               `json:"bundleVersionId"`
	Current         BundleVersionSummary `json:"current"`
}

type PolicyBinding struct {
	Subject string `json:"subject"`
	Role    Role   `json:"role"`
}

type GetPolicyRequest struct {
	Scope Scope `json:"scope"`
}

type GetPolicyResponse struct {
	Scope          Scope           `json:"scope"`
	PolicyRevision int64           `json:"policyRevision"`
	Bindings       []PolicyBinding `json:"bindings"`
}

type SetPolicyRequest struct {
	Scope    Scope           `json:"scope"`
	Bindings []PolicyBinding `json:"bindings"`
}

type SetPolicyResponse struct {
	Scope          Scope `json:"scope"`
	PolicyRevision int64 `json:"policyRevision"`
}

type AuditEvent struct {
	EventID               string         `json:"eventId"`
	EventType             AuditEventType `json:"eventType"`
	Actor                 string         `json:"actor"`
	Scope                 Scope          `json:"scope"`
	TargetBundleVersionID string         `json:"targetBundleVersionId,omitempty"`
	Result                string         `json:"result"`
	FailureCode           string         `json:"failureCode,omitempty"`
	RequestID             string         `json:"requestId,omitempty"`
	TraceID               string         `json:"traceId,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	Metadata              string         `json:"metadata,omitempty"`
}

type ListAuditEventsRequest struct {
	Scope     Scope          `json:"scope"`
	EventType AuditEventType `json:"eventType,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	StartTime *time.Time     `json:"startTime,omitempty"`
	EndTime   *time.Time     `json:"endTime,omitempty"`
	Limit     uint32         `json:"limit,omitempty"`
	Cursor    string         `json:"cursor,omitempty"`
}

type ListAuditEventsResponse struct {
	Events     []AuditEvent `json:"events"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

func NormalizeLimit(limit uint32) uint32 {
	if limit == 0 {
		return DefaultListLimit
	}
	if limit > 200 {
		return 200
	}
	return limit
}
