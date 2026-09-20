package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const ReleaseIdentity = "https://github.com/delinoio/oss/.github/workflows/release-async-commit-hook.yml@refs/heads/main"

type UpdateJournal struct {
	Executable     string  `json:"executable"`
	Candidate      string  `json:"candidate"`
	Backup         string  `json:"backup"`
	StateBackup    string  `json:"state_backup"`
	SHA256         string  `json:"sha256"`
	OriginalSHA256 string  `json:"original_sha256"`
	Phase          string  `json:"phase"`
	Parent         Process `json:"parent"`
}

func fetchRelease(ctx context.Context, address string, limit int64) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || len(via) > 5 {
			return E("update-redirect-denied", "release download requires bounded HTTPS redirects", 3)
		}
		return nil
	}}
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if e != nil {
		return nil, e
	}
	r.Header.Set("User-Agent", "ach/"+Version)
	response, e := client.Do(r)
	if e != nil {
		return nil, E("update-download-failed", "could not download the selected release", 3)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, E("update-download-failed", fmt.Sprintf("release server returned HTTP %d", response.StatusCode), 3)
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, E("update-artifact-too-large", "release asset exceeds the accepted bound", 3)
	}
	return b, nil
}
func VerifyReleaseFile(path, bundlePath string) error {
	trusted, e := root.FetchTrustedRoot()
	if e != nil {
		return E("signature-trust-unavailable", "cannot load authenticated Sigstore trust roots", 3)
	}
	return verifyReleaseWithTrust(path, bundlePath, trusted)
}
func releaseVerifier(trusted root.TrustedMaterial) (*verify.SignedEntityVerifier, verify.CertificateIdentity, error) {
	identity, err := verify.NewShortCertificateIdentity("https://token.actions.githubusercontent.com", "", ReleaseIdentity, "")
	if err != nil {
		return nil, identity, err
	}
	v, err := verify.NewVerifier(trusted, verify.WithTransparencyLog(1), verify.WithIntegratedTimestamps(1))
	return v, identity, err
}
func verifyReleaseWithTrust(path, bundlePath string, trusted root.TrustedMaterial) error {
	verifier, identity, e := releaseVerifier(trusted)
	if e != nil {
		return e
	}
	b, e := bundle.LoadJSONFromPath(bundlePath)
	if e != nil {
		return E("signature-invalid", "cannot parse Sigstore bundle", 3)
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = verifier.Verify(b, verify.NewPolicy(verify.WithArtifact(f), verify.WithCertificateIdentity(identity))); e != nil {
		return E("signature-invalid", "release authenticity verification failed", 3)
	}
	return nil
}
func extractExecutable(archive []byte, windows bool) ([]byte, error) {
	name := "ach"
	if windows {
		name = "ach.exe"
	}
	var out []byte
	if windows {
		r, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if e != nil {
			return nil, e
		}
		for _, f := range r.File {
			if f.Name != name || !f.Mode().IsRegular() || out != nil {
				return nil, E("update-archive-invalid", "archive must contain only ach.exe", 3)
			}
			reader, e := f.Open()
			if e != nil {
				return nil, e
			}
			out, e = io.ReadAll(io.LimitReader(reader, 128*1024*1024+1))
			reader.Close()
			if e != nil {
				return nil, e
			}
		}
	} else {
		gzipReader, e := gzip.NewReader(bytes.NewReader(archive))
		if e != nil {
			return nil, e
		}
		defer gzipReader.Close()
		reader := tar.NewReader(gzipReader)
		for {
			h, e := reader.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, e
			}
			if h.Name != name || h.Typeflag != tar.TypeReg || out != nil {
				return nil, E("update-archive-invalid", "archive must contain one regular ach executable", 3)
			}
			out, e = io.ReadAll(io.LimitReader(reader, 128*1024*1024+1))
			if e != nil {
				return nil, e
			}
		}
	}
	if len(out) == 0 || len(out) > 128*1024*1024 {
		return nil, E("update-archive-invalid", "invalid executable size", 3)
	}
	return out, nil
}
func (s *Service) stateBackup() (backup string, err error) {
	dir := filepath.Join(s.Store.Root, "backups", ID()+"-state")
	// Exclusive creation makes cleanup ownership explicit even if an ID ever
	// collides. Windows applies the account-only ACL at directory creation.
	if e := createStateDirectory(dir); e != nil {
		return "", e
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.removeUpdatePreparation(dir))
			backup = ""
		}
	}()
	db := filepath.Join(dir, "state.sqlite")
	if _, e := s.Store.DB.Exec("VACUUM INTO '" + strings.ReplaceAll(db, "'", "''") + "'"); e != nil {
		return "", e
	}
	e := filepath.WalkDir(filepath.Join(s.Store.Root, "evidence"), func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(s.Store.Root, path)
		if e != nil {
			return e
		}
		target := filepath.Join(dir, relative)
		if d.IsDir() {
			return PrivateDir(target)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return E("unsafe-backup", "owned evidence contains a symlink", 3)
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		if e == nil {
			e = out.Sync()
		}
		closeErr := out.Close()
		if e == nil {
			e = closeErr
		}
		return e
	})
	return dir, e
}
func (s *Service) SelfUpdate(ctx context.Context, version string) (map[string]string, error) {
	lock, e := TryLock(filepath.Join(s.Paths.Control, "lifecycle.lock"))
	if e != nil {
		return nil, e
	}
	if lock == nil {
		return nil, E("update-busy", "another startup/update is active", 3)
	}
	defer lock.Close()
	active, e := s.Active()
	if e != nil {
		return nil, e
	}
	pending, e := s.Store.Pending()
	if e != nil {
		return nil, e
	}
	if len(active) > 0 || len(pending) > 0 {
		return nil, E("update-active", "stop daemon/viewers/MCP and finish or cancel checks before self-update", 2)
	}
	executable, e := os.Executable()
	if e != nil {
		return nil, e
	}
	executable, e = filepath.EvalSymlinks(executable)
	if e != nil {
		return nil, e
	}
	if strings.Contains(filepath.ToSlash(executable), "/Cellar/") || strings.Contains(filepath.ToSlash(executable), "/Homebrew/") {
		return nil, E("homebrew-owned", "use brew upgrade async-commit-hook; package-owned files are not replaced", 2)
	}
	version, e = selectUpdateVersion(version, Version, func(page int) ([]byte, error) {
		return fetchRelease(ctx, fmt.Sprintf("https://api.github.com/repos/delinoio/oss/releases?per_page=100&page=%d", page), 8*1024*1024)
	})
	if e != nil {
		return nil, e
	}
	asset := fmt.Sprintf("ach-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset = fmt.Sprintf("ach-windows-%s.zip", runtime.GOARCH)
	}
	base := "https://github.com/delinoio/oss/releases/download/async-commit-hook@v" + version + "/"
	stage, e := os.MkdirTemp(filepath.Dir(executable), ".ach-update-*")
	if e != nil {
		return nil, E("update-directory-unwritable", "direct installation directory is not writable", 3)
	}
	defer os.RemoveAll(stage)
	for _, name := range []string{asset, asset + ".sigstore.json", "SHA256SUMS", "SHA256SUMS.sigstore.json"} {
		b, e := fetchRelease(ctx, base+name, 128*1024*1024)
		if e != nil {
			return nil, e
		}
		if e = AtomicWrite(filepath.Join(stage, name), b, 0600); e != nil {
			return nil, e
		}
	}
	if e = VerifyReleaseFile(filepath.Join(stage, "SHA256SUMS"), filepath.Join(stage, "SHA256SUMS.sigstore.json")); e != nil {
		return nil, e
	}
	if e = VerifyReleaseFile(filepath.Join(stage, asset), filepath.Join(stage, asset+".sigstore.json")); e != nil {
		return nil, e
	}
	archive, e := os.ReadFile(filepath.Join(stage, asset))
	if e != nil {
		return nil, e
	}
	sums, e := os.ReadFile(filepath.Join(stage, "SHA256SUMS"))
	if e != nil {
		return nil, e
	}
	matches := 0
	for _, line := range strings.Split(string(sums), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == asset && parts[0] == Hash(archive) {
			matches++
		}
	}
	if matches != 1 {
		return nil, E("checksum-invalid", "signed checksum manifest does not match the selected archive", 3)
	}
	binary, e := extractExecutable(archive, runtime.GOOS == "windows")
	if e != nil {
		return nil, e
	}
	journal, changed, e := s.prepareVerifiedUpdate(ctx, executable, version, binary)
	if e != nil {
		return nil, e
	}
	if !changed {
		s.Log.Info("update.unchanged", "version", version)
		return map[string]string{"status": "up-to-date", "version": version}, nil
	}
	backup, candidate := journal.StateBackup, journal.Candidate
	if runtime.GOOS == "windows" {
		helper := exec.Command(candidate, "internal", "apply-update", "--config", s.Paths.Config)
		Detached(helper)
		if e = helper.Start(); e != nil {
			return nil, e
		}
		_ = helper.Process.Release()
		return map[string]string{"status": "replacement-pending", "version": version, "state_backup": backup}, nil
	}
	if e = s.applyUpdate(journal); e != nil {
		return nil, e
	}
	return map[string]string{"status": "updated", "version": version, "state_backup": backup, "binary_backup": journal.Backup}, nil
}

