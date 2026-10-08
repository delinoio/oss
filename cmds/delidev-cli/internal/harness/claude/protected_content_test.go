package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	if len(os.Args) != 3 || os.Args[1] != "--protected-native-fixture" {
		return
	}
	secret := os.Getenv("ANTHROPIC_AUTH_TOKEN")
	if os.Args[2] == "caller" {
		secret = "additional-protected-fixture"
	}
	body := map[string]any{"type": "assistant", "uuid": "original-message", "session_id": "original-session", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": protectedFixtureForm(secret, os.Args[2])}}}}
	_ = json.NewEncoder(os.Stdout).Encode(body)
	for {
		time.Sleep(time.Second)
	}
}
func protectedFixtureForm(secret, form string) any {
	switch form {
	case "escaped":
		raw, _ := json.Marshal(secret)
		const digits = "0123456789abcdef"
		b := secret[0]
		return json.RawMessage(`"\u00` + string([]byte{digits[b>>4], digits[b&15]}) + string(raw[2:]))
	case "base64":
		return base64.StdEncoding.EncodeToString([]byte(secret))
	case "raw-base64":
		return base64.RawStdEncoding.EncodeToString([]byte(secret))
	case "url-base64":
		return base64.URLEncoding.EncodeToString([]byte(secret))
	case "raw-url-base64":
		return base64.RawURLEncoding.EncodeToString([]byte(secret))
	default:
		return secret
	}
}
func TestProtectedNativeStreamRefusesBeforeRetention(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		for _, form := range []string{"literal", "escaped", "base64", "raw-base64", "url-base64", "raw-url-base64", "caller"} {
			t.Run(form+map[bool]string{false: "-fresh", true: "-resumed"}[resumed], func(t *testing.T) {
				cfg, logs := apiFixtureConfig(t, "valid")
				cfg.Process.ProtectedValues = []string{"additional-protected-fixture"}
				if resumed {
					if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.Home), "instructions.txt"), []byte(cfg.Instructions), 0600); err != nil {
						t.Fatal(err)
					}
				}
				prepared, err := prepareAPIStreamMode(cfg, resumed)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(prepared.ProtectedValues, cfg.API.Token) || !slices.Contains(prepared.ProtectedValues, cfg.Process.ProtectedValues[0]) || len(cfg.Process.ProtectedValues) != 1 {
					t.Fatal("original protected values lost or aliased")
				}
				prepared.Args = []string{"--protected-native-fixture", form}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				stream, err := StartStream(ctx, prepared)
				if err != nil {
					t.Fatal(err)
				}
				event, err := stream.Next(ctx)
				if err == nil || domain.SafeError(err).Code != domain.Unsupported || len(event.Body) != 0 {
					t.Fatal("protected native content exposed", err)
				}
				if err = stream.Close(); err != nil {
					t.Fatal(err)
				}
				if err = process.ReconcileOwner(prepared.Directory, prepared.OwnerID); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(logs.String(), cfg.API.Token) || strings.Contains(logs.String(), "additional-protected-fixture") {
					t.Fatal("protected data entered logs")
				}
			})
		}
	}
}
func TestProtectedRetainedHistoryRefusesWithoutMutation(t *testing.T) {
	for _, form := range []string{"literal", "escaped", "base64", "raw-base64", "url-base64", "raw-url-base64"} {
		t.Run(form, func(t *testing.T) {
			secret := "private-history-runtime-token"
			session, workspace, records, messages := historyFixture(t)
			// Native unforwarded context is otherwise valid history. It must not bypass
			// the original-runtime guard merely because no public message matched it.
			records[2]["attachment"].(map[string]any)["content"] = protectedFixtureForm(secret, form)
			raw := historyJSONL(t, records)
			name := filepath.Join("projects", "delidev", string(session)+".jsonl")
			home := historyFileFixture(t, map[string][]byte{name: raw})
			if _, err := ReadMainTranscript(context.Background(), home, session, workspace, messages, nil, nil, nil); err != nil {
				t.Fatal("reflection fixture not valid original history", err)
			}
			if _, err := readMainTranscriptWithInlineTools(context.Background(), home, session, workspace, messages, nil, nil, nil, nil, nil, secret); err == nil {
				t.Fatal("protected main history retained")
			}
			stored, _ := os.ReadFile(filepath.Join(home, name))
			if string(stored) != string(raw) {
				t.Fatal("native history rewritten")
			}
			child, childWorkspace, childRecords, metadata, binding, proofs := childHistoryFixture(t)
			childRecords[1]["attachment"].(map[string]any)["content"] = protectedFixtureForm(secret, form)
			childRaw := historyJSONL(t, childRecords)
			sidecar, _ := json.Marshal(metadata)
			base := filepath.Join("projects", "delidev", string(child), "subagents", "agent-"+binding.TaskID)
			childHome := historyFileFixture(t, map[string][]byte{base + ".jsonl": childRaw, base + ".meta.json": sidecar})
			if _, err := ReadChildTranscript(context.Background(), childHome, child, childWorkspace, binding, proofs, nil); err != nil {
				t.Fatal("reflection child fixture not valid", err)
			}
			if _, err := ReadChildTranscript(context.Background(), childHome, child, childWorkspace, binding, proofs, nil, secret); err == nil {
				t.Fatal("protected child history retained")
			}
			childStored, _ := os.ReadFile(filepath.Join(childHome, base+".jsonl"))
			if string(childStored) != string(childRaw) {
				t.Fatal("child history rewritten")
			}
		})
	}
}

func TestProtectedControllerRetainsGuardWithoutAPIConfiguration(t *testing.T) {
	cfg, _ := apiFixtureConfig(t, "valid")
	cfg.Process.ProtectedValues = []string{"additional-protected-fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.config.API.Token != "" || !slices.Contains(session.config.Process.ProtectedValues, cfg.API.Token) || !slices.Contains(session.config.Process.ProtectedValues, cfg.Process.ProtectedValues[0]) {
		t.Fatal("closed controller lost transient original guard")
	}
	raw, _ := json.Marshal(session.config)
	if strings.Contains(string(raw), cfg.API.Token) || strings.Contains(string(raw), cfg.Process.ProtectedValues[0]) {
		t.Fatal("transient guard serialized")
	}
}

func TestProtectedInitializationRejectsOriginalCredentials(t *testing.T) {
	for _, form := range []string{"literal", "escaped", "base64", "raw-base64", "url-base64", "raw-url-base64"} {
		t.Run(form, func(t *testing.T) {
			cfg, logs := apiFixtureConfig(t, "reflect-init-"+form)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := OpenAPIStream(ctx, cfg)
			if err == nil {
				stream.Close()
				t.Fatal("protected initialization retained")
			}
			code := domain.SafeError(err).Code
			if code != domain.Unsupported && code != domain.RecoveryRequired {
				t.Fatal("reflection returned unsafe or unexpected error", err)
			}
			if strings.Contains(logs.String(), cfg.API.Token) {
				t.Fatal("credential entered diagnostics")
			}
			if err = process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
