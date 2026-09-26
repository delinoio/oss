package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const snapshotFixtureConfig = `[core]
 repositoryformatversion = 0
 filemode = true
 bare = false
 worktree = /private/workspace
 autocrlf = false
 longpaths = true
 symlinks = true
 fsmonitor = false
 untrackedCache = true
[feature]
 manyFiles = true
[index]
 version = 4
 threads = true
`

func TestSnapshotConfigRejectsExecutableOrForeignSettings(t *testing.T) {
	for _, bad := range []string{"[include]\n path = /private/foreign", "[core]\n hooksPath = /private/hook", "[filter \"unsafe\"]\n clean = command", "[remote \"origin\"]\n url = https://invalid.example", "[extensions]\n objectFormat = sha256", "[core]\n fsmonitor = command", "[core]\n bare = false"} {
		if safeSnapshotConfig([]byte(snapshotFixtureConfig+bad), "/private/workspace") {
			t.Fatal("unverified configuration accepted")
		}
	}
	for _, change := range []struct{ from, to string }{{"false", "true"}, {"version = 4", "version = 2"}, {"threads = true", "threads = false"}, {"worktree = /private/workspace", "worktree = /private/foreign"}, {"symlinks = true\n", ""}} {
		raw := strings.Replace(snapshotFixtureConfig, change.from, change.to, 1)
		if safeSnapshotConfig([]byte(raw), "/private/workspace") {
			t.Fatal("native setting changed")
		}
	}
	for _, workspace := range []string{"/private/workspace", "/private/space name", "/private/#quoted;name", "/private/quote\"name", "C:\\private\\workspace"} {
		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(workspace)
		raw := strings.Replace(snapshotFixtureConfig, "worktree = /private/workspace", "worktree = \""+escaped+"\"", 1)
		if !safeSnapshotConfig([]byte(raw), workspace) {
			t.Fatal("exact quoted native worktree rejected")
		}
	}
	if !safeSnapshotConfig([]byte(snapshotFixtureConfig), "/private/workspace") {
		t.Fatal("native profile rejected")
	}
}

// These synthetic archives exercise metadata/copy refusal only. Real native
// tests separately prove original Git export and self-contained object closure.
func snapshotMetadataFixture(t *testing.T) nativeCheckpoint {
	t.Helper()
	api, home := completedCheckpointFixture(t)
	raw, ref, err := api.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := api.session.snapshotObservation(value)
	if err != nil {
		t.Fatal(err)
	}
	proof.IndexTree, proof.Pack = strings.Repeat("ab", 20), strings.Repeat("cd", 20)
	value.Snapshot = proof
	archive := filepath.Join(home, checkpointSnapshotArchive)
	for _, name := range []string{"", "objects", "objects/pack", "objects/info", "refs"} {
		if err := security.PrivateDir(filepath.Join(archive, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"config", "HEAD", "index", "objects/pack/pack-" + proof.Pack + ".pack", "objects/pack/pack-" + proof.Pack + ".idx"} {
		if err := security.WriteAtomic(filepath.Join(archive, filepath.FromSlash(name)), []byte("private fixture "+name)); err != nil {
			t.Fatal(err)
		}
	}
	value.Files, err = checkpointFiles(context.Background(), home)
	if err != nil || !validCheckpointSnapshot(value) {
		t.Fatal("invalid snapshot metadata fixture", err)
	}
	return value
}

func TestSnapshotProofRequiresCompleteOriginalLineageAndArchive(t *testing.T) {
	source := snapshotMetadataFixture(t)
	if checkpointReplacementProfile(source) != nil {
		t.Fatal("positive project profile refused")
	}
	for _, mode := range []string{"legacy", "version", "missing-parts", "extra-part", "digest", "kind", "tree", "index-tree", "pack", "root", "project", "missing-file", "missing-directory", "alternate", "extra-directory"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(source)
			var value nativeCheckpoint
			if json.Unmarshal(raw, &value) != nil {
				t.Fatal("fixture decode")
			}
			switch mode {
			case "legacy":
				value.Snapshot = nil
			case "version":
				value.Snapshot.Version++
			case "missing-parts":
				value.Snapshot.Parts = nil
			case "extra-part":
				value.Snapshot.Parts = append(value.Snapshot.Parts, value.Snapshot.Parts[0])
			case "digest":
				value.Snapshot.Parts[0].Digest = strings.Repeat("00", 32)
			case "kind":
				value.Snapshot.Parts[0].Kind = PatchPartKind
			case "tree":
				value.Snapshot.Parts[0].Tree = "not-a-tree"
			case "index-tree":
				value.Snapshot.IndexTree = ""
			case "pack":
				value.Snapshot.Pack = strings.Repeat("ff", 20)
			case "root":
				value.NativeRoot = filepath.Dir(value.NativeRoot)
			case "project":
				value.Project = "../foreign"
			case "missing-file", "missing-directory":
				wanted := checkpointSnapshotArchive + "/index"
				if mode == "missing-directory" {
					wanted = checkpointSnapshotArchive + "/refs"
				}
				for i, file := range value.Files {
					if file.Path == wanted {
						value.Files = append(value.Files[:i], value.Files[i+1:]...)
						break
					}
				}
			case "alternate":
				value.Files = append(value.Files, checkpointFile{Path: checkpointSnapshotArchive + "/objects/info/alternates", Size: 10})
			case "extra-directory":
				value.Files = append(value.Files, checkpointFile{Path: checkpointSnapshotArchive + "/hooks", Directory: true})
			}
			if checkpointReplacementProfile(value) == nil {
				t.Fatal("incomplete or foreign project proof became restorable")
			}
		})
	}
	// Snapshot/Patch parts require explicit tree references in their own proof.
	for _, kind := range []PartKind{SnapshotPartKind, PatchPartKind} {
		raw, _ := json.Marshal(source)
		var value nativeCheckpoint
		_ = json.Unmarshal(raw, &value)
		part := &value.History.Messages[1].Parts[0]
		part.Kind = kind
		value.Snapshot.Parts[0].Kind = kind
		if validCheckpointSnapshot(value) {
			t.Fatal("missing snapshot tree accepted")
		}
		value.Snapshot.Parts[0].Tree = value.Snapshot.IndexTree
		if !validCheckpointSnapshot(value) {
			t.Fatal("complete snapshot reference rejected")
		}
	}
}