// Called only after both Sigstore bundles, the signed checksum and the archive
// structure have been verified, while SelfUpdate holds the lifecycle lock.
func (s *Service) prepareVerifiedUpdate(ctx context.Context, executable, version string, binary []byte) (_ UpdateJournal, _ bool, err error) {
	oldBinary, err := os.ReadFile(executable)
	if err != nil {
		return UpdateJournal{}, false, err
	}
	digest, originalDigest := Hash(binary), Hash(oldBinary)
	if digest == originalDigest {
		// Probe the identical installed bytes without creating a candidate, state
		// backup, binary backup or journal. Version equality alone is insufficient.
		return UpdateJournal{}, false, verifyUpdateVersion(ctx, executable, version)
	}
	id := ID()
	candidate := filepath.Join(filepath.Dir(executable), ".ach-new-"+id)
	if runtime.GOOS == "windows" {
		candidate += ".exe"
	}
	prepared := false
	backup := ""
	defer func() {
		if !prepared {
			err = errors.Join(err, s.removeUpdatePreparation(candidate, backup))
		}
	}()
	if err = AtomicWrite(candidate, binary, 0700); err != nil {
		return UpdateJournal{}, false, err
	}
	if err = verifyUpdateVersion(ctx, candidate, version); err != nil {
		return UpdateJournal{}, false, err
	}
	backup, err = s.stateBackup()
	if err != nil {
		return UpdateJournal{}, false, Wrap("update-backup-failed", err)
	}
	parent, err := ProcessIdentity(os.Getpid())
	if err != nil {
		return UpdateJournal{}, false, err
	}
	journal := UpdateJournal{Executable: executable, Candidate: candidate, Backup: executable + ".ach-backup-" + id, StateBackup: backup, SHA256: digest, OriginalSHA256: originalDigest, Phase: "prepared", Parent: parent}
	journalPath := filepath.Join(s.Paths.Control, "update.json")
	if err = AtomicWrite(journalPath, Encode(journal), 0600); err != nil {
		// Unix rename can succeed before directory sync fails. An already
		// published journal must retain its referenced files for recovery.
		published, readErr := os.ReadFile(journalPath)
		if readErr == nil && bytes.Equal(published, Encode(journal)) {
			prepared = true
			s.Log.Warn("update.journal_publication_uncertain", "code", "update-recovery-required")
			return UpdateJournal{}, false, E("update-recovery-required", "update journal was published but durability could not be confirmed; run self-update --recover", 3)
		}
		return UpdateJournal{}, false, err
	}
	prepared = true
	return journal, true, nil
}

