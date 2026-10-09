// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"path"
	"strings"
)

// ImageViewObservation owns only an immutable native observation. Its location
// describes the original prepared root; it grants no live file, byte retrieval,
// input attachment, or source-file deletion authority.
type ImageViewObservation struct {
	ReferenceID    ID     `json:"reference_id"`
	MachineID      ID     `json:"machine_id"`
	RepositoryID   ID     `json:"repository_id,omitempty"`
	ManifestDigest string `json:"manifest_digest"`
	Location       string `json:"location"`
}

func (v ImageViewObservation) Validate() error {
	digest, err := hex.DecodeString(v.ManifestDigest)
	if v.ReferenceID.Validate() != nil || v.MachineID.Validate() != nil || (v.RepositoryID != "" && v.RepositoryID.Validate() != nil) || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != v.ManifestDigest || Text(v.Location, "native image location", 4096, true) != nil || path.IsAbs(v.Location) || path.Clean(v.Location) != v.Location || v.Location == "." || v.Location == ".." || strings.HasPrefix(v.Location, "../") || strings.ContainsAny(v.Location, "\\:") {
		return invalidTool()
	}
	return nil
}
