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
	for _, p := range f.Read {
		if !slices.Contains(r.Read, p) && !slices.Contains(r.Write, p) {
			return invalid()
		}
	}
	for _, p := range f.Write {
		if !slices.Contains(r.Write, p) {
			return invalid()
		}
	}
	for _, entry := range f.Entries {
		if !slices.ContainsFunc(r.Entries, func(original CodexFilePermissionEntry) bool {
			return reflect.DeepEqual(original.Path, entry.Path) && (original.Access == entry.Access || (original.Access == CodexFilePermissionWrite && entry.Access == CodexFilePermissionRead))
		}) {
			return invalid()
		}
	}
	if len(f.Read) != 0 || len(f.Write) != 0 || len(f.Entries) != 0 {
		// Native intersection remains authoritative. Retain every requested deny
		// descriptor as well so the selected grant never drops a restriction
		// while approving another path; this code never resolves path overlap.
		for _, entry := range r.Entries {
			if entry.Access == CodexFilePermissionDeny && !slices.ContainsFunc(f.Entries, func(granted CodexFilePermissionEntry) bool { return reflect.DeepEqual(entry, granted) }) {
				return invalid()
			}
		}
	}
	if f.GlobScanMaxDepth != nil && (r.GlobScanMaxDepth == nil || *f.GlobScanMaxDepth != *r.GlobScanMaxDepth) {
		return invalid()
	}
	return nil
}
