package runmoor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type servicePublicationStage string

const (
	publicationPrepared       servicePublicationStage = "prepared"
	publicationClaimPending   servicePublicationStage = "claim_pending"
	publicationClaimed        servicePublicationStage = "claimed"
	publicationPublishPending servicePublicationStage = "publish_pending"
	publicationPublished      servicePublicationStage = "published"
	publicationRestorePending servicePublicationStage = "restore_pending"
	publicationRestored       servicePublicationStage = "restored"
)

func reloadClaimPath(j *serviceReloadJournal) string {
	return reloadTargetPath(j) + ".claim"
}

func validServicePublication(j *serviceReloadJournal) bool {
	switch j.Publication {
	case "", publicationPrepared, publicationClaimPending:
		return j.ClaimFileID == "" && j.ClaimSHA256 == ""
	case publicationClaimed, publicationPublishPending, publicationPublished, publicationRestorePending, publicationRestored:
		digest, err := hex.DecodeString(j.ClaimSHA256)
		return j.ClaimFileID != "" && err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == j.ClaimSHA256
	default:
		return false
	}
}

func publicationFailure() error {
	return problem(ErrRetry, "Service definition publication needs reconciliation.", "Preserve the service definition and private reload files. Inspect the user service, then retry reload with the same installed CLI and configuration after resolving conflicting definitions.")
}

func (r *serviceReloader) saveJournal(j, next *serviceReloadJournal) error {
	old, _ := json.Marshal(j)
	body, _ := json.Marshal(next)
	if err := replaceReloadFile(reloadJournalPath(r.Unit), old, reloadJournalLimit, body); err != nil {
		return err
	}
	*j = *next
	return nil
}

func (r *serviceReloader) publication(j *serviceReloadJournal, next servicePublicationStage) error {
	copy := *j
	copy.Publication = next
	if err := r.saveJournal(j, &copy); err != nil {
		return err
	}
	r.Log.Info("service_reload_publication", "stage", next)
	return nil
}

func (r *serviceReloader) renameDefinition(from, to string) error {
	if r.RenameDefinition != nil {
		return r.RenameDefinition(from, to)
	}
	return renameServiceDefinitionNoReplace(from, to)
}

func (r *serviceReloader) syncDefinitionDir() error {
	if r.SyncDefinitionDir != nil {
		return r.SyncDefinitionDir(filepath.Dir(r.Unit))
	}
	return syncPrivateDir(filepath.Dir(r.Unit))
}

func matchesReloadFile(path, id string, expected []byte) bool {
	body, info, err := readPrivateServiceDefinitionWithLimit(path, serviceDefinitionLimit)
	return err == nil && hostFileIdentity(info) == id && bytes.Equal(body, expected)
}