// Discard only newly created preparation paths. Failed cleanup is returned and
// logged, never hidden behind the original preparation error.
func (s *Service) removeUpdatePreparation(paths ...string) error {
	var result error
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			s.Log.Warn("update.preparation_cleanup_failed", "code", "update-cleanup-failed")
			result = errors.Join(result, Wrap("update-cleanup-failed", err))
		}
	}
	return result
}

func verifyUpdateVersion(ctx context.Context, executable, version string) error {
	probe := exec.CommandContext(ctx, executable, "version", "--json")
	versionOutput, err := probe.Output()
	var envelope struct {
		Result struct {
			Version string `json:"version"`
		} `json:"result"`
	}
	if err != nil || json.Unmarshal(versionOutput, &envelope) != nil || envelope.Result.Version != version {
		return E("update-version-invalid", "verified candidate cannot report the selected release version", 3)
	}
	return nil
}

func (s *Service) applyUpdate(j UpdateJournal) error {
	b, e := os.ReadFile(j.Candidate)
	if e != nil || Hash(b) != j.SHA256 {
		return E("update-candidate-invalid", "prepared candidate is unavailable or changed", 3)
	}
	// Journal the next operation before moving the original. Recovery also handles a
	// crash between the move and the candidate replacement.
	j.Phase = "original-backed-up"
	if e = AtomicWrite(filepath.Join(s.Paths.Control, "update.json"), Encode(j), 0600); e != nil {
		return e
	}
	if e = os.Rename(j.Executable, j.Backup); e != nil {
		return Wrap("update-backup-failed", e)
	}
	if e = replaceFile(j.Candidate, j.Executable); e != nil {
		if rollback := os.Rename(j.Backup, j.Executable); rollback != nil {
			return E("update-recovery-required", "replacement and rollback failed; restore the recorded binary backup", 3)
		}
		_ = os.Remove(filepath.Join(s.Paths.Control, "update.json"))
		return E("update-replaced-failed", "replacement failed; original executable restored", 3)
	}
	j.Phase = "installed"
	if e = AtomicWrite(filepath.Join(s.Paths.Control, "update.json"), Encode(j), 0600); e != nil {
		return e
	}
	return os.Remove(filepath.Join(s.Paths.Control, "update.json"))
}
func (s *Service) ApplyUpdate() error {
	b, e := os.ReadFile(filepath.Join(s.Paths.Control, "update.json"))
	if e != nil {
		return e
	}
	var j UpdateJournal
	if e = json.Unmarshal(b, &j); e != nil {
		return e
	}
	for ProcessAlive(j.Parent) {
		time.Sleep(50 * time.Millisecond)
	}
	return s.applyPreparedUpdate(b)
}

