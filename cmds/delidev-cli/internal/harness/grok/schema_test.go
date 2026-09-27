package grok

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeLaunchErrorsDoNotExposeControllerPaths(t *testing.T) {
	problem := nativeLaunchError(errors.New("write unix /private-controller-sentinel: broken pipe"))
	if domain.SafeError(problem).Code != domain.Unavailable || strings.Contains(problem.Error(), "sentinel") {
		t.Fatal("native launch error exposed private controller details")
	}
	for _, code := range []domain.Code{domain.Canceled, domain.RecoveryRequired, domain.NotFound, domain.PermissionDenied, domain.Unsupported} {
		if got := nativeLaunchError(domain.Fail(code, "safe", "safe")); domain.SafeError(got).Code != code {
			t.Fatal("typed native launch failure changed")
		}
	}
}

// Every nested key is part of the pinned profile, including metadata. Exercise
// case aliases recursively rather than relying on encoding/json strict mode.
func TestNativeProfilesRejectNestedCaseAliases(t *testing.T) {
	for _, profile := range []struct {
		name     string
		raw      []byte
		validate func([]byte) error
	}{
		{"inspect", inspectionFixture, func(raw []byte) error { return validateInspection(raw, "/private/fixture") }},
		{"initialize", fixtureResponse(domain.NewID(), "/private/fixture"), nil},
		{"session", sessionFixture, func(raw []byte) error {
			_, err := validateNewSession(raw, "/private/workspace", apiProfile{contextTokens: 32000})
			return err
		}},
	} {
		if profile.validate == nil {
			var envelope struct {
				ID domain.ID `json:"id"`
			}
			_ = json.Unmarshal(profile.raw, &envelope)
			profile.validate = func(raw []byte) error { return validateInitialize(raw, envelope.ID, "/private/fixture") }
		}
		if err := profile.validate(profile.raw); err != nil {
			t.Fatal(err)
		}
		var inspect func(any, string)
		root := fixtureObject(profile.raw)
		inspect = func(value any, path string) {
			switch node := value.(type) {
			case map[string]any:
				for key, child := range node {
					alias := strings.ToUpper(key)
					t.Run(profile.name+path+"/"+key, func(t *testing.T) {
						node[alias] = child
						raw, _ := json.Marshal(root)
						delete(node, alias)
						if profile.validate(raw) == nil {
							t.Fatal("case alias accepted")
						}
					})
					inspect(child, path+"/"+key)
				}
			case []any:
				for _, child := range node {
					inspect(child, path+"/item")
				}
			}
		}
		inspect(root, "")
	}
}

func TestInitializeRejectsForeignOrUninspectedFacts(t *testing.T) {
	for _, change := range []string{"version", "protocol", "cwd", "auth", "auth-method", "models", "mcp", "metadata", "hostname", "agent", "instance", "command", "hook", "null-flag", "missing-flag", "extra", "null-error", "mixed-error", "duplicate", "utf8", "null"} {
		t.Run(change, func(t *testing.T) {
			id := domain.NewID()
			message := fixtureObject(fixtureResponse(id, "/private/fixture"))
			result := message["result"].(map[string]any)
			meta := result["_meta"].(map[string]any)
			switch change {
			case "version":
				meta["agentVersion"] = "1.0.42"
			case "protocol":
				result["protocolVersion"] = 2
			case "cwd":
				meta["currentWorkingDirectory"] = "/foreign"
			case "auth":
				meta["defaultAuthMethodId"] = "grok.com"
			case "auth-method":
				result["authMethods"].([]any)[0].(map[string]any)["id"] = "cached_token"
			case "models":
				meta["modelState"].(map[string]any)["availableModels"] = []any{map[string]any{"id": "foreign"}}
			case "mcp":
				meta["mcpServers"] = []any{"foreign"}
			case "metadata":
				meta["metadata"] = map[string]any{}
			case "hostname":
				meta["hostname"] = strings.Repeat("x", 1025)
			case "agent":
				meta["agentId"] = domain.NewID()
			case "instance":
				meta["agentInstanceId"] = domain.NewID()
			case "command":
				meta["availableCommands"].([]any)[0].(map[string]any)["name"] = "login"
			case "hook":
				result["agentCapabilities"].(map[string]any)["_meta"].(map[string]any)["x.ai/hooks"].(map[string]any)["decisions"] = []any{"allow"}
			case "null-flag":
				meta["mcpApps"] = nil
			case "missing-flag":
				delete(meta, "mcpApps")
			case "extra":
				meta["apiKey"] = "private-secret-sentinel"
			case "null-error":
				message["error"] = nil
			case "mixed-error":
				message["error"] = map[string]any{"code": -32603, "message": "private-error-sentinel"}
			case "null":
				message["result"] = nil
			}
			raw, _ := json.Marshal(message)
			if change == "duplicate" {
				raw = []byte(strings.Replace(string(raw), `"protocolVersion":1`, `"protocolVersion":1,"protocolVersion":1`, 1))
			}
			if change == "utf8" {
				raw = append(raw, 0xff)
			}
			if validateInitialize(raw, id, "/private/fixture") == nil {
				t.Fatal("changed native facts accepted")
			}
		})
	}
}

func TestInspectionRejectsInheritedState(t *testing.T) {
	for _, change := range []string{"version", "cwd", "project", "instructions", "mcp", "plugins", "skills", "hooks", "lsp", "config", "managed", "loaded", "skipped", "login", "remote", "compat", "agent", "unknown", "null", "trailing"} {
		t.Run(change, func(t *testing.T) {
			report := fixtureObject(inspectionFixture)
			switch change {
			case "version":
				report["grokVersion"] = "1.0.42"
			case "cwd":
				report["cwd"] = "/foreign"
			case "project":
				report["projectRoot"] = "/foreign"
			case "instructions":
				report["projectInstructions"] = []any{"foreign"}
			case "mcp":
				report["mcpServers"] = []any{"foreign"}
			case "plugins", "skills", "hooks":
				report[change] = []any{"foreign"}
			case "lsp":
				report["lspServers"] = []any{"foreign"}
			case "config":
				report["configSources"].(map[string]any)["layers"] = []any{"MDM"}
			case "managed":
				report["permissions"].(map[string]any)["managedSettingsExists"] = true
			case "loaded":
				report["permissions"].(map[string]any)["loaded"] = 1
			case "skipped":
				report["permissions"].(map[string]any)["skipped"] = []any{"unreadable"}
			case "login":
				report["loginPolicy"].(map[string]any)["forceLoginTeamUuid"] = "foreign"
			case "remote":
				report["externalCompat"].(map[string]any)["remoteSettingsLoaded"] = true
			case "compat":
				report["externalCompat"].(map[string]any)["cells"].([]any)[0].(map[string]any)["enabled"] = true
			case "agent":
				report["agents"].([]any)[0].(map[string]any)["source"].(map[string]any)["type"] = "user"
			case "unknown":
				report["future"] = true
			case "null":
				report["projectTrusted"] = nil
			}
			raw, _ := json.Marshal(report)
			if change == "trailing" {
				raw = append(raw, []byte(` {}`)...)
			}
			if validateInspection(raw, "/private/fixture") == nil {
				t.Fatal("inherited native state accepted")
			}
		})
	}
}
