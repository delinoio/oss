// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestLegacyNativeSourcePreservesAssignmentWithoutStartupCapability(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.ClaudeCode, domain.OpenCode} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			var f *firstDispatchFixture
			var original domain.ExecutionJobInput
			if harness == domain.ClaudeCode {
				var p *publicationFixture
				f, p = publicCompactionFixture(t)
				original = p.input
			} else {
				c := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, harness)
				f, original = c.firstDispatchFixture, c.input
			}
			// Emulate an immutable completed v1 source from an older server,
			// rather than rewriting an active job or granting native authority.
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.legacy-source", nil, func(tx *store.Tx) (any, error) {
				mr, machine, err := activeMachine(tx, original.MachineID)
				if err != nil {
					return nil, err
				}
				for _, installation := range machine.Installations {
					if installation.Harness == harness {
						original.Installation = installation
					}
				}
				original.Version, original.Startup = 1, nil
				if err := original.Validate(); err != nil {
					return nil, err
				}
				sr, session, err := sessionRecord(tx, original.SessionID)
				if err != nil {
					return nil, err
				}
				jr, err := tx.SessionExecutionJob(sr.ID, original.ExecutionID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](jr)
				if err != nil {
					return nil, err
				}
				job.Input, _ = json.Marshal(original)
				job.Startup, session.Startup = nil, nil
				if _, err := tx.PutJob(jr.ID, jr.Revision, sr.ID, sr.ProjectID, job); err != nil {
					return nil, err
				}
				machine.WorkerCapabilities = slices.DeleteFunc(machine.WorkerCapabilities, func(c domain.WorkerCapability) bool { return c == domain.ExecutionStartupV1 })
				if harness != domain.ClaudeCode {
					native := domain.CodexSessionCompactionV1
					if harness == domain.OpenCode {
						native = domain.OpenCodeSessionCompactionV1
					}
					machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NativeSessionCompactionV1, native)
				}
				if _, err := tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); err != nil {
					return nil, err
				}
				return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			retained, _ := json.Marshal(original)
			err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
				sr, session, err := sessionRecord(tx, original.SessionID)
				if err != nil {
					return err
				}
				compact, err := compactionSource(tx, sr, session, domain.NewID())
				if err != nil {
					return err
				}
				raw, _ := json.Marshal(compact.Assignment)
				if !bytes.Equal(raw, retained) {
					t.Fatal("compaction rewrote the legacy source")
				}
				if harness == domain.Codex {
					_, _, fork, err := forkBoundary(tx, sr.ID, domain.NativeIdentity(session.Execution.NativeTurnID))
					if err != nil {
						return err
					}
					raw, _ = json.Marshal(fork.SourceAssignment)
					if !bytes.Equal(raw, retained) {
						t.Fatal("fork rewrote the legacy source")
					}
				}
				_, machine, err := activeMachine(tx, original.MachineID)
				if err != nil {
					return err
				}
				if _, err := checkedExecutionAssignment(tx, sr, session, machine, original); domain.SafeError(err).Code != domain.Unsupported {
					t.Fatal("legacy source support admitted a new execution without Worker 23", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