func (s *Service) applyPreparedUpdate(expected []byte) error {
	lock, e := TryLock(filepath.Join(s.Paths.Control, "lifecycle.lock"))
	if e != nil {
		return e
	}
	if lock == nil {
		return E("update-busy", "lifecycle owner has not stopped", 3)
	}
	defer lock.Close()
	// Recovery can win while this helper waits for its parent. Never replay the
	// previously decoded journal after recovery removed it or another update
	// replaced it. The same lock protects this comparison through replacement.
	current, e := os.ReadFile(filepath.Join(s.Paths.Control, "update.json"))
	if e != nil || !bytes.Equal(current, expected) {
		return E("update-superseded", "prepared update was recovered or changed; helper will not apply it", 3)
	}
	var j UpdateJournal
	if e = json.Unmarshal(current, &j); e != nil {
		return e
	}
	if j.Phase != "prepared" {
		return E("update-journal-invalid", "helper requires a prepared update", 3)
	}
	// Windows cannot rename the running helper; copy the authenticated candidate to a replacement sibling.
	if runtime.GOOS == "windows" {
		if e = s.recordUpdateHelper(j); e != nil {
			return e
		}
		candidate, e := os.ReadFile(j.Candidate)
		if e != nil || Hash(candidate) != j.SHA256 {
			return E("update-candidate-invalid", "helper candidate changed", 3)
		}
		j.Candidate += ".replacement"
		if e = AtomicWrite(j.Candidate, candidate, 0700); e != nil {
			return e
		}
	}
	return s.applyUpdate(j)
}

