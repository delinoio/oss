package runmoor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type serviceUninstallStage string

const (
	uninstallClaimPending   serviceUninstallStage = "claim_pending"
	uninstallClaimed        serviceUninstallStage = "claimed"
	uninstallRestorePending serviceUninstallStage = "restore_pending"
	uninstallRestored       serviceUninstallStage = "restored"
	uninstallDeletePending  serviceUninstallStage = "delete_pending"
	uninstallDeleted        serviceUninstallStage = "deleted"
)

// Private references and digests only: never copy an external writer's bytes
// into recovery intent. The service-operation lock owns every transition.
type serviceUninstallJournal struct {
	Schema         int                   `json:"schema"`
	Token          string                `json:"token"`
	Platform       string                `json:"platform"`
	Unit           string                `json:"unit"`
	Config         string                `json:"config"`
	OriginalID     string                `json:"original_id"`
	OriginalDigest string                `json:"original_digest"`
	ClaimID        string                `json:"claim_id,omitempty"`
	ClaimDigest    string                `json:"claim_digest,omitempty"`
	Stage          serviceUninstallStage `json:"stage"`
}

type serviceUninstaller struct {
	unit     string
	rename   func(string, string) error
	syncDir  func(string) error
	boundary func(string) error
}

func uninstallJournalPath(unit string) string {
	return filepath.Join(filepath.Dir(unit), ".runmoor-uninstall-"+filepath.Base(unit)+".json")
}
func uninstallClaimPath(j *serviceUninstallJournal) string {
	return filepath.Join(filepath.Dir(j.Unit), ".runmoor-uninstall-"+j.Token+".claim")
}
func uninstallFailure() error {
	return problem(ErrRetry, "Service uninstall needs reconciliation.", "Preserve the service definition and private uninstall recovery files. Retry explicit service uninstall with the original configuration after resolving conflicts; do not remove or repair unknown claims.")
}
func validDefinitionDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == value
}
func readUninstallJournal(unit string) (*serviceUninstallJournal, error) {
	path := uninstallJournalPath(unit)
	if absentReloadFile(path) {
		return nil, nil
	}
	body, err := readPrivate(path, serviceDefinitionLimit)
	if err != nil {
		return nil, uninstallFailure()
	}
	var j serviceUninstallJournal
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&j) != nil || d.Decode(&extra) != io.EOF || j.Schema != 1 || !validID(j.Token) || j.Unit != unit || j.OriginalID == "" || !validDefinitionDigest(j.OriginalDigest) || (j.Platform != "darwin" && j.Platform != "linux") {
		return nil, uninstallFailure()
	}
	for _, path := range []string{j.Unit, j.Config} {
		clean, err := cleanAbsoluteServicePath(path)
		if err != nil || clean != path {
			return nil, uninstallFailure()
		}
	}
	switch j.Stage {
	case uninstallClaimPending:
		if j.ClaimID != "" || j.ClaimDigest != "" {
			return nil, uninstallFailure()
		}
	case uninstallClaimed, uninstallRestorePending, uninstallRestored, uninstallDeletePending, uninstallDeleted:
		if j.ClaimID == "" || !validDefinitionDigest(j.ClaimDigest) {
			return nil, uninstallFailure()
		}
		if (j.Stage == uninstallDeletePending || j.Stage == uninstallDeleted) && !uninstallOriginalClaim(&j) {
			return nil, uninstallFailure()
		}
	default:
		return nil, uninstallFailure()
	}
	return &j, nil
}
func (u *serviceUninstaller) sync() error {
	if u.syncDir != nil {
		return u.syncDir(filepath.Dir(u.unit))
	}
	return syncPrivateDir(filepath.Dir(u.unit))
}
func (u *serviceUninstaller) move(from, to string) error {
	if u.rename != nil {
		return u.rename(from, to)
	}
	return renameServiceDefinitionNoReplace(from, to)
}
func (u *serviceUninstaller) checkpoint(name string) error {
	if u.boundary != nil {
		return u.boundary(name)
	}
	return nil
}
func (u *serviceUninstaller) save(j *serviceUninstallJournal, next serviceUninstallJournal) error {
	old, _ := json.Marshal(j)
	body, _ := json.Marshal(&next)
	if err := replaceReloadFile(uninstallJournalPath(u.unit), old, serviceDefinitionLimit, body); err != nil {
		return uninstallFailure()
	}
	*j = next
	slog.Info("service_uninstall_stage", "stage", j.Stage)
	return nil
}
func (u *serviceUninstaller) stage(j *serviceUninstallJournal, stage serviceUninstallStage) error {
	next := *j
	next.Stage = stage
	return u.save(j, next)
}
func uninstallOriginalClaim(j *serviceUninstallJournal) bool {
	return j.ClaimID == j.OriginalID && j.ClaimDigest == j.OriginalDigest
}
func matchesUninstallFile(path, id, digest string) bool {
	body, info, err := readPrivateServiceDefinition(path)
	return err == nil && hostFileIdentity(info) == id && definitionDigest(body) == digest
}
func (u *serviceUninstaller) begin(platform, config string, snapshot serviceDefinitionSnapshot) error {
	if pending, err := readUninstallJournal(u.unit); err != nil || pending != nil {
		return uninstallFailure()
	}
	config, err := filepath.Abs(config)
	if err == nil {
		config, err = cleanAbsoluteServicePath(config)
	}
	if err != nil {
		return uninstallFailure()
	}
	j := &serviceUninstallJournal{Schema: 1, Token: newID(), Platform: platform, Unit: u.unit, Config: config, OriginalID: hostFileIdentity(snapshot.info), OriginalDigest: definitionDigest(snapshot.data), Stage: uninstallClaimPending}
	if !absentReloadFile(uninstallClaimPath(j)) {
		return uninstallFailure()
	}
	if writeUninstallIntent(u.unit, j) != nil || u.sync() != nil {
		return uninstallFailure()
	}
	slog.Info("service_uninstall_stage", "stage", j.Stage)
	return u.resume(j)
}
func (u *serviceUninstaller) resume(j *serviceUninstallJournal) error {
	claim := uninstallClaimPath(j)
	if j.Stage == uninstallClaimPending {
		moved := false
		if absentReloadFile(claim) {
			if !matchesUninstallFile(u.unit, j.OriginalID, j.OriginalDigest) {
				return uninstallFailure()
			}
			if err := u.checkpoint("before_claim"); err != nil {
				return err
			}
			if err := u.move(u.unit, claim); err != nil {
				return uninstallFailure()
			}
			moved = true
			if err := u.checkpoint("after_claim"); err != nil {
				return err
			}
		}
		if u.sync() != nil {
			return uninstallFailure()
		}
		body, info, err := readPrivateServiceDefinition(claim)
		if err != nil {
			return uninstallFailure()
		}
		id, digest := hostFileIdentity(info), definitionDigest(body)
		// After a crash, an unexpected pre-existing claim has no proven origin.
		// Retain it without restoring or deleting it. Only an observed move may
		// grant restoration authority for mismatching external bytes.
		if !moved && (id != j.OriginalID || digest != j.OriginalDigest) {
			return uninstallFailure()
		}
		file, err := openPrivate(claim, os.O_RDONLY)
		if err != nil {
			return uninstallFailure()
		}
		err = file.Sync()
		closeErr := file.Close()
		if err != nil || closeErr != nil || !matchesUninstallFile(claim, id, digest) {
			return uninstallFailure()
		}
		next := *j
		next.ClaimID, next.ClaimDigest, next.Stage = id, digest, uninstallClaimed
		if err := u.save(j, next); err != nil {
			return err
		}
	}
	if j.Stage == uninstallClaimed {
		if !matchesUninstallFile(claim, j.ClaimID, j.ClaimDigest) {
			return uninstallFailure()
		}
		if !uninstallOriginalClaim(j) {
			if err := u.stage(j, uninstallRestorePending); err != nil {
				return err
			}
		} else {
			if err := u.stage(j, uninstallDeletePending); err != nil {
				return err
			}
		}
	}
	if j.Stage == uninstallRestorePending {
		if err := u.checkpoint("before_restore"); err != nil {
			return err
		}
		if !absentReloadFile(claim) {
			if !matchesUninstallFile(claim, j.ClaimID, j.ClaimDigest) {
				return uninstallFailure()
			}
			if u.move(claim, u.unit) != nil {
				return uninstallFailure()
			}
			if err := u.checkpoint("after_restore"); err != nil {
				return err
			}
		}
		if u.sync() != nil || !matchesUninstallFile(u.unit, j.ClaimID, j.ClaimDigest) {
			return uninstallFailure()
		}
		if err := u.stage(j, uninstallRestored); err != nil {
			return err
		}
		return uninstallFailure()
	}
	if j.Stage == uninstallRestored {
		// A later explicit retry may retire only a proven completed restoration.
		// It never adopts that replacement as this uninstall's deletion target.
		if !absentReloadFile(claim) || !matchesUninstallFile(u.unit, j.ClaimID, j.ClaimDigest) || u.sync() != nil {
			return uninstallFailure()
		}
		if err := u.retire(j); err != nil {
			return err
		}
		return uninstallFailure()
	}
	if j.Stage == uninstallDeletePending {
		if err := u.checkpoint("before_delete"); err != nil {
			return err
		}
		if !absentReloadFile(claim) {
			if !matchesUninstallFile(claim, j.OriginalID, j.OriginalDigest) {
				return uninstallFailure()
			}
			// The canonical path is never an unlink target. It may already hold
			// a new writer, which remains outside this operation's authority.
			if os.Remove(claim) != nil {
				return uninstallFailure()
			}
			if err := u.checkpoint("after_delete"); err != nil {
				return err
			}
		}
		if u.sync() != nil {
			return uninstallFailure()
		}
		if err := u.stage(j, uninstallDeleted); err != nil {
			return err
		}
	}
	if j.Stage != uninstallDeleted || !absentReloadFile(claim) || u.sync() != nil {
		return uninstallFailure()
	}
	if err := u.retire(j); err != nil {
		return err
	}
	if !absentReloadFile(u.unit) {
		return uninstallFailure()
	}
	return nil
}

