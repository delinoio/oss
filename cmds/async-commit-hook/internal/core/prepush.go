package core

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
)

func (s *Service) PrePush(ctx context.Context, repo string, input io.Reader, override PushPolicy) ([]Gate, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	out := []Gate{}
	seen := map[string]bool{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			return out, E("invalid-pre-push-input", "expected local-ref local-oid remote-ref remote-oid", 2)
		}
		localRef, sha, remoteRef := fields[0], fields[1], fields[2]
		if strings.Trim(sha, "0") == "" || localRef == "(delete)" || !strings.HasPrefix(remoteRef, "refs/heads/") {
			continue
		}
		if !objectID.MatchString(sha) {
			return out, E("invalid-pre-push-input", "invalid branch object ID", 2)
		}
		if seen[sha] {
			continue
		}
		seen[sha] = true
		plan, e := s.Plan(ctx, repo, sha)
		if e != nil {
			return out, e
		}
		// Pushed refs can differ from the checkout (or use an explicit SHA).
		// This labels newly accepted history; exact-commit gate reuse is unchanged.
		plan.Branch = strings.TrimPrefix(remoteRef, "refs/heads/")
		if strings.HasPrefix(localRef, "refs/heads/") {
			plan.Branch = strings.TrimPrefix(localRef, "refs/heads/")
		}
		policy := override
		if policy == "" {
			policy = plan.Config.PrePush
		}
		if policy != PushBlock && policy != PushWait && policy != PushRun {
			return out, E("invalid-push-policy", "use block, wait or run-and-wait", 2)
		}
		var gate Gate
		if policy == PushRun {
			gate, e = s.selectPushAttempt(ctx, plan)
		} else {
			gate, e = s.Gate(ctx, repo, sha)
		}
		if e != nil {
			return out, e
		}
		// Selection may have durably accepted a new attempt. Publish its identity
		// before startup, storage reads or waits can fail after acceptance.
		out = append(out, gate)
		gateIndex := len(out) - 1
		if !gate.Passed {
			id := gate.RunID
			if policy == PushRun {
				if e = s.Start(s.Personal.Mode); e != nil {
					out[gateIndex].Diagnostics = append(out[gateIndex].Diagnostics, Diagnostic{
						Code: "startup-failed", Message: "request was accepted but runner startup failed; it remains saved",
						Hint: "Inspect ach status --run " + id + " and ach doctor; fix startup and retry this push to reuse the saved attempt.",
					})
					return out, e
				}
			}
			if id != "" && policy != PushBlock {
				r, e := s.Store.Run(id)
				if e != nil {
					return out, e
				}
				if !r.State.Terminal() {
					if _, e = s.Wait(ctx, id); e != nil {
						return out, e
					}
				}
				gate, e = s.Gate(ctx, repo, sha)
				if e != nil {
					return out, e
				}
			}
		}
		out[gateIndex] = gate
	}
	if e := scanner.Err(); e != nil {
		return out, Wrap("pre-push-read", e)
	}
	for _, g := range out {
		if !g.Passed {
			return out, E("push-validation-incomplete", "a pushed branch tip has no passing latest compatible attempt; use ach run/check --commit for the reported SHA", 1)
		}
	}
	return out, nil
}

func (s *Service) selectPushAttempt(ctx context.Context, plan Run) (Gate, error) {
	if m, _ := Git(ctx, plan.Source, "config", "--local", "--get", "ach.managed"); m == "true" {
		return Gate{}, E("managed-workspace", "automatic execution is disabled in managed workspaces", 2)
	}
	return s.selectPushAttemptWithGate(ctx, plan, s.GateRun)
}

func (s *Service) selectPushAttemptWithGate(ctx context.Context, plan Run, evaluate func(Run) Gate) (Gate, error) {
	var candidate *Run
	var verified Gate
	for {
		if err := ctx.Err(); err != nil {
			return Gate{}, err
		}
		var gate Gate
		created, verify := false, false
		err := s.Store.Transaction(func(tx *sql.Tx) error {
			// Only metadata selection/revalidation and insertion own the writer
			// lock. Potentially unbounded evidence hashing happens after release.
			var id string
			err := tx.QueryRow("SELECT id FROM runs WHERE repo=? AND commit_oid=? AND fingerprint=? ORDER BY seq DESC LIMIT 1", plan.RepositoryID, plan.Commit, plan.Fingerprint).Scan(&id)
			if err == nil {
				r, err := loadRun(tx, id)
				if err != nil {
					return err
				}
				if !r.State.Terminal() {
					gate = s.GateRun(r) // Nonterminal states never inspect evidence.
					return nil
				}
				if candidate == nil || !bytes.Equal(Encode(r), Encode(*candidate)) {
					candidate, verify = &r, true
					return nil
				}
				gate = verified
				if gate.Passed {
					return nil
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err = insertRun(tx, &plan, ""); err != nil {
				return err
			}
			gate = s.GateRun(plan)
			created = true
			return nil
		})
		if err != nil {
			return Gate{}, Wrap("submission-failed", err)
		}
		if verify {
			verified = evaluate(*candidate)
			// A concurrent submission or retention mutation invalidates this
			// snapshot. The next short transaction reselects before using it.
			continue
		}
		if created {
			s.Log.Info("run.accepted", "run_id", plan.ID, "commit", plan.Commit)
		}
		return gate, nil
	}
}
