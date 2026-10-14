package workspace

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	// ManifestWorkerRootReserveBytes is the JSON-encoded length reserved for the
	// Worker data root that prefixes every prepared path. The server cannot
	// observe the root before preparation; the dispatch-time recheck of the real
	// bytes stays authoritative. The reserve is an encoded length, not a
	// character count: it covers a root of up to 1,536 plain bytes, 768 Windows
	// separators (two bytes each) or 256 '<', '>' or '&' bytes (six bytes each).
	ManifestWorkerRootReserveBytes = 1536
	// CheckoutRootReserveBytes is the minimum JSON-encoded length budgeted for
	// the canonical root of an existing checkout. Git inspection resolves
	// symlinks and records that canonical root as the prepared path and source,
	// so a short saved path can become longer (for example /tmp ->
	// /private/tmp). The budget for each checkout is the larger of this reserve
	// and the encoded saved path, and preparation refuses a canonical root that
	// exceeds it.
	CheckoutRootReserveBytes = 1024
	// UnresolvedReferenceNameReserveBytes is the accepted maximum of a Git
	// reference name the Worker may resolve (default branch) when the accepted
	// request left it unspecified.
	UnresolvedReferenceNameReserveBytes = 1024
	// UnresolvedRemoteNameReserveBytes is the accepted maximum of a Git remote
	// name the Worker may resolve when the request left the remote unspecified.
	UnresolvedRemoteNameReserveBytes = 256
	// unresolvedNameFill is a character that encoding/json expands to a six-byte
	// escape, so discovered names are budgeted at their worst escaped size.
	unresolvedNameFill = "<"
)

// ValidateWorkerRoot enforces ManifestWorkerRootReserveBytes. The root is
// measured as encoding/json writes it (so a Windows separator costs two bytes
// and '<', '>' or '&' cost six), which is how it repeats inside the Manifest.
func ValidateWorkerRoot(root string) error {
	encoded, err := json.Marshal(root)
	if err != nil {
		return domain.Fail(domain.InvalidArgument, "The Worker data directory cannot be encoded.", "Choose a different Worker data directory.")
	}
	if len(encoded)-2 > ManifestWorkerRootReserveBytes {
		return domain.Fail(domain.InvalidArgument, "The Worker data directory path is too long once JSON-escaped.", "Choose a shorter Worker data directory with fewer separators or special characters.")
	}
	return nil
}

// checkoutRootBudget returns the JSON-encoded byte budget of the canonical
// root of the saved checkout path, measured as encoding/json writes it.
func checkoutRootBudget(checkout string) int {
	encoded, err := json.Marshal(checkout)
	size := len(encoded) - 2
	if err != nil || size < CheckoutRootReserveBytes {
		return CheckoutRootReserveBytes
	}
	return size
}

// ValidateCheckoutRoot enforces checkoutRootBudget for the canonical root that
// inspection resolved from the saved checkout path.
func ValidateCheckoutRoot(saved, canonical string) error {
	encoded, err := json.Marshal(canonical)
	if err != nil || len(encoded)-2 > checkoutRootBudget(saved) {
		return domain.Fail(domain.ResourceExhausted, "The canonical checkout path is too long once JSON-escaped.", "Select the checkout through its canonical path or move it to a shorter path.")
	}
	return nil
}

// MaxManifestBytes returns an upper bound of the serialized Manifest a Worker
// can publish for this request. Every generated identity, digest and commit
// uses its maximum length and every unresolved path or reference uses the
// documented reserves above.
func (r PrepareRequest) MaxManifestBytes() (int, error) {
	digest := strings.Repeat("f", 64)
	// Generated separators use the Windows backslash, which JSON encodes in two
	// bytes, so the bound holds on every platform (filepath.Join emits it there).
	const sep = `\`
	root := strings.Repeat("r", ManifestWorkerRootReserveBytes) + sep + "workspaces" + sep + string(r.SessionID)
	manifest := Manifest{Version: 1, SessionID: r.SessionID, MachineID: r.MachineID, Type: r.Type, State: CleanupPending, InputDigest: digest, PrimaryPath: root + sep + string(r.SessionID), Repositories: []PreparedRepository{}, CreatedAt: time.Date(2006, 1, 2, 15, 4, 5, 123456789, time.UTC)}
	if r.Type == domain.GeneralChat {
		manifest.PrimaryPath = root + sep + "chat"
	}
	if r.SidechatSource != nil || r.ForkProfile == CodexSidechatReferenceV1 {
		manifest.Reference = &SidechatReference{SessionID: r.SessionID, PreparationDigest: digest, ManifestDigest: digest, DirectoryIdentity: digest, MetadataIdentity: digest}
	}
	for _, spec := range r.Repositories {
		managed := spec.SourceKind.managed()
		prepared := PreparedRepository{DirectoryName: spec.DirectoryName, SourceKind: spec.SourceKind, RemoteURL: spec.RemoteURL, PRTarget: spec.PRTarget, ID: spec.ID, Base: spec.Base, Starting: spec.Starting, BaseCommit: digest, StartingCommit: digest, Owned: r.Type == domain.Worktree}
		path := spec.Checkout
		if r.Type == domain.Worktree {
			path = root + sep + RepositoryDirectory(spec.ID, spec.DirectoryName)
		}
		prepared.Source = spec.Checkout
		if spec.Checkout != "" && !managed && spec.SourceKind != RemoteCloneSource {
			// Inspection replaces the saved path by its canonical root, which
			// preparation bounds by the same budget.
			prepared.Source = strings.Repeat("a", checkoutRootBudget(spec.Checkout))
			if r.Type != domain.Worktree {
				path = prepared.Source
			}
		}
		prepared.Path = path
		if managed {
			prepared.Source = path
			prepared.CloneRootDigest, prepared.CloneIdentityDigest = digest, digest
			manifest.ManagedRootDigest = digest
		}
		if spec.ForkRegistrationSource != "" {
			prepared.Source = spec.ForkRegistrationSource
		}
		if r.Type == domain.Local {
			prepared.LocalHEAD, prepared.LocalIdentityDigest = LocalHEADUnborn, digest
			prepared.Starting = domain.Reference{Type: domain.CommitReference, Name: digest}
		}
		if prepared.Starting.Type == "" {
			prepared.Starting = r.unresolvedReference(spec)
		}
		if prepared.Base.Type == "" {
			prepared.Base = prepared.Starting
		}
		if spec.ID == r.PrimaryRepository {
			manifest.PrimaryPath = prepared.Path
		}
		manifest.Repositories = append(manifest.Repositories, prepared)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return 0, err
	}
	return len(raw), nil
}

func (r PrepareRequest) unresolvedReference(spec RepositorySpec) domain.Reference {
	remote := spec.PreferredRemote
	if remote == "" {
		remote = strings.Repeat(unresolvedNameFill, UnresolvedRemoteNameReserveBytes)
	}
	return domain.Reference{Type: domain.RemoteBranch, Name: strings.Repeat(unresolvedNameFill, UnresolvedReferenceNameReserveBytes), Remote: remote}
}
