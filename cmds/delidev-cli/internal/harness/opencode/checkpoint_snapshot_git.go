package opencode

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type snapshotBoundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *snapshotBoundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, io.ErrShortBuffer
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// Retain only the process scope and copied system lookup context. Original
// harness arguments, tokens and unapproved environment never enter this scope.
func checkpointProcessScope(source process.Config) process.Config {
	result := process.Config{Directory: source.Directory}
	for _, entry := range source.Env {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR":
			result.Env = append(result.Env, entry)
		}
	}
	return result
}

// Only the independently owned original job can export after native cleanup.
// These additional read/export children use that job's process journal and all
// join before checkpoint publication. Inspection/recovery never calls Git.
func (s *sessionAPI) snapshotGit(ctx context.Context, directory string, input []byte, output io.Writer, args ...string) (returned error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	binary, err := exec.LookPath("git")
	if err != nil || !filepath.IsAbs(binary) {
		return unavailable()
	}
	config := process.Config{Directory: s.checkpointProcess.Directory, OwnerID: s.owner, Executable: binary, Cwd: directory, Logger: s.logger, Stdout: output, Stderr: &snapshotBoundedWriter{io.Discard, 1 << 20}}
	for _, entry := range s.checkpointProcess.Env {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR":
			config.Env = append(config.Env, entry)
		}
	}
	config.Env = append(config.Env, "HOME="+s.runtimeHome, "USERPROFILE="+s.runtimeHome, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "LC_ALL=C")
	config.Args = append([]string{"--git-dir=" + directory, "--work-tree=" + directory, "-c", "core.hooksPath=" + filepath.Join(directory, "disabled-hooks"), "-c", "core.fsmonitor=false"}, args...)
	handle, err := process.Start(bounded, config)
	if err != nil {
		return sessionUncertain()
	}
	defer func() {
		if handle.Close() != nil {
			returned = sessionUncertain()
		}
	}()
	if handle.Resume() != nil {
		return sessionUncertain()
	}
	// The native ownership controller interrupts a blocked writer on deadline.
	if len(input) != 0 {
		if n, err := handle.Write(input); err != nil || n != len(input) {
			return sessionUncertain()
		}
	}
	if handle.CloseInput() != nil || handle.Wait() != nil || bounded.Err() != nil {
		return sessionUncertain()
	}
	return nil
}