func TestSnapshotStagingPreservesExactClosedArchiveAndCurrentWorkspace(t *testing.T) {
	value := snapshotMetadataFixture(t)
	marker := filepath.Join(value.Workspace, "later.txt")
	if err := os.WriteFile(marker, []byte("later workspace change"), 0600); err != nil {
		t.Fatal(err)
	}
	home := checkpointRuntimeFixture(t)
	if err := stageCheckpointSnapshot(context.Background(), value, home); err != nil {
		t.Fatal(err)
	}
	for _, file := range value.Files {
		if file.Directory || !strings.HasPrefix(file.Path, checkpointSnapshotArchive+"/") {
			continue
		}
		before, err := os.ReadFile(filepath.Join(value.RuntimeHome, filepath.FromSlash(file.Path)))
		after, other := os.ReadFile(filepath.Join(home, filepath.FromSlash(checkpointSnapshotPath(value)), filepath.FromSlash(strings.TrimPrefix(file.Path, checkpointSnapshotArchive+"/"))))
		if err != nil || other != nil || !bytes.Equal(before, after) {
			t.Fatal("staged snapshot changed original bytes")
		}
	}
	files, err := checkpointFiles(context.Background(), value.RuntimeHome)
	if err != nil || !sameCheckpointFiles(files, value.Files) {
		t.Fatal("original archive mutated")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "later workspace change" {
		t.Fatal("workspace was restored from old snapshot")
	}
	if stageCheckpointSnapshot(context.Background(), value, home) == nil {
		t.Fatal("occupied snapshot destination replaced")
	}
	if err := os.WriteFile(filepath.Join(value.RuntimeHome, checkpointSnapshotArchive, "index"), []byte("changed original index"), 0600); err != nil {
		t.Fatal(err)
	}
	if stageCheckpointSnapshot(context.Background(), value, checkpointRuntimeFixture(t)) == nil {
		t.Fatal("changed original archive copied")
	}
}

func TestSnapshotProcessScopeExcludesOriginalHarnessAuthority(t *testing.T) {
	source := process.Config{Directory: "/private/processes", Executable: "/private/native", Args: []string{"private argument"}, Env: []string{"PATH=/approved/bin", "TMPDIR=/private/temp", "OPENAI_API_KEY=private", "SSH_AUTH_SOCK=/private/socket", "GIT_CONFIG_GLOBAL=/private/config", "OPENCODE_CONFIG_CONTENT=private"}}
	scope := checkpointProcessScope(source)
	source.Env[0] = "PATH=/changed"
	if scope.Directory != source.Directory || scope.Executable != "" || scope.Args != nil || scope.OwnerID != "" || len(scope.Env) != 2 || scope.Env[0] != "PATH=/approved/bin" || scope.Env[1] != "TMPDIR=/private/temp" {
		t.Fatal("snapshot export retained or aliased original harness authority")
	}
}
