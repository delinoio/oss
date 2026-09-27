package opencode

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const checkpointSnapshotArchive = "snapshot-checkpoint"

type checkpointSnapshotPart struct {
	ID     string   `json:"id"`
	Kind   PartKind `json:"kind"`
	Digest string   `json:"digest"`
	Tree   string   `json:"tree,omitempty"`
}

// Version 1 is positive original Git snapshot evidence, independent of tool
// eligibility. The self-contained pack includes every observed tree and the
// original index tree; external alternates never survive in the archive.
type checkpointSnapshot struct {
	Version   uint32                   `json:"version"`
	Parts     []checkpointSnapshotPart `json:"parts"`
	IndexTree string                   `json:"index_tree"`
	Pack      string                   `json:"pack"`
}

func snapshotObjectID(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha1.Size && hex.EncodeToString(raw) == value
}

func checkpointSnapshotPath(value nativeCheckpoint) string {
	hash := sha1.Sum([]byte(value.NativeRoot))
	return "data/opencode/snapshot/" + value.Project + "/" + hex.EncodeToString(hash[:])
}

func snapshotPartKind(kind PartKind) bool {
	return kind == StepStartPartKind || kind == StepFinishPartKind || kind == SnapshotPartKind || kind == PatchPartKind
}

func validCheckpointSnapshot(value nativeCheckpoint) bool {
	proof := value.Snapshot
	if proof == nil {
		return true
	}
	if proof.Version != 1 || proof.Parts == nil || len(proof.Parts) > maxObservedParts || value.NativeRoot != value.Workspace || !snapshotProjectComponent(value.Project) || !snapshotObjectID(proof.IndexTree) || !snapshotObjectID(proof.Pack) {
		return false
	}
	index := 0
	for _, history := range checkpointHistories(value) {
		for _, message := range history.Messages {
			for _, part := range message.Parts {
				if !snapshotPartKind(part.Kind) {
					continue
				}
				if index >= len(proof.Parts) {
					return false
				}
				stored := proof.Parts[index]
				if stored.ID != part.ID || stored.Kind != part.Kind || stored.Digest != part.Digest || stored.Tree != "" && !snapshotObjectID(stored.Tree) || (part.Kind == SnapshotPartKind || part.Kind == PatchPartKind) && stored.Tree == "" {
					return false
				}
				index++
			}
		}
	}
	if index != len(proof.Parts) {
		return false
	}
	required := map[string]bool{"config": false, "HEAD": false, "index": false, "objects/pack/pack-" + proof.Pack + ".pack": false, "objects/pack/pack-" + proof.Pack + ".idx": false}
	directories := map[string]bool{"": false, "objects": false, "objects/pack": false, "objects/info": false, "refs": false}
	for _, file := range value.Files {
		if file.Path != checkpointSnapshotArchive && !strings.HasPrefix(file.Path, checkpointSnapshotArchive+"/") {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(file.Path, checkpointSnapshotArchive), "/")
		if file.Directory {
			if _, known := directories[relative]; !known {
				return false
			}
			directories[relative] = true
		} else {
			if _, known := required[relative]; !known || file.Size == 0 {
				return false
			}
			required[relative] = true
		}
	}
	for _, found := range directories {
		if !found {
			return false
		}
	}
	for _, found := range required {
		if !found {
			return false
		}
	}
	return true
}

func snapshotProjectComponent(value string) bool {
	// Native project caches are not necessarily hashes. Preserve a safe exact
	// component, never normalize an arbitrary cached ID into a new identity.
	return fs.ValidPath(value) && value != "." && !strings.ContainsAny(value, "/\\:")
}