func (s *sessionAPI) retainCheckpointSnapshot(ctx context.Context, value nativeCheckpoint) (proof *checkpointSnapshot, returned error) {
	phase := "observation"
	defer func() {
		if s.logger != nil {
			if returned != nil {
				s.logger.WarnContext(ctx, "opencode_snapshot_checkpoint_failed", "owner_id", s.owner, "phase", phase, "code", domain.SafeError(returned).Code)
			} else {
				s.logger.InfoContext(ctx, "opencode_snapshot_checkpoint_retained", "owner_id", s.owner, "references", len(proof.Parts))
			}
		}
	}()
	proof, err := s.snapshotObservation(value)
	if err != nil {
		return nil, err
	}
	phase = "archive-create"
	archive := filepath.Join(s.runtimeHome, checkpointSnapshotArchive)
	if err := os.Mkdir(archive, 0700); err != nil {
		return nil, sessionUncertain()
	}
	for _, path := range []string{"objects", "objects/info", "objects/pack", "refs"} {
		if os.Mkdir(filepath.Join(archive, filepath.FromSlash(path)), 0700) != nil {
			return nil, sessionUncertain()
		}
	}
	root, err := os.OpenRoot(s.runtimeHome)
	if err != nil {
		return nil, sessionUncertain()
	}
	defer root.Close()
	target, err := os.OpenRoot(archive)
	if err != nil {
		return nil, sessionUncertain()
	}
	defer target.Close()
	phase = "native-index-copy"
	prefix := checkpointSnapshotPath(value) + "/"
	required := map[string]bool{"config": false, "HEAD": false, "index": false}
	var index checkpointFile
	for _, file := range value.Files {
		if !strings.HasPrefix(file.Path, prefix) {
			continue
		}
		relative := strings.TrimPrefix(file.Path, prefix)
		if _, ok := required[relative]; !ok {
			continue
		}
		if file.Directory || file.Size == 0 || file.Size > maxCheckpointFileBytes || relative == "config" && file.Size > 4096 || relative == "HEAD" && file.Size > 1024 {
			return nil, sessionUncertain()
		}
		if copyCheckpointFileTo(ctx, root, target, file, relative) != nil {
			return nil, sessionUncertain()
		}
		required[relative] = true
		if relative == "index" {
			index = file
		}
	}
	for _, found := range required {
		if !found {
			return nil, incompatible()
		}
	}
	// Never let copied configuration execute an include, filter, hook, helper or
	// fsmonitor. The pinned native initializer emits only this closed key family.
	phase = "configuration"
	config, err := os.ReadFile(filepath.Join(archive, "config"))
	if err != nil || !safeSnapshotConfig(config, value.Workspace) {
		return nil, incompatible()
	}
	head, err := os.ReadFile(filepath.Join(archive, "HEAD"))
	if err != nil || !strings.HasPrefix(string(head), "ref: refs/heads/") || strings.Count(string(head), "\n") != 1 || !strings.HasSuffix(string(head), "\n") || strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(string(head), "ref: refs/heads/"), "\n"), " \t\r\\~^:?*[") || strings.Contains(string(head), "..") || strings.Contains(string(head), "@{") {
		return nil, incompatible()
	}
	originalObjects := filepath.Join(s.runtimeHome, filepath.FromSlash(prefix), "objects")
	if strings.ContainsAny(originalObjects, "\r\n") || !canonicalDirectory(originalObjects) {
		return nil, incompatible()
	}
	alternates := filepath.Join(archive, "objects", "info", "alternates")
	if security.WriteAtomic(alternates, []byte(originalObjects+"\n")) != nil {
		return nil, sessionUncertain()
	}
	phase = "index-tree"
	var tree bytes.Buffer
	if err := s.snapshotGit(ctx, archive, nil, &snapshotBoundedWriter{&tree, 128}, "write-tree"); err != nil {
		return nil, err
	}
	proof.IndexTree = strings.TrimSuffix(tree.String(), "\n")
	if !snapshotObjectID(proof.IndexTree) {
		return nil, sessionUncertain()
	}
	// write-tree may refresh only the archive's cache-tree extension. Restore
	// exact original index bytes before pinning and staging native state.
	if target.Remove("index") != nil || copyCheckpointFileTo(ctx, root, target, index, "index") != nil {
		return nil, sessionUncertain()
	}
	phase = "export"
	packPath := filepath.Join(archive, "objects", "pack", "snapshot.pack")
	pack, err := os.OpenFile(packPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, sessionUncertain()
	}
	exportErr := s.snapshotGit(ctx, archive, []byte(strings.Join(snapshotTrees(proof), "\n")+"\n"), &snapshotBoundedWriter{pack, maxCheckpointFileBytes}, "pack-objects", "--stdout", "--revs")
	syncErr, closeErr := pack.Sync(), pack.Close()
	if exportErr != nil || syncErr != nil || closeErr != nil {
		return nil, sessionUncertain()
	}
	phase = "pack-verification"
	var packID bytes.Buffer
	if err := s.snapshotGit(ctx, archive, nil, &snapshotBoundedWriter{&packID, 128}, "index-pack", "--strict", "--no-rev-index", packPath); err != nil {
		return nil, err
	}
	proof.Pack = strings.TrimSuffix(packID.String(), "\n")
	if !snapshotObjectID(proof.Pack) {
		return nil, sessionUncertain()
	}
	for _, suffix := range []string{"pack", "idx"} {
		if os.Rename(filepath.Join(archive, "objects", "pack", "snapshot."+suffix), filepath.Join(archive, "objects", "pack", "pack-"+proof.Pack+"."+suffix)) != nil {
			return nil, sessionUncertain()
		}
	}
	if os.Remove(alternates) != nil {
		return nil, sessionUncertain()
	}
	// write-tree can add loose trees. The full export already contains them;
	// remove only our generated loose directories, never native/source objects.
	entries, err := os.ReadDir(filepath.Join(archive, "objects"))
	if err != nil {
		return nil, sessionUncertain()
	}
	for _, entry := range entries {
		if entry.Name() == "info" || entry.Name() == "pack" {
			continue
		}
		if len(entry.Name()) != 2 || !entry.IsDir() {
			return nil, sessionUncertain()
		}
		if os.RemoveAll(filepath.Join(archive, "objects", entry.Name())) != nil {
			return nil, sessionUncertain()
		}
	}
	phase = "self-contained"
	if err := s.snapshotGit(ctx, archive, nil, &snapshotBoundedWriter{io.Discard, 1 << 20}, "fsck", "--full", "--no-reflogs", "--no-dangling"); err != nil {
		return nil, err
	}
	var types bytes.Buffer
	trees := snapshotTrees(proof)
	if err := s.snapshotGit(ctx, archive, []byte(strings.Join(trees, "\n")+"\n"), &snapshotBoundedWriter{&types, int64(len(trees)) * 128}, "cat-file", "--batch-check=%(objectname) %(objecttype)"); err != nil {
		return nil, err
	}
	expected := ""
	for _, hash := range trees {
		expected += hash + " tree\n"
	}
	if types.String() != expected {
		return nil, sessionUncertain()
	}
	phase = "durability"
	for _, suffix := range []string{"pack", "idx"} {
		name := "objects/pack/pack-" + proof.Pack + "." + suffix
		file, err := target.OpenFile(name, checkpointReadFlags(), 0)
		if err != nil {
			return nil, sessionUncertain()
		}
		info, statErr := file.Stat()
		if statErr != nil || !ownedCheckpointOpenFile(file) || !info.Mode().IsRegular() || file.Chmod(0600) != nil {
			_ = file.Close()
			return nil, sessionUncertain()
		}
		if file.Close() != nil {
			return nil, sessionUncertain()
		}
		writable, err := target.OpenFile(name, checkpointReadFlags()|os.O_RDWR, 0)
		if err != nil {
			return nil, sessionUncertain()
		}
		actual, statErr := writable.Stat()
		valid := statErr == nil && os.SameFile(info, actual) && info.Size() == actual.Size() && ownedCheckpointOpenFile(writable)
		syncErr := writable.Sync()
		closeErr := writable.Close()
		if !valid || syncErr != nil || closeErr != nil {
			return nil, sessionUncertain()
		}
	}
	if syncSnapshotDirectories(s.runtimeHome, archive) != nil {
		return nil, sessionUncertain()
	}
	phase = "original-files"
	files, err := checkpointFiles(ctx, s.runtimeHome)
	if err != nil {
		return nil, err
	}
	original := make([]checkpointFile, 0, len(value.Files))
	for _, file := range files {
		if file.Path != checkpointSnapshotArchive && !strings.HasPrefix(file.Path, checkpointSnapshotArchive+"/") {
			original = append(original, file)
		}
	}
	if !sameCheckpointFiles(original, value.Files) {
		return nil, sessionUncertain()
	}
	return proof, nil
}

