// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ForkRequests struct {
	Restore      domain.ID `json:"restore"`
	Fork         domain.ID `json:"fork"`
	Move         domain.ID `json:"move"`
	Mark         domain.ID `json:"mark"`
	DeleteSource domain.ID `json:"delete_source"`
}

func (r ForkRequests) Validate() error {
	return domain.UniqueIDs([]domain.ID{r.Restore, r.Fork, r.Move, r.Mark, r.DeleteSource})
}

type nativeForkMutation struct{ method, path, digest string }
type nativeForkAttempt struct {
	source, child, readPath string
	mutation                *nativeForkMutation
	attempted               map[SessionMutation]bool
	claimed                 map[SessionMutation]bool
}

// InspectForkSourceCheckpoint supplies only read-only private eligibility. The
// Worker must independently prove its accepted report, lease and current account.
func InspectForkSourceCheckpoint(ctx context.Context, home string, raw []byte, ref CheckpointReference) error {
	source, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" || forkSourceProfile(source) != nil {
		return incompatible()
	}
	return InspectCheckpoint(ctx, home, raw, ref)
}

// PrepareForkAPI has no inference authority. Config must contain a fresh valid
// nonce that the caller never registers, plus the original immutable source
// settings. Every mutation is durably claimed before its one original send.
// Lost acknowledgments reconcile only exact state in this owned copied runtime.
func PrepareForkAPI(ctx context.Context, config APIExecutionConfig, sourceHome string, sourceRaw []byte, sourceRef CheckpointReference, target string, requests ForkRequests) (raw []byte, ref CheckpointReference, returned error) {
	if requests.Validate() != nil || config.Settings.Agent != BuildAgent || config.Settings.Permission == nil || len(config.Settings.Permission) != 0 || len(config.References) != 0 || !forkWorkspacesDisjoint(config.Workspace, target) || !canonicalDirectory(target) || InspectForkSourceCheckpoint(ctx, sourceHome, sourceRaw, sourceRef) != nil {
		return nil, ref, incompatible()
	}
	if _, err := GlobalWorkspaceRoot(target); err != nil {
		return nil, ref, err
	}
	source, err := decodeCheckpoint(sourceRaw, sourceRef, sourceHome)
	if err != nil {
		return nil, ref, err
	}
	if config.Workspace != source.Workspace {
		return nil, ref, sessionUncertain()
	}
	api, err := OpenResumedAPI(ctx, config, sourceHome, sourceRaw, sourceRef, requests.Restore, BuildAgent, false)
	if err != nil {
		return nil, ref, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if api.Close(cleanup) != nil {
			returned = sessionUncertain()
			raw = nil
		}
	}()
	return prepareOwnedForkAPI(ctx, config, api, sourceHome, sourceRaw, sourceRef, source, target, requests)
}

