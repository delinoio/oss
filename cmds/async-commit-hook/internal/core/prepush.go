package core

import (
	"bufio"
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
		if !gate.Passed {
			id := gate.RunID
			if policy == PushRun {
				if e = s.Start(s.Personal.Mode); e != nil {
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
		out = append(out, gate)
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
	var gate Gate
	created := false
	err := s.Store.Transaction(func(tx *sql.Tx) error {
		// The immediate SQLite transaction covers latest-attempt selection,
		// evidence validation and insertion across independent pre-push clients.
		var id string
		err := tx.QueryRow("SELECT id FROM runs WHERE repo=? AND commit_oid=? AND fingerprint=? ORDER BY seq DESC LIMIT 1", plan.RepositoryID, plan.Commit, plan.Fingerprint).Scan(&id)
		if err == nil {
			r, err := loadRun(tx, id)
			if err != nil {
				return err
			}
			gate = s.GateRun(r)
			if !r.State.Terminal() || gate.Passed {
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
	if created {
		s.Log.Info("run.accepted", "run_id", plan.ID, "commit", plan.Commit)
	}
	return gate, nil
}
