// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

const replayLimit = 2 << 20
const replayEntries = 256

type replayEntry struct {
	Key       string   `json:"key"`
	Signature string   `json:"signature"`
	Variants  []string `json:"variants,omitempty"`
}
type replaySnapshot struct {
	Version int           `json:"version"`
	Binding string        `json:"binding"`
	Entries []replayEntry `json:"entries"`
}

var _ inference.StatefulAdapter = (*Adapter)(nil)
var _ inference.ContextPreparer = (*Adapter)(nil)
var _ inference.ContextualAdapter = (*Adapter)(nil)

// replayKey retains a digest, never tool arguments. JSON's sorted object keys
// make equivalent argument objects stable without flattening string values.
func replayKey(name string, args any) string {
	if args == nil {
		args = object{}
	}
	b, err := json.Marshal(args)
	if err != nil || len(b) > 64<<10 {
		return ""
	}
	h := sha256.Sum256(append(append([]byte(name), 0), b...))
	return hex.EncodeToString(h[:])
}
func replayBinding(in inference.AdapterInput) string {
	fields := []string{in.Principal.OrganizationID, in.Principal.UserID, in.Target.Provider, in.Target.BaseURL, in.Target.Model, in.Credential.AccountID, in.Credential.Secret, in.Credential.Headers.Get(ProjectHeader)}
	if in.Target.Wire == WireVertex {
		fields = append(fields, selectedMetadata(in, ProjectHeader, "project"), selectedMetadata(in, LocationHeader, "location"), string(WireVertex))
	}
	b, _ := json.Marshal(fields)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// RestoreState uses only the executor's exact-account, deletion-fenced store.
// A missing explicit continuation fails closed; a new conversation needs no state.
func (a *Adapter) RestoreState(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	s, _ := ctx.Value(requestKey{}).(*requestState)
	if s == nil {
		ctx = a.PrepareContext(ctx)
		s = ctx.Value(requestKey{}).(*requestState)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.built {
		return ctx, failure(500, "google_request_context_reused")
	}
	s.binding = replayBinding(in)
	s.sessionID = ""
	if (a.cca || a.vertex) && !strings.HasPrefix(strings.ToLower(in.Target.Model), "claude") {
		if anchor := headerAnchor(in); anchor != "" {
			if a.vertex {
				anchor += "\x00" + s.binding
			}
			h := sha256.Sum256([]byte(anchor))
			s.sessionID = "google-session:" + hex.EncodeToString(h[:])
		}
	}
	var b []byte
	var err error
	if in.PreviousResponseID != "" {
		if store == nil {
			return ctx, inference.ErrContinuation
		}
		b, err = store.Load(ctx, in.PreviousResponseID)
	} else if s.sessionID != "" {
		lookup, ok := store.(inference.AdapterStateLookup)
		if !ok {
			return ctx, failure(503, "google_session_state_unavailable")
		}
		if _, ok := store.(inference.AdapterStateUpdater); !ok {
			return ctx, failure(503, "google_session_state_unavailable")
		}
		var found bool
		b, found, err = lookup.Lookup(ctx, s.sessionID)
		if err == nil && !found {
			return ctx, nil
		}
	} else {
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	snapshot, err := decodeReplaySnapshot(b, s.binding)
	if err != nil {
		return ctx, err
	}
	s.replay = append([]replayEntry(nil), snapshot.Entries...)
	return ctx, nil
}

// CaptureState is called only after core delivery and authorization checks. The
// adapter additionally requires complete, valid provider output. Storage owns TTL
// enforcement, encryption, aggregate quotas, and atomic deletion fences.
func (*Adapter) CaptureState(ctx context.Context, in inference.AdapterInput, id string, store inference.AdapterStateStore) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, _ := ctx.Value(requestKey{}).(*requestState)
	if s == nil || store == nil || id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.binding != replayBinding(in) {
		return inference.ErrContinuation
	}
	if !s.complete {
		return nil
	}
	b, err := json.Marshal(replaySnapshot{1, replayBinding(in), s.replay})
	if err != nil {
		return err
	}
	if len(b) > replayLimit {
		return failure(500, "google_state_limit")
	}
	if err := store.Save(ctx, id, b, time.Hour); err != nil {
		return err
	}
	if s.sessionID != "" {
		updater, ok := store.(inference.AdapterStateUpdater)
		if !ok {
			return failure(503, "google_session_state_unavailable")
		}
		return updater.Update(ctx, s.sessionID, time.Hour, func(current []byte) ([]byte, error) {
			merged := &requestState{}
			if len(current) == 0 {
				merged.replay = append([]replayEntry(nil), s.replay...)
			}
			if len(current) > 0 {
				snapshot, err := decodeReplaySnapshot(current, s.binding)
				if err != nil {
					return nil, err
				}
				merged.replay = snapshot.Entries
			}
			for _, e := range s.observed {
				if len(e.Variants) == 0 {
					merged.remember(e.Key, e.Signature)
				} else {
					for _, sig := range e.Variants {
						merged.remember(e.Key, sig)
					}
				}
			}
			return json.Marshal(replaySnapshot{1, s.binding, merged.replay})
		})
	}
	return nil
}
func (s *requestState) remember(key, sig string) {
	if key == "" || !realSignature(sig) {
		return
	}
	variants := []string{}
	for i, e := range s.replay {
		if e.Key == key {
			variants = append(variants, e.Variants...)
			if len(variants) == 0 {
				variants = append(variants, e.Signature)
			}
			s.replay = append(s.replay[:i], s.replay[i+1:]...)
			break
		}
	}
	seen := false
	for _, v := range variants {
		if v == sig {
			seen = true
			break
		}
	}
	if !seen {
		variants = append(variants, sig)
	}
	if len(variants) > 32 {
		variants = variants[len(variants)-32:]
	}
	s.replay = append(s.replay, replayEntry{Key: key, Signature: sig, Variants: variants})
	// Bound the serialized snapshot even with maximum-length signatures.
	size := 512
	for i := len(s.replay) - 1; i >= 0; i-- {
		size += len(s.replay[i].Key) + len(s.replay[i].Signature) + 128
		for _, v := range s.replay[i].Variants {
			size += len(v) + 4
		}
		if size > replayLimit || len(s.replay)-i > replayEntries {
			s.replay = append([]replayEntry(nil), s.replay[i+1:]...)
			break
		}
	}
}
func (s *requestState) restoreCalls(body object, aliases map[string]string) {
	occurrences := map[string]int{}
	contents := array(body["contents"])
	for ci := len(contents) - 1; ci >= 0; ci-- {
		content := obj(contents[ci])
		if text(content["role"]) != "model" {
			continue
		}
		parts := array(content["parts"])
		for pi := len(parts) - 1; pi >= 0; pi-- {
			p := obj(parts[pi])
			call := obj(p["functionCall"])
			if call == nil {
				continue
			}
			name := text(call["name"])
			if original, ok := aliases[name]; ok {
				name = original
			}
			key := replayKey(name, call["args"])
			// Custom tool clients replay a JSON input string inside {input: ...}.
			// Exact argument identity wins; unwrap only when that key was not observed.
			if !s.hasReplayKey(key) {
				raw := strings.TrimSpace(text(obj(call["args"])["input"]))
				if len(raw) > 0 && len(raw) <= 64<<10 {
					var parsed any
					d := json.NewDecoder(strings.NewReader(raw))
					d.UseNumber()
					if d.Decode(&parsed) == nil {
						switch parsed.(type) {
						case map[string]any, []any:
							// Require a complete JSON value, including no trailing garbage.
							var extra any
							if d.Decode(&extra) == io.EOF {
								candidate := replayKey(name, parsed)
								if s.hasReplayKey(candidate) {
									key = candidate
								}
							}
						}
					}
				}
			}
			occurrence := occurrences[key]
			occurrences[key]++
			if realSignature(signature(p)) {
				continue
			}
			for i := len(s.replay) - 1; i >= 0; i-- {
				e := s.replay[i]
				if e.Key == key {
					sig := e.Signature
					if occurrence < len(e.Variants) {
						sig = e.Variants[len(e.Variants)-1-occurrence]
					}
					p["thoughtSignature"] = sig
					break
				}
			}
		}
	}
}
func (s *responseState) completeReplay() {
	if s.request == nil {
		return
	}
	s.request.mu.Lock()
	defer s.request.mu.Unlock()
	for _, e := range s.replay {
		if len(e.Variants) == 0 {
			s.request.remember(e.Key, e.Signature)
		} else {
			for _, sig := range e.Variants {
				s.request.remember(e.Key, sig)
			}
		}
	}
	s.request.observed = append([]replayEntry(nil), s.replay...)
	s.request.complete = true
}

func (s *requestState) hasReplayKey(key string) bool {
	if key == "" {
		return false
	}
	for _, e := range s.replay {
		if e.Key == key {
			return true
		}
	}
	return false
}

func decodeReplaySnapshot(b []byte, binding string) (replaySnapshot, error) {
	if len(b) > replayLimit {
		return replaySnapshot{}, inference.ErrContinuation
	}
	var snapshot replaySnapshot
	if json.Unmarshal(b, &snapshot) != nil || snapshot.Version != 1 || snapshot.Binding != binding || len(snapshot.Entries) > replayEntries {
		return replaySnapshot{}, inference.ErrContinuation
	}
	for _, entry := range snapshot.Entries {
		key, err := hex.DecodeString(entry.Key)
		if err != nil || len(key) != sha256.Size || !realSignature(entry.Signature) || len(entry.Variants) > 32 {
			return replaySnapshot{}, inference.ErrContinuation
		}
	}
	for _, e := range snapshot.Entries {
		for _, sig := range e.Variants {
			if !realSignature(sig) {
				return replaySnapshot{}, inference.ErrContinuation
			}
		}
	}
	return snapshot, nil
}

// Match the pin's parent/own anchor. Parentless root requests keep their text
// anchor; prompt-cache keys and request IDs are not conversation identities.
func headerAnchor(in inference.AdapterInput) string {
	parent := strings.TrimSpace(in.ClientHeaders.Get("x-codex-parent-thread-id"))
	own := strings.TrimSpace(in.ClientHeaders.Get("thread-id"))
	for _, v := range []string{parent, own} {
		if len(v) > 4096 || strings.ContainsAny(v, "\x00\r\n") {
			return ""
		}
	}
	if parent == "" {
		return ""
	}
	if own != "" {
		return "codex-thread:" + parent + "\x00" + own
	}
	return "codex-thread:" + parent
}
