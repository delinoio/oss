// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func imageViewManifest(input domain.ExecutionJobInput, workerOS string) (Manifest, error) {
	var preparation PrepareRequest
	var manifest Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || ValidateResult(preparation, manifest, workerOS) != nil {
		return manifest, ResultUncertain()
	}
	return manifest, nil
}

// ObserveImageViewLocation performs no filesystem operation. It scopes a native
// observation to the original prepared roots, without claiming that the path
// still identifies a file, that symlink targets are inside a root, or that any
// bytes are the ones the native tool read. URLs and unscoped paths are refused.
func ObserveImageViewLocation(input domain.ExecutionJobInput, workerOS, location string, reference domain.ID) (domain.ImageViewObservation, error) {
	manifest, err := imageViewManifest(input, workerOS)
	if err != nil {
		return domain.ImageViewObservation{}, err
	}
	location = normalizeWorkerPath(location, workerOS)
	if !validWorkerAbsolutePath(location, workerOS) || strings.Contains(location, "://") {
		return domain.ImageViewObservation{}, ResultUncertain()
	}
	roots := manifest.Repositories
	if len(roots) == 0 {
		roots = []PreparedRepository{{Path: manifest.PrimaryPath}}
	}
	selected := PreparedRepository{}
	relative := ""
	selectedLength := 0
	for _, root := range roots {
		rootPrefix := workerRootPrefix(root.Path, workerOS)
		// The longest matching root wins; equal-length roots keep the first match.
		if scoped, ok := scopeToRootPrefix(location, rootPrefix, workerOS); ok && len(rootPrefix) > selectedLength {
			selected, relative, selectedLength = root, scoped, len(rootPrefix)
		}
	}
	digest := sha256.Sum256(input.Manifest)
	observation := domain.ImageViewObservation{ReferenceID: reference, MachineID: input.MachineID, RepositoryID: selected.ID, ManifestDigest: hex.EncodeToString(digest[:]), Location: relative}
	if observation.Validate() != nil {
		return domain.ImageViewObservation{}, ResultUncertain()
	}
	return observation, nil
}

// Server validation binds metadata to the immutable accepted manifest. Neither
// successful validation nor this reference permits opening or deleting a file.
func ValidateImageViewReference(input domain.ExecutionJobInput, workerOS string, observation domain.ImageViewObservation) error {
	manifest, err := imageViewManifest(input, workerOS)
	if err != nil || observation.Validate() != nil || observation.MachineID != input.MachineID {
		return ResultUncertain()
	}
	root := ""
	if observation.RepositoryID == "" && len(manifest.Repositories) == 0 {
		root = manifest.PrimaryPath
	}
	for _, candidate := range manifest.Repositories {
		if candidate.ID == observation.RepositoryID {
			root = candidate.Path
		}
	}
	if root == "" {
		return ResultUncertain()
	}
	expected, err := ObserveImageViewLocation(input, workerOS, workerRootPrefix(root, workerOS)+observation.Location, observation.ReferenceID)
	if err != nil || expected != observation {
		return ResultUncertain()
	}
	return nil
}

// workerRootPrefix returns the normalized root followed by exactly one
// separator. The filesystem root "/" already ends with its separator, so it must
// not receive a second one.
func workerRootPrefix(root, workerOS string) string {
	return strings.TrimSuffix(normalizeWorkerPath(root, workerOS), "/") + "/"
}

// scopeToRootPrefix returns the part of a normalized absolute location below the
// prepared root prefix. The prefix ends at a component boundary, so siblings
// such as "/repo-other" never match "/repo". Windows paths compare ASCII letters
// case-insensitively, while Linux and macOS compare bytes exactly. The returned
// relative spelling always comes from the observed location. An empty relative
// part (the root itself) is not a match.
func scopeToRootPrefix(location, rootPrefix, workerOS string) (string, bool) {
	if len(location) <= len(rootPrefix) {
		return "", false
	}
	head := location[:len(rootPrefix)]
	if workerOS == "windows" {
		if !equalASCIIFold(head, rootPrefix) {
			return "", false
		}
	} else if head != rootPrefix {
		return "", false
	}
	return location[len(rootPrefix):], true
}

// equalASCIIFold folds only ASCII letters, so equal-length byte slicing stays
// valid for the observed spelling. Non-ASCII bytes must match exactly.
func equalASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