func safeSnapshotConfig(raw []byte, workspace string) bool {
	if len(raw) > 4096 {
		return false
	}
	allowed := map[string]map[string]bool{
		"core.repositoryformatversion": {"0": true}, "core.filemode": {"true": true, "false": true}, "core.bare": {"false": true}, "core.logallrefupdates": {"true": true}, "core.ignorecase": {"true": true, "false": true}, "core.precomposeunicode": {"true": true, "false": true},
		"core.autocrlf": {"false": true}, "core.longpaths": {"true": true}, "core.symlinks": {"true": true}, "core.fsmonitor": {"false": true}, "core.untrackedcache": {"true": true}, "feature.manyfiles": {"true": true}, "index.version": {"4": true}, "index.threads": {"true": true},
	}
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\t", "\\t", "\b", "\\b").Replace(workspace)
	allowed["core.worktree"] = map[string]bool{"\"" + escaped + "\"": true}
	if !strings.ContainsAny(workspace, "\\\"#;\n\r\t\b") && strings.TrimSpace(workspace) == workspace {
		allowed["core.worktree"][workspace] = true
	}
	seen := map[string]bool{}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if section != "core" && section != "index" && section != "feature" {
				return false
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = section + "." + strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !ok || seen[key] || !allowed[key][value] {
			return false
		}
		seen[key] = true
	}
	for _, key := range []string{"core.repositoryformatversion", "core.bare", "core.filemode", "core.worktree", "core.autocrlf", "core.fsmonitor", "core.longpaths", "core.symlinks", "core.untrackedcache", "feature.manyfiles", "index.version", "index.threads"} {
		if !seen[key] {
			return false
		}
	}
	return true
}
