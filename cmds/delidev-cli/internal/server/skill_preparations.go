// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type skillPreparationState string

const (
	skillPreparationPending  skillPreparationState = "pending"
	skillPreparationAccepted skillPreparationState = "accepted"
	skillPreparationCleaning skillPreparationState = "cleaning"
	skillPreparationRemoved  skillPreparationState = "removed"
)
const skillPreparationJournalLimit = 4096
const skillPreparationJournalBytes = 2 << 20

type skillPreparationJournal struct {
	Version        uint32                  `json:"version"`
	RequestID      domain.ID               `json:"request_id"`
	Operation      string                  `json:"operation"`
	Identity       json.RawMessage         `json:"identity"`
	IdentityDigest string                  `json:"identity_digest"`
	Actor          domain.Principal        `json:"actor"`
	Scope          domain.SkillReadRequest `json:"scope"`
	State          skillPreparationState   `json:"state"`
}
type skillPreparationOwner struct {
	RequestID domain.ID
	Operation string
	Identity  json.RawMessage
	Actor     domain.Principal
}
type skillPreparationContextKey struct{}

// Service test instances can share the same live store. This process-local gate
// joins their original handlers too; the Store process lock excludes old servers
// after a real restart. A missing in-memory claim alone never replaces a receipt.
var liveSkillPreparations = struct {
	sync.Mutex
	owners map[string]map[domain.ID]bool
}{owners: map[string]map[domain.ID]bool{}}

func skillOperation(operation string) bool {
	return operation == "session.create" || operation == "session.enqueue" || operation == "session.input.change"
}
func preparationIdentityDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (s *Service) skillPreparationPath(id domain.ID) string {
	return filepath.Join(s.Store.Root(), "skill-preparations", string(id)+".json")
}
func (s *Service) terminalSkillPreparationPath(id domain.ID) string {
	return filepath.Join(s.Store.Root(), "skill-preparation-receipts", preparationIdentityDigest([]byte(id))[:2], string(id)+".json")
}
func (s *Service) retainedSkillPreparation(id domain.ID) (skillPreparationJournal, error) {
	v, e := readSkillPreparation(s.terminalSkillPreparationPath(id))
	if !errors.Is(e, os.ErrNotExist) {
		return v, e
	}
	return readSkillPreparation(s.skillPreparationPath(id))
}
func (s *Service) retireSkillPreparation(v skillPreparationJournal) error {
	path := s.terminalSkillPreparationPath(v.RequestID)
	if e := security.PrivateDir(filepath.Dir(path)); e != nil {
		return e
	}
	if e := writeSkillPreparation(path, v); e != nil {
		return e
	}
	if e := os.Remove(s.skillPreparationPath(v.RequestID)); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return security.SyncParent(s.skillPreparationPath(v.RequestID))
}
func readSkillPreparation(path string) (skillPreparationJournal, error) {
	var v skillPreparationJournal
	raw, err := security.ReadPrivate(path, skillPreparationJournalBytes)
	if err != nil {
		return v, err
	}
	if domain.Decode(raw, &v) != nil || v.Version != 1 || v.RequestID.Validate() != nil || !skillOperation(v.Operation) || !json.Valid(v.Identity) || len(v.Identity) == 0 || preparationIdentityDigest(v.Identity) != v.IdentityDigest || domain.ValidateSkillPreparation(v.Scope) != nil || v.Scope.Action != "" || v.Scope.Preparation.RequestID != v.RequestID || (v.Actor.Type != domain.OwnerDevice && v.Actor.Type != domain.ClientDevice) || (v.State != skillPreparationPending && v.State != skillPreparationAccepted && v.State != skillPreparationCleaning && v.State != skillPreparationRemoved) {
		return v, skillUnavailable()
	}
	return v, nil
}
func writeSkillPreparation(path string, v skillPreparationJournal) error {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > skillPreparationJournalBytes {
		return domain.Fail(domain.ResourceExhausted, "The skill preparation journal limit is reached.", "Use a smaller input or wait for original cleanup.")
	}
	return security.WriteAtomicOwned(path, raw)
}
func (s *Service) skillPreparationsWakeup() {
	s.skillPreparationsOnce.Do(func() { s.skillPreparationsWake = make(chan struct{}, 1) })
	select {
	case s.skillPreparationsWake <- struct{}{}:
	default:
	}
}