func (s *sessionAPI) snapshotObservation(value nativeCheckpoint) (*checkpointSnapshot, error) {
	if value.NativeRoot != value.Workspace || !snapshotProjectComponent(value.Project) {
		return nil, incompatible()
	}
	proof := &checkpointSnapshot{Version: 1, Parts: []checkpointSnapshotPart{}}
	if s.predecessor != nil {
		if s.predecessor.Snapshot == nil || !validCheckpointSnapshot(*s.predecessor) {
			return nil, incompatible()
		}
		proof.Parts = append(proof.Parts, s.predecessor.Snapshot.Parts...)
	}
	s.observer.mu.Lock()
	defer s.observer.mu.Unlock()
	if s.observer.problem != nil {
		return nil, sessionUncertain()
	}
	for _, message := range value.History.Messages {
		for _, part := range message.Parts {
			if !snapshotPartKind(part.Kind) {
				continue
			}
			observed := s.observer.parts[part.ID]
			if observed == nil || observed.value.Kind != part.Kind || mutationDigest(observed.raw) != part.Digest {
				if s.logger != nil {
					s.logger.Warn("opencode_snapshot_part_comparison_failed", "part_kind", part.Kind, "observed", observed != nil)
				}
				return nil, sessionUncertain()
			}
			var tree string
			switch part.Kind {
			case StepStartPartKind, StepFinishPartKind:
				if observed.value.Step == nil {
					return nil, sessionUncertain()
				}
				if observed.value.Step.Snapshot != nil {
					tree = *observed.value.Step.Snapshot
				}
			case SnapshotPartKind:
				if observed.value.Snapshot == nil {
					return nil, sessionUncertain()
				}
				tree = *observed.value.Snapshot
			case PatchPartKind:
				if observed.value.Patch == nil {
					return nil, sessionUncertain()
				}
				tree = observed.value.Patch.Hash
			}
			if tree != "" && !snapshotObjectID(tree) || (part.Kind == SnapshotPartKind || part.Kind == PatchPartKind) && tree == "" {
				return nil, incompatible()
			}
			proof.Parts = append(proof.Parts, checkpointSnapshotPart{part.ID, part.Kind, part.Digest, tree})
		}
	}
	return proof, nil
}

func stageCheckpointSnapshot(ctx context.Context, source nativeCheckpoint, home string) error {
	return stageCheckpointSnapshotAs(ctx, source, home, source.Project)
}
func stageCheckpointSnapshotAs(ctx context.Context, source nativeCheckpoint, home, project string) error {
	if source.Snapshot == nil {
		return nil
	}
	if !validCheckpointSnapshot(source) || !snapshotProjectComponent(project) {
		return sessionUncertain()
	}
	root, err := os.OpenRoot(source.RuntimeHome)
	if err != nil {
		return sessionUncertain()
	}
	defer root.Close()
	targetPath := source
	targetPath.Project = project
	destination := filepath.Join(home, filepath.FromSlash(checkpointSnapshotPath(targetPath)))
	homeRoot, err := os.OpenRoot(home)
	if err != nil {
		return sessionUncertain()
	}
	defer homeRoot.Close()
	for _, name := range []string{"data/opencode/snapshot", "data/opencode/snapshot/" + project, checkpointSnapshotPath(targetPath)} {
		if err := homeRoot.Mkdir(name, 0700); err != nil {
			// An adopted project shares only verified private parent folders.
			// Its final snapshot directory must still be created exclusively.
			if !os.IsExist(err) || name == checkpointSnapshotPath(targetPath) || security.CheckPrivateDir(filepath.Join(home, filepath.FromSlash(name))) != nil || !canonicalDirectory(filepath.Join(home, filepath.FromSlash(name))) {
				return sessionUncertain()
			}
		}
	}
	target, err := os.OpenRoot(destination)
	if err != nil {
		return sessionUncertain()
	}
	defer target.Close()
	for _, file := range source.Files {
		if !strings.HasPrefix(file.Path, checkpointSnapshotArchive+"/") {
			continue
		}
		relative := strings.TrimPrefix(file.Path, checkpointSnapshotArchive+"/")
		if file.Directory {
			if target.Mkdir(relative, 0700) != nil {
				return sessionUncertain()
			}
		} else if err := copyCheckpointFileTo(ctx, root, target, file, relative); err != nil {
			return err
		}
	}
	return syncSnapshotDirectories(home, destination)
}

func snapshotTrees(proof *checkpointSnapshot) []string {
	trees := []string{proof.IndexTree}
	for _, part := range proof.Parts {
		if part.Tree != "" && !slices.Contains(trees, part.Tree) {
			trees = append(trees, part.Tree)
		}
	}
	return trees
}

func checkpointHasSnapshotFiles(value nativeCheckpoint) bool {
	prefix := checkpointSnapshotPath(value)
	for _, file := range value.Files {
		if file.Path == prefix || strings.HasPrefix(file.Path, prefix+"/") {
			return true
		}
	}
	return false
}

func syncSnapshotDirectories(home, archive string) error {
	seen := map[string]bool{}
	for _, relative := range []string{"objects/pack", "objects/info", "refs"} {
		for directory := filepath.Join(archive, filepath.FromSlash(relative)); directoryContains(home, directory); directory = filepath.Dir(directory) {
			if !seen[directory] {
				if security.SyncParent(filepath.Join(directory, ".snapshot-sync")) != nil {
					return sessionUncertain()
				}
				seen[directory] = true
			}
			if directory == home {
				break
			}
		}
	}
	return nil
}
