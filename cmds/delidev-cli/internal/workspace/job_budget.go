package workspace

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	// ManifestWorkerRootReserveBytes is the assumed length of the Worker data
	// root that prefixes every prepared path. The server cannot observe it before
	// preparation; the dispatch-time recheck of the real bytes stays authoritative.
	ManifestWorkerRootReserveBytes = 512
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

// MaxManifestBytes returns an upper bound of the serialized Manifest a Worker
// can publish for this request. Every generated identity, digest and commit
// uses its maximum length and every unresolved path or reference uses the
// documented reserves above.
func (r PrepareRequest) MaxManifestBytes() (int, error) {
	digest := strings.Repeat("f", 64)
	root := strings.Repeat("r", ManifestWorkerRootReserveBytes) + "/workspaces/" + string(r.SessionID)
	manifest := Manifest{Version: 1, SessionID: r.SessionID, MachineID: r.MachineID, Type: r.Type, State: CleanupPending, InputDigest: digest, PrimaryPath: root + "/" + string(r.SessionID), Repositories: []PreparedRepository{}, CreatedAt: time.Date(2006, 1, 2, 15, 4, 5, 123456789, time.UTC)}
	if r.Type == domain.GeneralChat {
		manifest.PrimaryPath = root + "/chat"
	}
	if r.SidechatSource != nil || r.ForkProfile == CodexSidechatReferenceV1 {
		manifest.Reference = &SidechatReference{SessionID: r.SessionID, PreparationDigest: digest, ManifestDigest: digest, DirectoryIdentity: digest, MetadataIdentity: digest}
	}
	for _, spec := range r.Repositories {
		managed := spec.SourceKind.managed()
		prepared := PreparedRepository{DirectoryName: spec.DirectoryName, SourceKind: spec.SourceKind, RemoteURL: spec.RemoteURL, PRTarget: spec.PRTarget, ID: spec.ID, Base: spec.Base, Starting: spec.Starting, BaseCommit: digest, StartingCommit: digest, Owned: r.Type == domain.Worktree}
		path := spec.Checkout
		if r.Type == domain.Worktree {
			path = root + "/" + RepositoryDirectory(spec.ID, spec.DirectoryName)
		}
		prepared.Path, prepared.Source = path, spec.Checkout
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