func (u *serviceUninstaller) retire(j *serviceUninstallJournal) error {
	if err := u.checkpoint("before_retire"); err != nil {
		return err
	}
	// Retire only this exact private receipt. Never let an old deletion receipt
	// authorize a claim of a newer canonical definition.
	current, err := readUninstallJournal(u.unit)
	if err != nil || current == nil || *current != *j {
		return uninstallFailure()
	}
	if os.Remove(uninstallJournalPath(u.unit)) != nil {
		return uninstallFailure()
	}
	if u.sync() != nil {
		// Keep retry authority visible on a reported sync failure. Recreate only
		// into a vacant receipt path; preserve any external replacement there.
		if writeUninstallIntent(u.unit, j) == nil {
			_ = u.sync()
		}
		return uninstallFailure()
	}
	return nil
}

// Publish a complete synced intent atomically into a vacant receipt path.
// A crash during the temporary write leaves the original definition untouched.
func writeUninstallIntent(unit string, j *serviceUninstallJournal) error {
	body, _ := json.Marshal(j)
	file, err := os.CreateTemp(filepath.Dir(unit), ".runmoor-uninstall-intent-*")
	if err != nil {
		return uninstallFailure()
	}
	defer os.Remove(file.Name())
	_, err = file.Write(body)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return uninstallFailure()
	}
	if renameServiceDefinitionNoReplace(file.Name(), uninstallJournalPath(unit)) != nil {
		return uninstallFailure()
	}
	return nil
}

func requireUninstallConfig(j *serviceUninstallJournal, platform, requested string) error {
	wanted, err := filepath.Abs(requested)
	if err != nil || wanted != j.Config || platform != j.Platform {
		return uninstallFailure()
	}
	if _, err := cleanAbsoluteServicePath(wanted); err != nil {
		return uninstallFailure()
	}
	if hasParentPathComponent(requested) {
		original, err := filepath.EvalSymlinks(requested)
		if err == nil {
			original, err = filepath.Abs(original)
		}
		cleaned, cleanErr := filepath.EvalSymlinks(wanted)
		if err != nil || cleanErr != nil || original != cleaned {
			return uninstallFailure()
		}
	}
	return nil
}