func absentReloadFile(path string) bool {
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func originalClaim(j *serviceReloadJournal) bool {
	return j.ClaimFileID == j.OriginalFileID && j.ClaimSHA256 == definitionDigest(j.Original)
}

func definitionDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func matchesClaim(path string, j *serviceReloadJournal) bool {
	body, info, err := readPrivateServiceDefinitionWithLimit(path, serviceDefinitionLimit)
	return err == nil && hostFileIdentity(info) == j.ClaimFileID && definitionDigest(body) == j.ClaimSHA256
}

// A claim is a private, token-derived name, never a replacement destination.
// Journal each rename's intent first, then sync its directory before recording
// the observed outcome. No pathname check grants permission to replace a file.
func (r *serviceReloader) publish(path string, j *serviceReloadJournal) error {
	claim, staged := reloadClaimPath(j), reloadTargetPath(j)
	if j.Publication == "" {
		// Older journals may have completed an exchange. Only a missing staged
		// file or the exact displaced original is safe; unknown bytes stay put.
		target, err := r.definition(path, j)
		if err != nil {
			return err
		}
		if target {
			if !absentReloadFile(staged) && !matchesReloadFile(staged, j.OriginalFileID, j.Original) {
				return publicationFailure()
			}
			return r.syncDefinitionDir()
		}
		if err := r.publication(j, publicationPrepared); err != nil {
			return err
		}
	}
	if j.Publication == publicationRestored {
		if err := r.rebaseRestoredPublication(path, j); err != nil {
			return err
		}
	}
	if j.Publication == publicationRestorePending {
		return r.restoreClaim(j)
	}
	if j.Publication == publicationPrepared {
		if !matchesReloadFile(r.Unit, j.OriginalFileID, j.Original) || !matchesReloadFile(staged, j.TargetFileID, j.Target) || !absentReloadFile(claim) {
			return publicationFailure()
		}
		if _, err := r.definition(path, j); err != nil {
			return err
		}
		if err := r.publication(j, publicationClaimPending); err != nil {
			return err
		}
		if r.BeforePublish != nil {
			r.BeforePublish()
		}
	}
	if j.Publication == publicationClaimPending {
		if absentReloadFile(claim) {
			if err := r.renameDefinition(r.Unit, claim); err != nil {
				return publicationFailure()
			}
		}
		if err := r.syncDefinitionDir(); err != nil {
			return publicationFailure()
		}
		body, info, err := readPrivateServiceDefinitionWithLimit(claim, serviceDefinitionLimit)
		if err != nil {
			return publicationFailure()
		}
		file, err := openPrivate(claim, os.O_RDONLY)
		if err != nil {
			return publicationFailure()
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil || closeErr != nil || !matchesReloadFile(claim, hostFileIdentity(info), body) {
			return publicationFailure()
		}
		copy := *j
		copy.ClaimSHA256, copy.ClaimFileID, copy.Publication = definitionDigest(body), hostFileIdentity(info), publicationClaimed
		if err := r.saveJournal(j, &copy); err != nil {
			return err
		}
	}
	if j.Publication == publicationClaimed {
		if !matchesClaim(claim, j) {
			return publicationFailure()
		}
		if !originalClaim(j) || !matchesReloadFile(staged, j.TargetFileID, j.Target) {
			return r.abortPublication(j)
		}
		if err := r.publication(j, publicationPublishPending); err != nil {
			return err
		}
	}
	if j.Publication == publicationPublishPending {
		if !originalClaim(j) || !matchesClaim(claim, j) {
			return publicationFailure()
		}
		if !matchesReloadFile(r.Unit, j.TargetFileID, j.Target) {
			if !matchesReloadFile(staged, j.TargetFileID, j.Target) {
				return r.abortPublication(j)
			}
			if err := r.renameDefinition(staged, r.Unit); err != nil {
				if matchesReloadFile(r.Unit, j.TargetFileID, j.Target) {
					// The rename may have completed before an interrupted caller
					// observed its result. Keep publish intent for recovery.
					return publicationFailure()
				}
				return r.abortPublication(j)
			}
		}
		if err := r.syncDefinitionDir(); err != nil {
			return publicationFailure()
		}
		target, err := r.definition(path, j)
		if err != nil || !target {
			return publicationFailure()
		}
		if err := r.publication(j, publicationPublished); err != nil {
			return err
		}
	}
	if j.Publication != publicationPublished || !originalClaim(j) || !matchesClaim(claim, j) {
		return publicationFailure()
	}
	target, err := r.definition(path, j)
	if err != nil || !target {
		return publicationFailure()
	}
	return nil
}

// A restored claim may have belonged to an external writer. Permit a retry
// only after the canonical file has been repaired to the journaled original
// bytes. The recreated file receives a new identity, so record that identity
// before starting another no-replace claim. Do not copy the external bytes
// from the restored claim into the journal or accept an already-published
// target as a new baseline.
func (r *serviceReloader) rebaseRestoredPublication(path string, j *serviceReloadJournal) error {
	claim := reloadClaimPath(j)
	if !absentReloadFile(claim) {
		return publicationFailure()
	}
	snapshot, err := validateServiceConfigMatch(r.Platform, r.Unit, path)
	if err != nil || !bytes.Equal(snapshot.data, j.Original) || bytes.Equal(snapshot.data, j.Target) {
		return publicationFailure()
	}
	copy := *j
	copy.OriginalFileID = hostFileIdentity(snapshot.info)
	copy.Publication, copy.ClaimFileID, copy.ClaimSHA256 = publicationPrepared, "", ""
	return r.saveJournal(j, &copy)
}

func (r *serviceReloader) abortPublication(j *serviceReloadJournal) error {
	if err := r.publication(j, publicationRestorePending); err != nil {
		return err
	}
	return r.restoreClaim(j)
}

func (r *serviceReloader) restoreClaim(j *serviceReloadJournal) error {
	claim := reloadClaimPath(j)
	if absentReloadFile(claim) {
		// A crash after restoration is distinguishable only by the exact moved
		// identity and bytes. Missing or different files retain recovery intent.
		if !matchesClaim(r.Unit, j) {
			return publicationFailure()
		}
	} else {
		if !matchesClaim(claim, j) {
			return publicationFailure()
		}
		if err := r.renameDefinition(claim, r.Unit); err != nil {
			return publicationFailure()
		}
	}
	if err := r.syncDefinitionDir(); err != nil {
		return publicationFailure()
	}
	if !matchesClaim(r.Unit, j) {
		return publicationFailure()
	}
	if err := r.publication(j, publicationRestored); err != nil {
		return err
	}
	return publicationFailure()
}

// Keep the displaced original after completion as private retained bytes. A
// pathname check cannot authorize unlinking against an external writer either.
// Retained claims without a journal grant no future publication authority.
func (r *serviceReloader) retirePublication(j *serviceReloadJournal) error {
	if j.Publication == "" {
		return nil
	}
	if j.Publication != publicationPublished || !originalClaim(j) {
		return publicationFailure()
	}
	claim := reloadClaimPath(j)
	if !matchesReloadFile(claim, j.OriginalFileID, j.Original) {
		return publicationFailure()
	}
	return r.syncDefinitionDir()
}