// The owned controller is private: callers cannot inject an endpoint or acquire
// preparation authority without the independently restored source checkpoint.
func prepareOwnedForkAPI(ctx context.Context, config APIExecutionConfig, api *OwnedAPI, sourceHome string, sourceRaw []byte, sourceRef CheckpointReference, source nativeCheckpoint, target string, requests ForkRequests) (raw []byte, ref CheckpointReference, returned error) {
	phase := "source"
	defer func() {
		if logger := config.Probe.Process.Logger; logger != nil && returned != nil {
			logger.WarnContext(ctx, "opencode_fork_preparation_uncertain", "owner_id", config.Probe.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	s := api.session
	if err := s.enter(ctx); err != nil {
		return nil, ref, err
	}
	defer s.leave()
	if s.input != nil || s.events != nil || s.problem != nil || s.forkAttempt != nil || s.creation == nil || s.predecessor == nil || s.predecessorDigest != sourceRef.SHA256 {
		return nil, ref, sessionUncertain()
	}
	s.forkAttempt = &nativeForkAttempt{source: sourceRef.SessionID, attempted: map[SessionMutation]bool{}, claimed: map[SessionMutation]bool{}}
	phase = "operations"
	doc, err := s.forkRead(ctx, "/doc", http.StatusOK)
	if err != nil || validateSchemaOperations(doc, []operationProfile{{"/session/{sessionID}/fork", "post", "session.fork", "200", "application/json"}, {"/session/{sessionID}", "patch", "session.update", "200", "application/json"}, {"/session/{sessionID}", "delete", "session.delete", "200", "application/json"}, {"/experimental/control-plane/move-session", "post", "experimental.controlPlane.moveSession", "204", ""}}) != nil {
		return nil, ref, incompatible()
	}
	if err := s.forkIdle(ctx); err != nil {
		return nil, ref, err
	}
	original, err := s.forkHistory(ctx, sourceRef.SessionID, checkpointInventory(source))
	if err != nil {
		return nil, ref, err
	}
	list, err := s.forkSessions(ctx)
	if err != nil || len(list) != 1 {
		return nil, ref, sessionUncertain()
	}
	if identity, err := validateSession(list[0], s.cwd, s.creation, false); err != nil || identity != s.creation.identity {
		return nil, ref, sessionUncertain()
	}
	phase = "fork"
	// This exact empty-body native call intentionally routes through the source.
	_, sendErr := s.forkMutation(ctx, ForkSessionMutation, requests.Fork, sourceRef, http.MethodPost, "/session/"+sourceRef.SessionID+"/fork", []byte("{}"), http.StatusOK)
	list, err = s.forkSessions(ctx)
	if !s.forkAttempt.claimed[ForkSessionMutation] || err != nil || len(list) != 2 {
		return nil, ref, sessionUncertain()
	}
	var child json.RawMessage
	for _, candidate := range list {
		fields, e := object(candidate)
		if e != nil {
			return nil, ref, e
		}
		id, _ := boundedString(fields["id"], 30, true)
		if id == sourceRef.SessionID {
			if identity, e := validateSession(candidate, s.cwd, s.creation, false); e != nil || identity != s.creation.identity {
				return nil, ref, sessionUncertain()
			}
		} else {
			if child != nil || !nativeID(id, "ses") {
				return nil, ref, sessionUncertain()
			}
			child = candidate
			s.forkAttempt.child = id
		}
	}
	if child == nil {
		return nil, ref, sessionUncertain()
	}
	identity, err := validateForkSession(child, s.cwd, s.creation, false)
	if err != nil || identity.project != "global" {
		return nil, ref, sessionUncertain()
	}
	// An acknowledgment can be lost after all native cloning completed. Only the
	// unique new root and every exact inherited byte can reconcile that send.
	_ = sendErr
	childHistory, err := s.forkHistory(ctx, identity.id, nil)
	if err != nil {
		return nil, ref, err
	}
	identities, messages, err := compareForkHistory(original, childHistory, sourceRef.SessionID, identity.id)
	if err != nil {
		return nil, ref, err
	}
	histories, err := cloneForkHistories(source, identities, messages, identity.id)
	if err != nil {
		return nil, ref, err
	}
	if err := s.forkIdle(ctx); err != nil {
		return nil, ref, err
	}
	phase = "move"
	move, _ := json.Marshal(struct {
		Session     string `json:"sessionID"`
		Destination struct {
			Directory string `json:"directory"`
		} `json:"destination"`
		Move bool `json:"moveChanges"`
	}{identity.id, struct {
		Directory string `json:"directory"`
	}{target}, false})
	inheritedRef := sourceRef
	inheritedRef.SessionID = identity.id
	last := histories[len(histories)-1]
	inheritedRef.InputID = last.InputID
	inheritedRef.PartID = last.Messages[0].Parts[0].ID
	_, _ = s.forkMutation(ctx, MoveForkMutation, requests.Move, inheritedRef, http.MethodPost, "/experimental/control-plane/move-session", move, http.StatusNoContent)
	relocated, err := s.forkRead(ctx, "/session/"+identity.id, http.StatusOK)
	if !s.forkAttempt.claimed[MoveForkMutation] || err != nil || !forkRelocationMatches(child, relocated, target) {
		return nil, ref, sessionUncertain()
	}
	// Native routing must now resolve the independently inspected destination.
	s.cwd = target
	if actual, err := s.forkHistory(ctx, identity.id, messages); err != nil || !sameForkRawHistory(actual, childHistory) {
		return nil, ref, sessionUncertain()
	}
	if err := s.forkIdle(ctx); err != nil {
		return nil, ref, err
	}
	phase = "marker"
	creation := &sessionCreation{request: requests.Fork, settings: config.Settings, identity: identity, recorded: true}
	marker, _ := json.Marshal(struct {
		Metadata   sessionMetadata  `json:"metadata"`
		Permission []PermissionRule `json:"permission"`
	}{sessionMetadata{sessionMarker{requests.Fork}}, []PermissionRule{}})
	_, _ = s.forkMutation(ctx, MarkForkMutation, requests.Mark, inheritedRef, http.MethodPatch, "/session/"+identity.id, marker, http.StatusOK)
	marked, err := s.forkRead(ctx, "/session/"+identity.id, http.StatusOK)
	if err != nil {
		return nil, ref, err
	}
	observed, err := validateForkSession(marked, target, creation, true)
	if !s.forkAttempt.claimed[MarkForkMutation] || err != nil || observed != identity || !forkMarkerMatches(relocated, marked, requests.Fork) {
		return nil, ref, sessionUncertain()
	}
	if actual, err := s.forkHistory(ctx, identity.id, messages); err != nil || !sameForkRawHistory(actual, childHistory) {
		return nil, ref, sessionUncertain()
	}
	// Reinspect the copied source independently after every child mutation. A
	// changed source cannot be deleted or promoted into a completed child proof.
	if actual, err := s.forkHistory(ctx, sourceRef.SessionID, checkpointInventory(source)); err != nil || !sameForkRawHistory(actual, original) {
		return nil, ref, sessionUncertain()
	}
	phase = "copied-source-delete"
	// The closed root/session/history parsers exclude parentID and every unowned
	// foreign key before native recursive deletion of only this copied source.
	_, _ = s.forkMutation(ctx, DeleteForkSourceMutation, requests.DeleteSource, sourceRef, http.MethodDelete, "/session/"+sourceRef.SessionID, nil, http.StatusOK)
	if _, err := s.forkRead(ctx, "/session/"+sourceRef.SessionID, http.StatusNotFound); !s.forkAttempt.claimed[DeleteForkSourceMutation] || err != nil {
		return nil, ref, sessionUncertain()
	}
	list, err = s.forkSessions(ctx)
	if err != nil || len(list) != 1 || !bytes.Equal(canonicalNative(list[0]), canonicalNative(marked)) {
		return nil, ref, sessionUncertain()
	}
	if actual, err := s.forkHistory(ctx, identity.id, messages); err != nil || !sameForkRawHistory(actual, childHistory) {
		return nil, ref, sessionUncertain()
	}
	if err := s.forkIdle(ctx); err != nil {
		return nil, ref, err
	}
	phase = "target-profile"
	scope, err := GlobalWorkspaceRoot(target)
	if err != nil {
		return nil, ref, err
	}
	project := &projectConfigScope{Directory: target, Root: scope.boundary}
	if err := project.inspect(); err != nil {
		return nil, ref, err
	}
	instructions, err := collectProjectInstructions(target, scope.instructionRoot())
	if err != nil {
		return nil, ref, err
	}
	s.cwd, s.creation, s.sessionAgent = target, creation, BuildAgent
	s.apiProfile.ProjectConfig, s.apiProfile.WorkspaceRoot, s.apiProfile.ProjectInstructions = project, &scope, instructions
	settings, err := checkpointSettings(s)
	if err != nil {
		return nil, ref, err
	}
	phase = "cleanup"
	if err := s.closeOwned(ctx); err != nil {
		return nil, ref, sessionUncertain()
	}
	files, err := checkpointFiles(ctx, s.runtimeHome)
	if err != nil {
		return nil, ref, err
	}
	proof := &nativeCheckpointFork{Version: 1, RequestID: requests.Fork, SourceReference: sourceRef, SourceWorkspace: source.Workspace, SourceHistories: checkpointHistories(source), ClonedHistories: histories, Identities: identities, SelectionPending: true}
	ref = CheckpointReference{OwnerID: s.owner, CreationRequestID: requests.Fork, InputRequestID: last.RequestID, SessionID: identity.id, InputID: last.InputID, PartID: last.Messages[0].Parts[0].ID, InputSHA256: sourceRef.InputSHA256, HistorySHA256: last.Digest}
	value := nativeCheckpoint{Version: 1, NativeVersion: SupportedVersion, Reference: ref, RuntimeHome: s.runtimeHome, Workspace: target, NativeRoot: "/", Project: "global", Slug: identity.slug, Created: identity.created, SettingsSHA256: settings, CredentialSHA256: mutationDigest([]byte(config.Token)), History: last, Files: files, Fork: proof}
	if len(histories) > 1 {
		value.Previous = slices.Clone(histories[:len(histories)-1])
		value.PredecessorSHA256 = sourceRef.SHA256
	}
	raw, err = json.Marshal(value)
	if err != nil || len(raw) > maxCheckpointBytes {
		return nil, ref, sessionUncertain()
	}
	ref.SHA256 = mutationDigest(raw)
	if InspectCheckpoint(ctx, s.runtimeHome, raw, ref) != nil || InspectCheckpoint(ctx, sourceHome, sourceRaw, sourceRef) != nil {
		return nil, ref, sessionUncertain()
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_fork_native_verified", "owner_id", s.owner, "request_id", requests.Fork, "messages", len(messages), "cleanup_confirmed", true, "selection_pending", true)
	}
	return raw, ref, nil
}

func (s *sessionAPI) forkRead(ctx context.Context, path string, status int) ([]byte, error) {
	if s.forkAttempt == nil || s.forkAttempt.readPath != "" {
		return nil, sessionInvalid()
	}
	s.forkAttempt.readPath = path
	defer func() { s.forkAttempt.readPath = "" }()
	raw, _, err := s.request(ctx, http.MethodGet, path, nil, status)
	return raw, err
}
func (s *sessionAPI) forkSessions(ctx context.Context) ([]json.RawMessage, error) {
	raw, err := s.forkRead(ctx, "/session?limit=3", http.StatusOK)
	var v []json.RawMessage
	if err != nil || domain.Decode(raw, &v) != nil || v == nil || len(v) > 2 {
		return nil, sessionUncertain()
	}
	return v, nil
}
func (s *sessionAPI) forkIdle(ctx context.Context) error {
	for _, path := range []string{"/session/status", "/permission", "/question"} {
		raw, err := s.forkRead(ctx, path, http.StatusOK)
		if err != nil {
			return err
		}
		if path == "/session/status" {
			m, err := object(raw)
			if err != nil || len(m) != 0 {
				return sessionUncertain()
			}
		} else {
			var v []json.RawMessage
			if domain.Decode(raw, &v) != nil || v == nil || len(v) != 0 {
				return sessionUncertain()
			}
		}
	}
	return nil
}
func (s *sessionAPI) forkHistory(ctx context.Context, id string, expected []HistoryMessage) ([]json.RawMessage, error) {
	if s.forkAttempt == nil || id != s.forkAttempt.source && id != s.forkAttempt.child {
		return nil, sessionInvalid()
	}
	var result []json.RawMessage
	path := "/session/" + id + "/message?limit=1"
	seen := map[string]bool{}
	total := 0
	defer func() { s.historyRead = nil }()
	for len(result) < maxObservedMessages {
		s.historyRead = &historyPageRead{path: path}
		raw, _, err := s.request(ctx, http.MethodGet, path, nil, http.StatusOK)
		if err != nil {
			return nil, err
		}
		total += len(raw)
		if total > maxObservedBytes {
			return nil, eventBound()
		}
		var page []json.RawMessage
		if domain.Decode(raw, &page) != nil || len(page) != 1 {
			return nil, sessionUncertain()
		}
		result = append(result, bytes.Clone(page[0]))
		cursor := s.historyRead.cursor
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return nil, sessionUncertain()
		}
		seen[cursor] = true
		path = "/session/" + id + "/message?limit=1&before=" + url.QueryEscape(cursor)
	}
	if s.historyRead.cursor != "" {
		return nil, eventBound()
	}
	slices.Reverse(result)
	if expected != nil {
		if len(result) != len(expected) {
			return nil, sessionUncertain()
		}
		for i := range expected {
			if !checkpointMessageMatches(result[i], expected[i]) {
				return nil, sessionUncertain()
			}
		}
	}
	return result, nil
}
func (s *sessionAPI) forkMutation(ctx context.Context, kind SessionMutation, id domain.ID, source CheckpointReference, method, path string, body []byte, status int) ([]byte, error) {
	f := s.forkAttempt
	if f == nil || f.mutation != nil || f.attempted[kind] {
		return nil, sessionConflict()
	}
	f.attempted[kind] = true
	claim := SessionClaim{RequestID: id, Kind: kind, SessionID: source.SessionID, MessageID: source.InputID, PartID: source.PartID, InputRequestID: source.InputRequestID, BodyDigest: mutationDigest(body)}
	if claim.Validate() != nil || s.claim(ctx, claim) != nil {
		return nil, sessionUncertain()
	}
	f.claimed[kind] = true
	f.mutation = &nativeForkMutation{method: method, path: path, digest: claim.BodyDigest}
	defer func() { f.mutation = nil }()
	raw, _, err := s.request(ctx, method, path, body, status)
	return raw, err
}
func sameForkRawHistory(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(canonicalNative(a[i]), canonicalNative(b[i])) {
			return false
		}
	}
	return true
}
func forkRelocationMatches(before, after []byte, target string) bool {
	a, err := object(before)
	if err != nil {
		return false
	}
	b, err := object(after)
	if err != nil || !scalar(b["directory"], target) || !scalar(b["projectID"], "global") {
		return false
	}
	// The global native project's root is '/', so path is its relative directory.
	relative, err := filepath.Rel("/", target)
	if err != nil || !scalar(b["path"], filepath.ToSlash(relative)) {
		return false
	}
	if _, exists := a["parentID"]; exists {
		return false
	}
	if _, exists := b["parentID"]; exists {
		return false
	}
	ta, err := shape(a["time"], []string{"created", "updated"}, nil)
	if err != nil {
		return false
	}
	tb, err := shape(b["time"], []string{"created", "updated"}, nil)
	if err != nil || !bytes.Equal(ta["created"], tb["created"]) {
		return false
	}
	x, ok := nativeCount(ta["updated"])
	y, valid := nativeCount(tb["updated"])
	if !ok || !valid || y < x {
		return false
	}
	delete(ta, "updated")
	delete(tb, "updated")
	a["time"], _ = json.Marshal(ta)
	b["time"], _ = json.Marshal(tb)
	delete(a, "directory")
	delete(b, "directory")
	delete(a, "path")
	delete(b, "path")
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(canonicalNative(left), canonicalNative(right))
}
func forkMarkerMatches(before, after []byte, request domain.ID) bool {
	a, err := object(before)
	if err != nil {
		return false
	}
	b, err := object(after)
	if err != nil || !bytes.Equal(b["permission"], []byte("[]")) {
		return false
	}
	if _, present := a["permission"]; present {
		return false
	}
	a["metadata"], _ = json.Marshal(sessionMetadata{sessionMarker{request}})
	a["permission"] = json.RawMessage("[]")
	ta, err := shape(a["time"], []string{"created", "updated"}, nil)
	if err != nil {
		return false
	}
	tb, err := shape(b["time"], []string{"created", "updated"}, nil)
	if err != nil || !bytes.Equal(ta["created"], tb["created"]) {
		return false
	}
	x, ok := nativeCount(ta["updated"])
	y, valid := nativeCount(tb["updated"])
	if !ok || !valid || y < x {
		return false
	}
	ta["updated"] = tb["updated"]
	a["time"], _ = json.Marshal(ta)
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(canonicalNative(left), canonicalNative(right))
}

// The Worker proves ownership of sibling managed session roots; their 'chat'
// leaves need not share an immediate parent. Reject every overlapping native
// workspace here independently before restoring or mutating a copied runtime.
func forkWorkspacesDisjoint(source, target string) bool {
	if source == target {
		return false
	}
	for _, pair := range [][2]string{{source, target}, {target, source}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// InspectForkCheckpoint returns only child identity/provenance metadata after
// private full-file validation. It grants no restoration or mutation authority.
func InspectForkCheckpoint(ctx context.Context, home string, raw []byte, ref CheckpointReference) ([]ForkMessageIdentity, error) {
	c, err := decodeCheckpoint(raw, ref, home)
	if err != nil || c.Fork == nil || !c.Fork.SelectionPending || len(checkpointHistories(c)) != len(c.Fork.ClonedHistories) || InspectCheckpoint(ctx, home, raw, ref) != nil {
		return nil, sessionUncertain()
	}
	return cloneForkProof(c.Fork).Identities, nil
}

// InspectForkClaims compares every durable once-only intent to the private native
// lineage, including the inert credential/runtime digest used for restoration.
// It neither restores a session nor exposes native history or a credential.
func InspectForkClaims(raw []byte, ref CheckpointReference, home string, requests ForkRequests, claims []SessionClaim) error {
	c, err := decodeCheckpoint(raw, ref, home)
	if err != nil || c.Fork == nil || !c.Fork.SelectionPending || requests.Validate() != nil || len(claims) != 5 || requests.Fork != c.Reference.CreationRequestID {
		return sessionUncertain()
	}
	source := c.Fork.SourceReference
	intent, _ := json.Marshal(struct {
		CheckpointSHA256 string
		OwnerID          domain.ID
		RuntimeSHA256    string
		CredentialSHA256 string
	}{source.SHA256, c.Reference.OwnerID, mutationDigest([]byte(home)), c.CredentialSHA256})
	move, _ := json.Marshal(struct {
		Session     string `json:"sessionID"`
		Destination struct {
			Directory string `json:"directory"`
		} `json:"destination"`
		Move bool `json:"moveChanges"`
	}{ref.SessionID, struct {
		Directory string `json:"directory"`
	}{c.Workspace}, false})
	marker, _ := json.Marshal(struct {
		Metadata   sessionMetadata  `json:"metadata"`
		Permission []PermissionRule `json:"permission"`
	}{sessionMetadata{sessionMarker{requests.Fork}}, []PermissionRule{}})
	kinds := []SessionMutation{ResumeSessionMutation, ForkSessionMutation, MoveForkMutation, MarkForkMutation, DeleteForkSourceMutation}
	ids := []domain.ID{requests.Restore, requests.Fork, requests.Move, requests.Mark, requests.DeleteSource}
	bodies := [][]byte{intent, []byte("{}"), move, marker, nil}
	for n, actual := range claims {
		scope := source
		if n == 2 || n == 3 {
			scope.SessionID, scope.InputID, scope.PartID = ref.SessionID, ref.InputID, ref.PartID
		}
		expected := SessionClaim{RequestID: ids[n], Kind: kinds[n], SessionID: scope.SessionID, MessageID: scope.InputID, PartID: scope.PartID, InputRequestID: source.InputRequestID, BodyDigest: mutationDigest(bodies[n])}
		if expected.Validate() != nil || actual != expected {
			return sessionUncertain()
		}
	}
	return nil
}
