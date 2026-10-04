// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Recovery retains two copies of individually bounded original workspace
// evidence and at most eight claim references. Keep this finite exception until
// immutable preparation/manifest evidence can be stored by reference.
const MaxStorageRecoveryClaims = 8

const MaxStorageRecoveryJobBytes = 4 << 20

func DecodeStorageRequest(raw []byte, target *StorageRequest) error {
	if err := domain.DecodeWithLimit(raw, target, domain.MaxStorageRecoveryInputBytes); err != nil {
		return err
	}
	if len(raw) <= 1<<20 {
		return nil
	}
	if target.Action != StorageRecover || target.Version != 1 || target.Recovery == nil || target.Recovery.Original.Action == StorageRecover {
		return ResultUncertain()
	}
	original, err := json.Marshal(target.Recovery.Original)
	if err != nil || len(original) > 1<<20 || len(target.Recovery.Claims) < 1 || len(target.Recovery.Claims) > MaxStorageRecoveryClaims {
		return ResultUncertain()
	}
	return nil
}

// Only typed recovery jobs may exceed the ordinary entity bound. Full contextual
// request/native ownership validation remains mandatory at admission and use.
func DecodeStorageJob(raw []byte, target *domain.Job) error {
	if err := domain.DecodeWithLimit(raw, target, MaxStorageRecoveryJobBytes); err != nil {
		return err
	}
	if len(raw) <= 1<<20 {
		return nil
	}
	var input StorageRequest
	if target.Type != domain.WorkspaceStorageJob || target.Validate() != nil || DecodeStorageRequest(target.Input, &input) != nil || input.Action != StorageRecover || input.Recovery == nil {
		return ResultUncertain()
	}
	return nil
}

func StorageJobDocumentLimit(job domain.Job) int {
	var input StorageRequest
	if job.Type == domain.WorkspaceStorageJob && DecodeStorageRequest(job.Input, &input) == nil && input.Action == StorageRecover && input.Recovery != nil {
		return MaxStorageRecoveryJobBytes
	}
	return 1 << 20
}
