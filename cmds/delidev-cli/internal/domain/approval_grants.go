package domain

import (
	"reflect"
	"slices"
)

// CodexPermissionGrant explicitly selects scope. Only an exact requested descriptor
// (or a write entry reduced to read access) may be granted. This comparison does
// not claim to resolve native paths or prove resulting sandbox enforcement.
type CodexPermissionGrant struct {
	Permissions      CodexPermissionProfile    `json:"permissions"`
	Scope            CodexPermissionGrantScope `json:"scope"`
	StrictAutoReview *bool                     `json:"strict_auto_review,omitempty"`
}

func (g *CodexPermissionGrant) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Permissions      *CodexPermissionProfile    `json:"permissions"`
		Scope            *CodexPermissionGrantScope `json:"scope"`
		StrictAutoReview *bool                      `json:"strict_auto_review,omitempty"`
	}
	if Decode(raw, &wire) != nil || wire.Permissions == nil || wire.Scope == nil {
		return invalidApprovalResponse()
	}
	*g = CodexPermissionGrant{Permissions: *wire.Permissions, Scope: *wire.Scope, StrictAutoReview: wire.StrictAutoReview}
	return nil
}

func (g CodexPermissionGrant) Validate(request *CodexPermissionsApprovalRequest) error {
	invalid := func() error {
		return Fail(InvalidArgument, "The grant exceeds the original native permission request.", "Choose only requested access and an explicit native scope; do not introduce new paths, rules or network access.")
	}
	if request == nil || request.Permissions.Validate() != nil || g.Permissions.Validate() != nil || (g.Scope != CodexPermissionTurn && g.Scope != CodexPermissionSession) || (g.StrictAutoReview != nil && *g.StrictAutoReview && g.Scope != CodexPermissionTurn) {
		return invalid()
	}
	want, have := g.Permissions, request.Permissions
	if want.Network != nil && want.Network.Enabled != nil && *want.Network.Enabled && (have.Network == nil || have.Network.Enabled == nil || !*have.Network.Enabled) {
		return invalid()
	}
	if want.FileSystem == nil {
		return nil
	}
	f, r := want.FileSystem, have.FileSystem
	if r == nil {
		if len(f.Read) != 0 || len(f.Write) != 0 || len(f.Entries) != 0 || f.GlobScanMaxDepth != nil {
			return invalid()
		}
		return nil
	}
	granted, requested := effectiveCodexPermissionEntries(f), effectiveCodexPermissionEntries(r)
	for _, entry := range granted {
		if !slices.ContainsFunc(requested, func(original CodexFilePermissionEntry) bool {
			return reflect.DeepEqual(original.Path, entry.Path) && (original.Access == entry.Access || (original.Access == CodexFilePermissionWrite && entry.Access == CodexFilePermissionRead))
		}) {
			return invalid()
		}
	}
	if len(granted) != 0 {
		// Retain effective deny descriptors while granting any filesystem access.
		// Native entries take precedence over their deprecated read/write mirrors.
		for _, entry := range requested {
			if entry.Access == CodexFilePermissionDeny && !slices.ContainsFunc(granted, func(selected CodexFilePermissionEntry) bool { return reflect.DeepEqual(entry, selected) }) {
				return invalid()
			}
		}
	}
	if f.GlobScanMaxDepth != nil && (r.GlobScanMaxDepth == nil || *f.GlobScanMaxDepth != *r.GlobScanMaxDepth) {
		return invalid()
	}
	return nil
}

// The pinned app-server conversion treats a present entries collection,
// including an empty one, as authoritative over legacy read/write mirrors.
// Preserve descriptor order and bytes; this does not interpret filesystem paths.
func effectiveCodexPermissionEntries(fs *CodexAdditionalFilePermissions) []CodexFilePermissionEntry {
	if fs == nil {
		return nil
	}
	if fs.Entries != nil {
		return slices.Clone(fs.Entries)
	}
	result := make([]CodexFilePermissionEntry, 0, len(fs.Read)+len(fs.Write))
	for _, path := range fs.Read {
		result = append(result, CodexFilePermissionEntry{Access: CodexFilePermissionRead, Path: CodexFilePermissionPathValue{Kind: CodexFilePermissionPath, Path: &path}})
	}
	for _, path := range fs.Write {
		result = append(result, CodexFilePermissionEntry{Access: CodexFilePermissionWrite, Path: CodexFilePermissionPathValue{Kind: CodexFilePermissionPath, Path: &path}})
	}
	return result
}
