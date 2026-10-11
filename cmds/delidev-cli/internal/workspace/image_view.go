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
	if domain.Text(location, "native image location", 4096, true) != nil || !canonicalWorkerAbsolutePath(location, workerOS) || strings.Contains(location, "://") {
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
	if err != nil || observation.Validate() != nil || observation.MachineID != input.MachineID || (workerOS == "windows" && strings.ContainsAny(observation.Location, "\\:")) {
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

// canonicalWorkerAbsolutePath checks the original Worker grammar without using
// the server host's filesystem rules. Keep the UNC prefix for root comparison;
// remove one slash only while checking POSIX lexical canonicality.
func canonicalWorkerAbsolutePath(value, workerOS string) bool {
	if domain.Text(value, "workspace path", 4096, true) != nil {
		return false
	}
	if workerOS == "windows" {
		value = strings.ReplaceAll(value, "\\", "/")
		if strings.HasPrefix(value, "//") {
			parts := strings.Split(value[2:], "/")
			if len(parts) < 3 || parts[0] == "" || parts[0] == "." || parts[0] == ".." || parts[0] == "?" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.ContainsAny(value, ":?") {
				return false
			}
			value = value[1:]
		} else {
			if len(value) < 4 || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) || value[1:3] != ":/" {
				return false
			}
			value = value[2:]
			if strings.Contains(value, ":") {
				return false
			}
		}
	} else if workerOS != "darwin" && workerOS != "linux" {
		return false
	}
	return path.IsAbs(value) && path.Clean(value) == value
}
