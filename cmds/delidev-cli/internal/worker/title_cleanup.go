// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type titleCleanupStage string

const (
	titleCloseNative titleCleanupStage = "native_close"
	titleCloseProxy  titleCleanupStage = "proxy_close"
	titleReconcile   titleCleanupStage = "process_reconcile"
	titleRemoveHome  titleCleanupStage = "runtime_remove"
	titleSyncParent  titleCleanupStage = "parent_sync"
)

// One owner finalizes both successful and failed inference. Cache uncertainty
// so the deferred return cannot retry removal or erase an earlier failed join.
type titleFinalizer struct {
	home        string
	jobID       domain.ID
	logger      *slog.Logger
	closeNative func() error
	closeProxy  func() error
	reconcile   func() error
	remove      func(string) error
	syncParent  func(string) error
	finished    bool
	err         error
}

func newTitleFinalizer(home string, jobID domain.ID, logger *slog.Logger) *titleFinalizer {
	return &titleFinalizer{home: home, jobID: jobID, logger: logger, remove: os.RemoveAll, syncParent: security.SyncParent}
}

func (f *titleFinalizer) fail(stage titleCleanupStage) {
	message := "The private automatic title runtime cleanup could not be confirmed."
	switch stage {
	case titleCloseNative:
		message = "The private Codex title runtime could not be closed cleanly."
	case titleCloseProxy:
		message = "The private automatic title proxy could not be closed cleanly."
	case titleReconcile:
		message = "The original automatic title processes could not be reconciled."
	case titleRemoveHome:
		message = "The private automatic title runtime could not be removed."
	case titleSyncParent:
		message = "The automatic title runtime removal could not be synchronized."
	}
	if f.err == nil {
		f.err = domain.Fail(domain.RecoveryRequired, message, "Retain the original title ownership and reconcile cleanup before continuing.")
	}
	if f.logger != nil {
		// Native errors may contain private paths, prompts or credentials.
		f.logger.Warn("automatic title cleanup unconfirmed", "job_id", f.jobID, "stage", stage, "code", domain.RecoveryRequired)
	}
}

func (f *titleFinalizer) run() error {
	if f.finished {
		return f.err
	}
	f.finished = true
	for _, step := range []struct {
		stage titleCleanupStage
		join  func() error
	}{
		{titleCloseNative, f.closeNative},
		{titleCloseProxy, f.closeProxy},
		{titleReconcile, f.reconcile},
	} {
		if step.join != nil && step.join() != nil {
			f.fail(step.stage)
		}
	}
	// Every owner gets its own join attempt. None grants permission to erase
	// the private runtime while any other owner's cleanup remains uncertain.
	if f.err != nil {
		return f.err
	}
	if err := f.remove(f.home); err != nil {
		f.fail(titleRemoveHome)
		return f.err
	}
	if err := f.syncParent(f.home); err != nil {
		f.fail(titleSyncParent)
	}
	return f.err
}

func (f *titleFinalizer) finish(output *json.RawMessage, returned *error) {
	if err := f.run(); err != nil {
		*output, *returned = nil, err
	}
}