func (s *Service) RecoverUpdate() (map[string]string, error) {
	lock, e := TryLock(filepath.Join(s.Paths.Control, "lifecycle.lock"))
	if e != nil {
		return nil, e
	}
	if lock == nil {
		return nil, E("update-busy", "another lifecycle owner is active", 3)
	}
	defer lock.Close()
	active, e := s.Active()
	if e != nil {
		return nil, e
	}
	if len(active) > 0 {
		return nil, E("update-active", "stop all ach services before recovery", 2)
	}
	path := filepath.Join(s.Paths.Control, "update.json")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return map[string]string{"status": "no-pending-update"}, nil
	}
	if e != nil {
		return nil, e
	}
	var j UpdateJournal
	if e = json.Unmarshal(b, &j); e != nil {
		return nil, e
	}
	if ProcessAlive(j.Parent) {
		return nil, E("update-active", "the updater process is still active", 2)
	}
	if j.Phase == "original-backed-up" {
		if _, e = os.Lstat(j.Executable); os.IsNotExist(e) {
			original, mode, err := authenticatedUpdateBackup(j)
			if err != nil {
				return nil, err
			}
			// Publish the authenticated snapshot, not a backup pathname that
			// could change after verification. Retain the backup for inspection
			// and never overwrite a concurrently restored executable.
			if e = AtomicCreate(j.Executable, original, mode); e != nil {
				return nil, E("update-recovery-required", "cannot restore the recorded binary backup", 3)
			}
		} else {
			current, e := os.ReadFile(j.Executable)
			if e != nil || (Hash(current) != j.SHA256 && Hash(current) != j.OriginalSHA256) {
				return nil, E("update-recovery-required", "installed executable is uncertain; inspect the retained backup", 3)
			}
		}
	}
	if j.Phase != "prepared" && j.Phase != "original-backed-up" && j.Phase != "installed" {
		return nil, E("update-journal-invalid", "unknown update recovery phase", 3)
	}
	if j.Phase == "installed" {
		current, err := os.ReadFile(j.Executable)
		if err != nil || Hash(current) != j.SHA256 {
			return nil, E("update-recovery-required", "installed executable changed after replacement", 3)
		}
	}
	if e = os.Remove(path); e != nil {
		return nil, e
	}
	return map[string]string{"status": "recovered", "state_backup": j.StateBackup, "binary_backup": j.Backup}, nil
}

func authenticatedUpdateBackup(j UpdateJournal) ([]byte, os.FileMode, error) {
	invalid := func() ([]byte, os.FileMode, error) {
		return nil, 0, E("update-recovery-required", "recorded binary backup is unavailable, nonregular or does not match the original digest", 3)
	}
	info, err := os.Lstat(j.Backup)
	if err != nil || !info.Mode().IsRegular() {
		return invalid()
	}
	f, err := os.Open(j.Backup)
	if err != nil {
		return invalid()
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return invalid()
	}
	const maxExecutableBytes = 128 * 1024 * 1024
	if opened.Size() > maxExecutableBytes {
		return invalid()
	}
	b, err := io.ReadAll(io.LimitReader(f, maxExecutableBytes+1))
	if err != nil || len(b) == 0 || len(b) > maxExecutableBytes || Hash(b) != j.OriginalSHA256 {
		return invalid()
	}
	return b, opened.Mode().Perm(), nil
}
