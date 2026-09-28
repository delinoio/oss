package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInstructionInspectionRequiresExactOriginalFile(t *testing.T) {
	p := instructionProfile{path: "/owned/grok/Agents.md", contents: nativeInstructionFixture}
	for _, mutation := range []string{"valid", "foreign-path", "case", "scope", "type", "size", "missing-size", "missing-tokens", "unknown", "extra", "null"} {
		t.Run(mutation, func(t *testing.T) {
			entry := map[string]any{"path": p.path, "scope": "global", "fileType": "agents_md", "sizeBytes": len(p.contents), "approxTokens": len(p.contents) / 4}
			var inventory any = []any{entry}
			switch mutation {
			case "foreign-path":
				entry["path"] = "/foreign/Agents.md"
			case "case":
				entry["path"] = "/owned/grok/AGENTS.md"
			case "scope":
				entry["scope"] = "project"
			case "type":
				entry["fileType"] = "rules"
			case "size":
				entry["sizeBytes"] = len([]rune(p.contents))
			case "missing-size":
				delete(entry, "sizeBytes")
			case "missing-tokens":
				delete(entry, "approxTokens")
			case "unknown":
				entry["extension"] = true
			case "extra":
				inventory = []any{entry, entry}
			case "null":
				inventory = nil
			}
			raw, _ := json.Marshal(inventory)
			if (p.inspect(raw) == nil) != (mutation == "valid") {
				t.Fatal("instruction ownership validation changed")
			}
			if (instructionProfile{}).inspect(raw) == nil {
				t.Fatal("discovery inherited execution instructions")
			}
		})
	}
	if p.inspect([]byte("[]")) == nil || (instructionProfile{}).inspect([]byte("[]")) != nil {
		t.Fatal("absent instruction inventory silently changed")
	}
}

func TestInstructionsRemainPrivateExclusiveAndUnchanged(t *testing.T) {
	for _, mutation := range []string{"overwrite", "changed", "removed", "symlink", "permissions", "empty-inherited"} {
		t.Run(mutation, func(t *testing.T) {
			p := instructionProfile{path: filepath.Join(t.TempDir(), "Agents.md"), contents: nativeInstructionFixture}
			if err := p.write(); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "overwrite":
				if p.write() == nil {
					t.Fatal("existing instructions replaced")
				}
				return
			case "changed":
				if err := os.WriteFile(p.path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "removed":
				if err := os.Remove(p.path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := filepath.Join(filepath.Dir(p.path), "other")
				if err := os.Rename(p.path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, p.path); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if runtime.GOOS == "windows" {
					t.Skip("Windows private-file checks use the inherited private DACL")
				}
				if err := os.Chmod(p.path, 0644); err != nil {
					t.Fatal(err)
				}
			case "empty-inherited":
				p.contents = ""
			}
			if p.check() == nil {
				t.Fatal("changed private instructions accepted")
			}
		})
	}
}

func TestInstructionHistoryRequiresCompleteNativeEnvelope(t *testing.T) {
	p := instructionProfile{contents: nativeInstructionFixture}
	original := "Original native context\n\n<user_rule>\n" + p.contents + "</user_rule>\n</user_rules>\n</rules>"
	if !p.verifyContext(original) {
		t.Fatal("original additive native context rejected")
	}
	for _, changed := range []string{p.contents, strings.Replace(original, p.contents, "changed", 1), original + "changed", strings.Replace(original, "<user_rule>", "<system>", 1), strings.Replace(original, "한글", "", 1)} {
		if p.verifyContext(changed) {
			t.Fatal("partial or differently scoped instruction context accepted")
		}
	}
}

func TestInstructionProfileRejectsUnsupportedContentAndMode(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "valid")
	for _, contents := range []string{"\x00", string([]byte{0xff}), strings.Repeat("a", domain.MaxAppliedInstructions+1)} {
		config.Instructions = contents
		if _, err := buildAPIProfile(config); err == nil {
			t.Fatal("invalid instructions accepted")
		}
	}
	config.Instructions = nativeInstructionFixture
	config.Mode = domain.PlanMode
	if _, err := buildAPIProfile(config); err == nil {
		t.Fatal("Execute instruction evidence granted Plan")
	}
}

func TestInstructionChangeBeforeNativeInputRefusesClaim(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "valid")
	config.Instructions = nativeInstructionFixture
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api, err := openAPI(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(_ context.Context, claim CreationClaim) error { return claim.Validate() }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(api.profile.instructions.path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunText(ctx, domain.NewID(), "Original input", func(context.Context, InputClaim) error { t.Error("changed instructions claimed input"); return nil }, func(context.Context, InputObservation) error {
		t.Error("changed instructions published input")
		return nil
	}); err == nil {
		t.Fatal("changed instructions reached original native input")
	}
}