// The handler must defer the returned finish closure before inspecting its error.
// Its unique claim spans dispatch and the final original mutation; a rejected
// concurrent retry cannot release another handler's claim.
func (s *Service) prepareSkills(ctx context.Context, scope domain.SkillReadRequest, request domain.ID, operation string, identity any) (domain.SkillReadResult, func(), error) {
	finish := func() {}
	empty := domain.SkillReadResult{Entries: []domain.SkillEntry{}}
	actor, ok := domain.PrincipalFrom(ctx)
	raw, err := json.Marshal(identity)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) || request.Validate() != nil || !skillOperation(operation) || err != nil || len(raw) > 1<<20 {
		return empty, finish, skillUnavailable()
	}
	liveSkillPreparations.Lock()
	root := s.Store.Root()
	owners := liveSkillPreparations.owners[root]
	if owners == nil {
		owners = map[domain.ID]bool{}
		liveSkillPreparations.owners[root] = owners
	}
	if owners[request] {
		liveSkillPreparations.Unlock()
		return empty, finish, domain.Fail(domain.Conflict, "The original skill preparation is still active.", "Wait for the original operation and retry its exact request.")
	}
	if retained, err := s.retainedSkillPreparation(request); err == nil {
		if retained.IdentityDigest != preparationIdentityDigest(raw) || retained.Operation != operation || retained.Actor != actor || retained.State == skillPreparationCleaning || retained.State == skillPreparationRemoved {
			liveSkillPreparations.Unlock()
			return empty, finish, skillUnavailable()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		liveSkillPreparations.Unlock()
		return empty, finish, skillUnavailable()
	}
	owners[request] = true
	// Release before the read lane waits. Cleanup sees this joined live owner.
	liveSkillPreparations.Unlock()
	result, err := s.observeSkills(context.WithValue(ctx, skillPreparationContextKey{}, skillPreparationOwner{RequestID: request, Operation: operation, Identity: raw, Actor: actor}), scope)
	var once sync.Once
	finish = func() { once.Do(func() { s.finishSkillPreparation(request) }) }
	return result, finish, err
}
func (s *Service) finishSkillPreparation(request domain.ID) {
	if request.Validate() != nil || s.Store == nil {
		return
	}
	liveSkillPreparations.Lock()
	delete(liveSkillPreparations.owners[s.Store.Root()], request)
	liveSkillPreparations.Unlock()
	s.skillPreparationsWakeup()
}
func (s *Service) retainSkillPreparation(ctx context.Context, scope *domain.SkillReadRequest) error {
	owner, ok := ctx.Value(skillPreparationContextKey{}).(skillPreparationOwner)
	if !ok {
		return skillUnavailable()
	}
	liveSkillPreparations.Lock()
	defer liveSkillPreparations.Unlock()
	if !liveSkillPreparations.owners[s.Store.Root()][owner.RequestID] {
		return skillUnavailable()
	}
	path := s.skillPreparationPath(owner.RequestID)
	old, err := s.retainedSkillPreparation(owner.RequestID)
	if err == nil {
		if old.IdentityDigest != preparationIdentityDigest(owner.Identity) || old.Operation != owner.Operation || old.Actor != owner.Actor || old.State == skillPreparationCleaning || old.State == skillPreparationRemoved || old.Scope.WorkerDeviceID != scope.WorkerDeviceID {
			return skillUnavailable()
		}
		scope.Preparation = old.Scope.Preparation
		if domain.SkillPreparationDigest(*scope) != old.Scope.Preparation.ScopeDigest {
			return skillUnavailable()
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return skillUnavailable()
	}
	if err = security.PrivateDir(filepath.Dir(path)); err != nil {
		return domain.SafeError(err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return domain.SafeError(err)
	}
	entries, err := directory.ReadDir(skillPreparationJournalLimit + 1)
	directory.Close()
	if err != nil && !errors.Is(err, io.EOF) || len(entries) >= skillPreparationJournalLimit {
		return domain.Fail(domain.ResourceExhausted, "The skill preparation limit is reached.", "Wait for original cleanup before selecting another package.")
	}
	scope.Preparation = &domain.SkillPreparationProof{ServerID: s.Identity.ServerID, RequestID: owner.RequestID, OriginalInstanceID: scope.WorkerInstanceID}
	scope.Preparation.ScopeDigest = domain.SkillPreparationDigest(*scope)
	if domain.ValidateSkillPreparation(*scope) != nil {
		return skillUnavailable()
	}
	journal := skillPreparationJournal{Version: 1, RequestID: owner.RequestID, Operation: owner.Operation, Identity: owner.Identity, IdentityDigest: preparationIdentityDigest(owner.Identity), Actor: owner.Actor, Scope: *scope, State: skillPreparationPending}
	return writeSkillPreparation(path, journal)
}
func (s *Service) reconcileSkillPreparations(ctx context.Context) error {
	directory := filepath.Dir(s.skillPreparationPath(domain.NewID()))
	file, err := os.Open(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return domain.SafeError(err)
	}
	entries, err := file.ReadDir(skillPreparationJournalLimit + 1)
	file.Close()
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > skillPreparationJournalLimit {
		return skillUnavailable()
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return domain.SafeError(ctx.Err())
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return skillUnavailable()
		}
		path := filepath.Join(directory, entry.Name())
		journal, err := readSkillPreparation(path)
		if err != nil {
			return skillUnavailable()
		}
		if entry.Name() != string(journal.RequestID)+".json" || journal.Scope.Preparation.ServerID != s.Identity.ServerID {
			return skillUnavailable()
		}
		if journal.State == skillPreparationRemoved {
			if err = s.retireSkillPreparation(journal); err != nil {
				return domain.SafeError(err)
			}
			continue
		}
		liveSkillPreparations.Lock()
		if liveSkillPreparations.owners[s.Store.Root()][journal.RequestID] {
			liveSkillPreparations.Unlock()
			continue
		}
		ownerContext := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
		_, found, replayErr := s.Store.Replay(ownerContext, journal.RequestID, journal.Operation, journal.Identity)
		if replayErr != nil {
			liveSkillPreparations.Unlock()
			continue
		}
		if found {
			journal.State = skillPreparationAccepted
			err = s.retireSkillPreparation(journal)
			liveSkillPreparations.Unlock()
			if err != nil {
				return domain.SafeError(err)
			}
			continue
		}
		// Only a positively absent receipt after the original handler joins (or an
		// exclusive store restart) commits removal authority before dispatch.
		journal.State = skillPreparationCleaning
		err = writeSkillPreparation(path, journal)
		liveSkillPreparations.Unlock()
		if err != nil {
			return domain.SafeError(err)
		}
		scope := journal.Scope
		scope.Action = domain.CleanupSkillPreparation
		_, err = s.observeSkills(ownerContext, scope)
		if err != nil {
			if s.logger != nil {
				s.logger.WarnContext(ctx, "skill_preparation_cleanup_pending", "request_id", journal.RequestID, "error_code", domain.SafeError(err).Code)
			}
			continue
		}
		journal.State = skillPreparationRemoved
		if err = s.retireSkillPreparation(journal); err != nil {
			return domain.SafeError(err)
		}
		if s.logger != nil {
			s.logger.InfoContext(ctx, "skill_preparation_removed", "request_id", journal.RequestID)
		}
	}
	return nil
}
func (s *Service) runSkillPreparations(ctx context.Context) {
	s.skillPreparationsOnce.Do(func() { s.skillPreparationsWake = make(chan struct{}, 1) })
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		if err := s.reconcileSkillPreparations(ctx); err != nil && ctx.Err() == nil && s.logger != nil {
			s.logger.WarnContext(ctx, "skill_preparation_scan_failed", "error_code", domain.SafeError(err).Code)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.skillPreparationsWake:
		}
	}
}
