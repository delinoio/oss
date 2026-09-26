package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func checkpointStageFixture(t *testing.T) (*checkpointResume, apiSessionConfig, *nativeAPIProfile) {
	t.Helper()
	api, home := completedCheckpointFixture(t)
	raw, ref, err := api.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		t.Fatal(err)
	}
	config := fixtureOwnedAPIConfig(t)
	config.Workspace = source.Workspace
	_, profile, err := prepareAPISession(config)
	if err != nil {
		t.Fatal(err)
	}
	source.NativeRoot = config.NativeRoot
	source.SettingsSHA256, err = checkpointSettings(&sessionAPI{apiProfile: profile, creation: &sessionCreation{settings: config.Settings}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(source)
	ref.SHA256 = mutationDigest(raw)
	if InspectCheckpoint(context.Background(), home, raw, ref) != nil {
		t.Fatal("invalid independent source fixture")
	}
	return &checkpointResume{source: source, raw: raw, ref: ref, request: domain.NewID()}, config, profile
}

func TestCheckpointStagingCopiesExactDatabaseAndWALKeepsSourceImmutable(t *testing.T) {
	r, config, profile := checkpointStageFixture(t)
	home := filepath.Dir(config.Probe.Home)
	claims := 0
	config.Claim = func(_ context.Context, claim SessionClaim) error {
		claims++
		if claim.Validate() != nil || claim.Kind != ResumeSessionMutation || claim.RequestID != r.request || claim.InputRequestID != r.ref.InputRequestID || claim.MessageID != r.ref.InputID || claim.PartID != r.ref.PartID {
			t.Fatal("staging lost original predecessor authority")
		}
		entries, err := os.ReadDir(filepath.Join(home, "data"))
		if err != nil || len(entries) != 0 {
			t.Fatal("database was copied before its durable claim")
		}
		return nil
	}
	if err := r.stage(context.Background(), config, profile); err != nil || claims != 1 {
		t.Fatal(err, claims)
	}
	for _, name := range []string{"opencode.db", "opencode.db-wal"} {
		before, err := os.ReadFile(filepath.Join(r.source.RuntimeHome, "data", "opencode", name))
		after, readErr := security.ReadPrivate(filepath.Join(home, "data", "opencode", name), 4096)
		if err != nil || readErr != nil || !bytes.Equal(before, after) {
			t.Fatal("original SQLite bytes changed during staging")
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "data", "opencode", "native-artifact")); !os.IsNotExist(err) {
		t.Fatal("unapproved auxiliary state entered the new runtime")
	}
	if InspectCheckpoint(context.Background(), r.source.RuntimeHome, r.raw, r.ref) != nil {
		t.Fatal("staging mutated original closed evidence")
	}
	if copyCheckpointDatabase(context.Background(), r.source, home) == nil {
		t.Fatal("existing staging was overwritten")
	}
}

func TestCheckpointStagingRejectsChangedAuthorityAndPreservesPartialState(t *testing.T) {
	for _, mode := range []string{"credential", "settings", "origin", "instructions", "source", "claim-failure", "source-after-claim", "occupied-target", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			r, config, profile := checkpointStageFixture(t)
			home := filepath.Dir(config.Probe.Home)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			claims := 0
			config.Claim = func(context.Context, SessionClaim) error {
				claims++
				if mode == "claim-failure" {
					return sessionUncertain()
				}
				if mode == "source-after-claim" {
					return os.WriteFile(filepath.Join(r.source.RuntimeHome, "data", "opencode", "opencode.db-wal"), []byte("contradictory original WAL"), 0600)
				}
				return nil
			}
			switch mode {
			case "credential":
				r.source.CredentialSHA256 = mutationDigest([]byte(profile.Token))
			case "settings":
				profile.Settings.Agent = PlanAgent
			case "origin":
				profile.BaseURL = "http://127.0.0.1:2/api-proxy/v1"
			case "instructions":
				profile.Instructions = "changed immutable instruction"
			case "source":
				if err := os.Remove(filepath.Join(r.source.RuntimeHome, "data", "opencode", "opencode.db-wal")); err != nil {
					t.Fatal(err)
				}
			case "occupied-target":
				if err := security.WriteAtomic(filepath.Join(home, "data", "existing"), []byte("retained evidence")); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			}
			if r.stage(ctx, config, profile) == nil {
				t.Fatal("contradictory staging acquired native launch authority")
			}
			wantClaims := 0
			if mode == "claim-failure" || mode == "source-after-claim" || mode == "occupied-target" {
				wantClaims = 1
			}
			if claims != wantClaims {
				t.Fatal("claim boundary changed", claims)
			}
			if mode == "source-after-claim" {
				if _, err := os.Stat(filepath.Join(home, "data", "opencode", "opencode.db")); err != nil {
					t.Fatal("failed partial copy lost its original evidence")
				}
			}
			if mode == "occupied-target" {
				raw, err := security.ReadPrivate(filepath.Join(home, "data", "existing"), 4096)
				if err != nil || string(raw) != "retained evidence" {
					t.Fatal("failed staging replaced existing evidence")
				}
			}
		})
	}
}

func TestCheckpointReplacementRefusesReplayBeforeClaimsOrLaunch(t *testing.T) {
	for _, mode := range []string{"owner", "workspace", "root", "request-create", "request-input", "request-owner", "resume-required", "project", "tool", "source", "reference", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			r, config, _ := checkpointStageFixture(t)
			config.Probe = fixtureOwnedAPIConfig(t).Probe
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "owner":
				config.Probe.Process.OwnerID = r.ref.OwnerID
			case "workspace":
				config.Workspace = filepath.Dir(config.Workspace)
			case "root":
				config.NativeRoot = config.Workspace
			case "request-create":
				r.request = r.ref.CreationRequestID
			case "request-input":
				r.request = r.ref.InputRequestID
			case "request-owner":
				r.request = r.ref.OwnerID
			case "resume-required":
				r.source.Reference.RequiresResume = true
				r.ref.RequiresResume = true
			case "project":
				r.source.Project = "foreign-project"
			case "tool":
				r.source.History.Messages[1].Parts[0].Kind = ToolPartKind
				r.source.History.Digest = ""
				history, _ := json.Marshal(r.source.History)
				r.source.History.Digest = mutationDigest(history)
				r.source.Reference.HistorySHA256, r.ref.HistorySHA256 = r.source.History.Digest, r.source.History.Digest
			case "source":
				if err := os.Remove(filepath.Join(r.source.RuntimeHome, "data", "opencode", "opencode.db")); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			}
			r.raw, _ = json.Marshal(r.source)
			r.ref.SHA256 = mutationDigest(r.raw)
			if mode == "reference" {
				r.ref.OwnerID = domain.NewID()
			}
			claims := 0
			config.Claim = func(context.Context, SessionClaim) error { claims++; return nil }
			if api, err := OpenResumedAPI(ctx, config, r.source.RuntimeHome, r.raw, r.ref, r.request, false); api != nil || err == nil || claims != 0 {
				t.Fatal("invalid original authority consumed a native claim")
			}
		})
	}
}
