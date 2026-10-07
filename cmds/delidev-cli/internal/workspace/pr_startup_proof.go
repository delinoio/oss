package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func IsPRStartupRejection(err error) bool {
	var rejected *prStartupRejection
	return errors.As(err, &rejected)
}

func MatchesPRStartupRejection(err error, proof domain.PRStartupRejectionProof) bool {
	var rejected *prStartupRejection
	if !errors.As(err, &rejected) || rejected.record.validate() != nil || rejected.record.Phase != prStartupRejected || proof.Validate() != nil {
		return false
	}
	raw, marshalErr := json.Marshal(rejected.record)
	projected, projectionErr := json.Marshal(prStartupRecordFromProof(proof))
	return marshalErr == nil && projectionErr == nil && bytes.Equal(raw, projected) && startupProofDigest(raw) == proof.JournalDigest
}

func prStartupRecordFromProof(proof domain.PRStartupRejectionProof) prStartupRecord {
	return prStartupRecord{Version: 1, JobID: proof.JobID, ExecutionID: proof.ExecutionID, SessionID: proof.SessionID, PreparationDigest: proof.PreparationDigest, ManifestDigest: proof.ManifestDigest, TargetDigest: proof.TargetDigest, Phase: prStartupRejected, Reason: proof.Reason, StartedAt: proof.StartedAt, FinishedAt: &proof.FinishedAt}
}

func startupProofDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// ValidatePRStartupRejection is a pure comparison for server publication. It
// cannot inspect Worker paths, invoke Git or infer a proof from absent events.
func ValidatePRStartupRejection(input PrepareRequest, manifest Manifest, proof domain.PRStartupRejectionProof, workerOS string) error {
	if input.validateStructure() != nil || ValidateResult(input, manifest, workerOS) != nil || proof.Validate() != nil ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(proof.SessionID), proof.SessionID != input.SessionID) ||
		proof.PreparationDigest != preparationDigest(input) {
		return domain.StartupRejectionUncertain()
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil || startupProofDigest(rawManifest) != proof.ManifestDigest {
		return domain.StartupRejectionUncertain()
	}
	var target *domain.PRGitTarget
	for _, spec := range input.Repositories {
		if spec.PRTarget != nil {
			if target != nil {
				return domain.StartupRejectionUncertain()
			}
			target = spec.PRTarget
		}
	}
	rawTarget, err := json.Marshal(target)
	if target == nil || err != nil || startupProofDigest(rawTarget) != proof.TargetDigest {
		return domain.StartupRejectionUncertain()
	}
	record := prStartupRecordFromProof(proof)
	rawRecord, err := json.Marshal(record)
	if err != nil || record.validate() != nil || startupProofDigest(rawRecord) != proof.JournalDigest {
		return domain.StartupRejectionUncertain()
	}
	return nil
}

// ReadPRStartupRejection holds the original session lock and reads only its
// unchanged rejected phase/manifest. It never runs Git, cleans processes,
// rewrites a phase, or creates execution/response/continuation authority.
func (m *Manager) ReadPRStartupRejection(ctx context.Context, job, execution domain.ID, input PrepareRequest, expected Manifest, workerOS string) (domain.PRStartupRejectionProof, error) {
	var empty domain.PRStartupRejectionProof
	if ctx.Err() != nil || domain.UniqueIDs([]domain.ID{job, execution, input.SessionID}) != nil || input.validate() != nil || m.initialize() != nil {
		return empty, domain.StartupRejectionUncertain()
	}
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(input.SessionID)+".lock"))
	if err != nil {
		return empty, domain.StartupRejectionUncertain()
	}
	defer lock.Close()
	raw, err := security.ReadPrivate(m.prStartupPath(input.SessionID, execution), 4096)
	var record prStartupRecord
	if err != nil || domain.Decode(raw, &record) != nil || record.validate() != nil || record.Phase != prStartupRejected || record.JobID != job || record.ExecutionID != execution ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(record.SessionID), record.SessionID != input.SessionID) ||
		m.requireNoPRNativeEligibility(record) != nil || m.requireNoExecutionHistory(input.SessionID) != nil {
		return empty, domain.StartupRejectionUncertain()
	}
	proof := domain.PRStartupRejectionProof{JobID: job, ExecutionID: execution, SessionID: input.SessionID, PreparationDigest: record.PreparationDigest, ManifestDigest: record.ManifestDigest, TargetDigest: record.TargetDigest, JournalDigest: startupProofDigest(raw), Reason: record.Reason, StartedAt: record.StartedAt, FinishedAt: *record.FinishedAt}
	if ValidatePRStartupRejection(input, expected, proof, workerOS) != nil {
		return empty, domain.StartupRejectionUncertain()
	}
	manifest, err := m.Read(input.SessionID)
	if err != nil {
		return empty, domain.StartupRejectionUncertain()
	}
	actual, err := json.Marshal(manifest)
	accepted, acceptedErr := json.Marshal(expected)
	if err != nil || acceptedErr != nil || !bytes.Equal(actual, accepted) || ctx.Err() != nil {
		return empty, domain.StartupRejectionUncertain()
	}
	return proof, nil
}
