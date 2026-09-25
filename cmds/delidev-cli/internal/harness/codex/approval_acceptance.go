package codex

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ApprovalEvidence string

const PermissionOutputEvidence ApprovalEvidence = "native-permissions-output"

// The native tool returns the core snake_case profile, not the app-server
// camelCase reply. Only schema-equivalent legacy roots are converted to entries;
// never resolve paths, expand globs, reorder rules or infer a broader grant.
func permissionGrantDigest(grant PermissionGrant) ([32]byte, error) {
	profile := grant.Permissions
	if profile.Validate() != nil || (grant.Scope != PermissionTurn && grant.Scope != PermissionSession) || (grant.StrictAutoReview != nil && *grant.StrictAutoReview && grant.Scope != PermissionTurn) {
		return [32]byte{}, incompatible()
	}
	if fs := profile.FileSystem; fs != nil {
		canonical := &AdditionalFilePermissions{Entries: effectivePermissionEntries(fs), GlobScanMaxDepth: fs.GlobScanMaxDepth}
		profile.FileSystem = canonical
		if len(canonical.Entries) == 0 && canonical.GlobScanMaxDepth == nil {
			profile.FileSystem = nil
		}
	}
	value := struct {
		Permissions PermissionProfile    `json:"permissions"`
		Scope       PermissionGrantScope `json:"scope"`
		Strict      bool                 `json:"strict_auto_review"`
	}{profile, grant.Scope, grant.StrictAutoReview != nil && *grant.StrictAutoReview}
	raw, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, incompatible()
	}
	return sha256.Sum256(raw), nil
}

type corePermissionFileSystem struct {
	Read    []string              `json:"read,omitempty"`
	Write   []string              `json:"write,omitempty"`
	Entries []FilePermissionEntry `json:"entries,omitempty"`
	Depth   *uint32               `json:"glob_scan_max_depth,omitempty"`
}

func (f *corePermissionFileSystem) UnmarshalJSON(raw []byte) error {
	type plain corePermissionFileSystem
	var decoded plain
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &decoded) != nil || domain.Decode(raw, &fields) != nil {
		return incompatible()
	}
	// Core serialization chooses exactly one legacy or canonical shape.
	if (fields["read"] != nil || fields["write"] != nil) && (fields["entries"] != nil || fields["glob_scan_max_depth"] != nil) {
		return incompatible()
	}
	*f = corePermissionFileSystem(decoded)
	return nil
}

func decodePermissionOutput(raw []byte) (PermissionGrant, error) {
	var output struct {
		Permissions *struct {
			Network    *AdditionalNetworkPermissions `json:"network"`
			FileSystem *corePermissionFileSystem     `json:"file_system"`
		} `json:"permissions"`
		Scope  *PermissionGrantScope `json:"scope"`
		Strict *bool                 `json:"strict_auto_review,omitempty"`
	}
	var fields map[string]json.RawMessage
	if len(raw) > maxAnswerBytes || domain.Decode(raw, &output) != nil || domain.Decode(raw, &fields) != nil || output.Permissions == nil || output.Scope == nil || (fields["strict_auto_review"] != nil && output.Strict == nil) {
		return PermissionGrant{}, incompatible()
	}
	grant := PermissionGrant{Permissions: PermissionProfile{Network: output.Permissions.Network}, Scope: *output.Scope, StrictAutoReview: output.Strict}
	if fs := output.Permissions.FileSystem; fs != nil {
		grant.Permissions.FileSystem = &AdditionalFilePermissions{Read: fs.Read, Write: fs.Write, Entries: fs.Entries, GlobScanMaxDepth: fs.Depth}
	}
	return grant, nil
}

func (c *Client) observePermissionAcceptanceLocked(owned *trackedInteraction, output nativeFunctionOutput, discarded Event) (Event, error) {
	if output.Namespace != nil || (output.Name != nil && *output.Name != "request_permissions") {
		return Event{}, c.permissionEvidenceFailure(permissionEvidenceTool)
	}
	var text *string
	if json.Unmarshal(output.Output, &text) != nil || text == nil {
		return Event{}, c.permissionEvidenceFailure(permissionEvidenceOutput)
	}
	grant, err := decodePermissionOutput([]byte(*text))
	if err != nil {
		return Event{}, c.permissionEvidenceFailure(permissionEvidenceGrant)
	}
	digest, err := permissionGrantDigest(grant)
	if err != nil || owned.grantDigest == ([32]byte{}) || digest != owned.grantDigest {
		return Event{}, c.permissionEvidenceFailure(permissionEvidenceDigest)
	}
	if owned.status.Accepted {
		return discarded, nil
	}
	owned.confirmAcceptance(PermissionOutputEvidence)
	status := owned.status
	return Event{Kind: ApprovalAcceptedEvent, ThreadID: c.thread, TurnID: status.TurnID, ItemID: status.ItemID, InteractionState: &status, Correlated: true, Late: discarded.Late}, nil
}

// Closed validation stages identify protocol drift without serializing output,
// paths, grants, scope contents or their commitments into operational logs.
func (c *Client) permissionEvidenceFailure(stage permissionEvidenceStage) error {
	if c.logger != nil {
		c.logger.Warn("Codex permission evidence rejected", "owner_id", c.ownerID, "stage", stage)
	}
	return incompatible()
}

type permissionEvidenceStage string

const (
	permissionEvidenceTool   permissionEvidenceStage = "tool-identity"
	permissionEvidenceOutput permissionEvidenceStage = "output-shape"
	permissionEvidenceGrant  permissionEvidenceStage = "grant-shape"
	permissionEvidenceDigest permissionEvidenceStage = "grant-digest"
)
