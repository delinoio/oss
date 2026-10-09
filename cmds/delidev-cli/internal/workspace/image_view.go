// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
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
	normalize := func(value string) string {
		if workerOS == "windows" {
			return strings.ReplaceAll(value, "\\", "/")
		}
		return value
	}
	location = normalize(location)
	if domain.Text(location, "native image location", 4096, true) != nil || path.Clean(location) != location || strings.Contains(location, "://") {
		return domain.ImageViewObservation{}, ResultUncertain()
	}
	roots := manifest.Repositories
	if len(roots) == 0 {
		roots = []PreparedRepository{{Path: manifest.PrimaryPath}}
	}
	selected := PreparedRepository{}
	relative := ""
	for _, root := range roots {
		base := normalize(root.Path)
		if strings.HasPrefix(location, base+"/") && len(base) > len(normalize(selected.Path)) {
			selected, relative = root, strings.TrimPrefix(location, base+"/")
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
	normalized := root
	if workerOS == "windows" {
		normalized = strings.ReplaceAll(root, "\\", "/")
	}
	expected, err := ObserveImageViewLocation(input, workerOS, normalized+"/"+observation.Location, observation.ReferenceID)
	if err != nil || expected != observation {
		return ResultUncertain()
	}
	return nil
}
