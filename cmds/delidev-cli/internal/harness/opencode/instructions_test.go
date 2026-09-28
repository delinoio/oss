package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const privateInstructionsFixture = "Private first template 지침.\nPrivate second template: preserve order."

func TestOwnedInstructionsRetainPrivateOriginalBytes(t *testing.T) {
	config := fixtureOwnedAPIConfig(t)
	config.Instructions = privateInstructionsFixture
	env, profile, err := prepareAPISession(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(config.Probe.Home), "instructions.txt")
	raw, err := security.ReadPrivate(path, 256<<10)
	if err != nil || string(raw) != config.Instructions || profile.InstructionsPath != path {
		t.Fatal("private original instructions were not retained")
	}
	if strings.Contains(strings.Join(env, "\n"), config.Instructions) {
		t.Fatal("instructions entered the process environment")
	}
	value, _ := profile.config()
	paths, ok := value["instructions"].([]string)
	if !ok || len(paths) != 1 || paths[0] != path || value["agent"] != nil || value["system"] != nil {
		t.Fatal("instructions replaced native agent prompt authority")
	}
	serialized, _ := json.Marshal(profile)
	if string(serialized) != "{}" {
		t.Fatal("private instructions became ordinary output")
	}
	if _, _, err := prepareAPISession(config); err == nil {
		t.Fatal("retained instruction runtime reopened as fresh authority")
	}
	profile.Instructions = "changed replacement"
	if err := profile.writeInstructions(); err == nil {
		t.Fatal("original instructions overwritten")
	}
	raw, err = security.ReadPrivate(path, 256<<10)
	if err != nil || string(raw) != config.Instructions {
		t.Fatal("refused initialization changed retained instructions")
	}
}

func TestInvalidInstructionsCannotCreateRuntimeFile(t *testing.T) {
	for _, instructions := range []string{strings.Repeat("x", (256<<10)+1), "invalid\x00instruction", "invalid\xffinstruction"} {
		config := fixtureOwnedAPIConfig(t)
		config.Instructions = instructions
		if _, _, err := prepareAPISession(config); err == nil {
			t.Fatal("invalid instructions accepted")
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(config.Probe.Home), "instructions.txt")); !os.IsNotExist(err) {
			t.Fatal("invalid selection created an instruction file")
		}
	}
}

func TestChangedInstructionsBlockOriginalMutationsWithoutClaims(t *testing.T) {
	for _, stage := range []string{"initialization", "creation", "input"} {
		for _, change := range []string{"missing", "altered", "symlink"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				config := fixtureOwnedAPIConfig(t)
				config.Instructions = privateInstructionsFixture
				_, profile, err := prepareAPISession(config)
				if err != nil {
					t.Fatal(err)
				}
				f := newSessionFixture(t)
				api := f.api
				api.apiProfile, api.apiVerified, api.rejectionPolicy = profile, true, profile.Rejection
				if stage == "input" {
					if _, err := api.create(context.Background(), domain.NewID(), config.Settings); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Remove(profile.InstructionsPath); err != nil {
					t.Fatal(err)
				}
				switch change {
				case "altered":
					if err := os.WriteFile(profile.InstructionsPath, []byte("changed private instruction"), 0600); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := filepath.Join(t.TempDir(), "external.txt")
					if err := os.WriteFile(target, []byte(config.Instructions), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, profile.InstructionsPath); err != nil {
						t.Skip("native symlink privilege unavailable")
					}
				}
				before := f.postCount()
				var attempted error
				switch stage {
				case "initialization":
					attempted = api.verifyAPIProfile(context.Background())
				case "creation":
					_, attempted = api.create(context.Background(), domain.NewID(), config.Settings)
				case "input":
					_, attempted = api.submitText(context.Background(), domain.NewID(), "private prompt")
				}
				if attempted == nil || api.problem == nil || api.apiVerified || f.postCount() != before || len(f.claims) != before {
					t.Fatal("changed instructions gained HTTP or mutation claims")
				}
				_ = os.Remove(profile.InstructionsPath)
				if err := os.WriteFile(profile.InstructionsPath, []byte(config.Instructions), 0600); err != nil {
					t.Fatal(err)
				}
				if api.verifyAPIProfile(context.Background()) == nil {
					t.Fatal("restored instructions erased the original contradiction")
				}
				for _, private := range []string{config.Instructions, profile.InstructionsPath, "changed private instruction"} {
					if strings.Contains(f.logs.String(), private) {
						t.Fatal("private instructions entered diagnostics")
					}
				}
			})
		}
	}
}
