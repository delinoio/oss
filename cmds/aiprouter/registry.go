// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ClientSpec struct {
	ID        string
	Format    Format
	Loopback  bool
	Platforms string
}

var clients = []ClientSpec{
	{"qoder", JSON, true, "client installation required"}, {"qoder-cn", JSON, true, "client installation required"}, {"workbuddy", JSON, true, "client installation required"}, {"workbuddy-ai", JSON, true, "client installation required"},
	{"opencode", JSON, false, "linux,darwin,windows"}, {"pi", JSON, true, "linux,darwin,windows"}, {"omp", YAML, true, "client installation required"}, {"hermes", YAML, false, "client installation required"}, {"openclaw", JSON5, false, "client installation required"}, {"kimi", TOML, true, "client installation required"}, {"gajae", YAML, true, "client installation required"}, {"dsh", YAML, true, "client installation required"}, {"mcode", YAML, true, "client installation required"}, {"zcode", JSON, true, "client installation required"}, {"prime", JSON, true, "client installation required"}, {"aside", JSON, true, "client installation required"}, {"raycast", YAML, true, "darwin,windows"}, {"omo", JSON, true, "client installation required"}, {"cline", JSON, true, "client installation required; stop Cline during changes"}, {"commandcode", JSON, true, "client installation required"}, {"kilo", JSON5, false, "client installation required"}, {"droid", JSON, true, "client installation required"}, {"gentle-shell", JSON, true, "client installation required"},
	{"codex", TOML, false, "linux,darwin,windows"}, {"claude", JSON, false, "client installation required"}, {"grok", TOML, true, "client installation required"}, {"claude-desktop", JSON, false, "darwin,windows"},
}

func clientSpec(id string) (ClientSpec, error) {
	for _, c := range clients {
		if c.ID == id {
			return c, nil
		}
	}
	return ClientSpec{}, errors.New("unknown client; run aiprouter clients")
}

type Resolver struct {
	Home, OS string
	Env      map[string]string
}

func (r Resolver) envHome(key, fallback string) (string, error) {
	v := strings.TrimSpace(r.Env[key])
	if v == "" {
		if filepath.IsAbs(fallback) {
			return filepath.Clean(fallback), nil
		}
		return filepath.Join(r.Home, fallback), nil
	}
	if v == "~" {
		return r.Home, nil
	}
	if strings.HasPrefix(v, "~/") || strings.HasPrefix(v, `~\`) {
		return filepath.Join(r.Home, v[2:]), nil
	}
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("%s must be an absolute path", key)
	}
	return filepath.Clean(v), nil
}
func exists(p string) bool { _, e := os.Lstat(p); return e == nil }
func (r Resolver) Resolve(id string) (string, error) {
	home := r.Home
	join := filepath.Join
	root := ""
	file := ""
	var err error
	switch id {
	case "qoder":
		root, err = r.envHome("QODER_CONFIG_DIR", ".qoder")
		file = "settings.json"
	case "qoder-cn":
		root, err = r.envHome("QODERCN_CONFIG_DIR", ".qoder-cn")
		file = "settings.json"
	case "workbuddy", "workbuddy-ai":
		key := "WORKBUDDY_CONFIG_DIR"
		if strings.TrimSpace(r.Env[key]) == "" {
			key = "CODEBUDDY_CONFIG_DIR"
		}
		root, err = r.envHome(key, "."+id)
		file = "models.json"
	case "opencode", "kilo":
		root, err = r.envHome("XDG_CONFIG_HOME", ".config")
		root = join(root, id)
		file = "opencode.json"
		if id == "kilo" {
			file = "kilo.jsonc"
			for _, f := range kiloCandidates {
				if exists(join(root, f)) {
					file = f
					break
				}
			}
		}
	case "pi":
		root, err = r.envHome("PI_CODING_AGENT_DIR", ".pi/agent")
		file = "models.json"
	case "prime":
		root, err = r.envHome("PRIME_AGENT_CODING_AGENT_DIR", ".prime/agent")
		file = "models.json"
	case "omo":
		key := "OMO_CODING_AGENT_DIR"
		if r.Env[key] == "" {
			key = "SENPI_CODING_AGENT_DIR"
		}
		if r.Env[key] == "" {
			key = "PI_CODING_AGENT_DIR"
		}
		root, err = r.envHome(key, ".omo/agent")
		file = "models.json"
	case "omp":
		profile, ok := r.Env["OMP_PROFILE"]
		if !ok {
			profile = r.Env["PI_PROFILE"]
		}
		profile = strings.TrimSpace(profile)
		if profile == "" || profile == "default" {
			root, err = r.envHome("PI_CODING_AGENT_DIR", join(".omp", "agent"))
		}
		config := r.Env["PI_CONFIG_DIR"]
		if config == "" {
			config = ".omp"
		}
		if profile != "" && profile != "default" {
			if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`).MatchString(profile) || strings.HasSuffix(profile, ".") || regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\.|$)`).MatchString(profile) {
				return "", errors.New("invalid OMP profile")
			}
			root = join(home, config, "profiles", profile, "agent")
		} else if r.Env["PI_CODING_AGENT_DIR"] == "" {
			root = join(home, config, "agent")
		}
		file = "models.yml"
		if !exists(join(root, file)) && exists(join(root, "models.yaml")) {
			file = "models.yaml"
		}
	case "hermes":
		fallback := ".hermes"
		if r.OS == "windows" {
			local := r.Env["LOCALAPPDATA"]
			if local == "" {
				local = join(home, "AppData", "Local")
			}
			fallback = join(local, "hermes")
		}
		root, err = r.envHome("HERMES_HOME", fallback)
		file = "config.yaml"
	case "openclaw":
		effective, e := r.envHome("OPENCLAW_HOME", "")
		if e != nil {
			return "", e
		}
		if r.Env["OPENCLAW_CONFIG_PATH"] != "" {
			return r.envHome("OPENCLAW_CONFIG_PATH", "")
		}
		if r.Env["OPENCLAW_STATE_DIR"] != "" {
			root, err = r.envHome("OPENCLAW_STATE_DIR", "")
		} else if profile := strings.TrimSpace(r.Env["OPENCLAW_PROFILE"]); profile != "" && !strings.EqualFold(profile, "default") {
			if strings.ContainsAny(profile, `/\`) || profile == ".." {
				return "", errors.New("invalid OpenClaw profile")
			}
			root = join(effective, ".openclaw-"+profile)
		} else {
			for _, dir := range []string{".openclaw", ".clawdbot"} {
				for _, f := range []string{"openclaw.json", "clawdbot.json"} {
					if exists(join(effective, dir, f)) {
						return join(effective, dir, f), nil
					}
				}
			}
			root = join(effective, ".openclaw")
		}
		file = "openclaw.json"
		if !exists(join(root, file)) && exists(join(root, "clawdbot.json")) {
			file = "clawdbot.json"
		}
	case "kimi":
		root, err = r.envHome("KIMI_CODE_HOME", ".kimi-code")
		file = "config.toml"
	case "gajae":
		root = join(home, ".gjc", "agent")
		file = "models.yml"
	case "dsh":
		root, err = r.envHome("DSH_HOME", ".dsh")
		file = "settings.yaml"
		patch := join(root, "profiles", "desktop", "cordis.patch.yml")
		if exists(patch) {
			return patch, nil
		}
		if exists(join(filepath.Dir(patch), "package.json")) {
			return "", errors.New("DSH active profile patch is missing; restore it in DSH before configuring")
		}
	case "mcode":
		key := "MINIMAX_DATA_DIR"
		if r.Env[key] == "" {
			key = "MAVIS_DATA_DIR"
		}
		root, err = r.envHome(key, ".minimax")
		file = "config.yaml"
	case "zcode":
		root, err = r.envHome("ZCODE_DATA_DIR", ".zcode")
		if r.Env["ZCODE_DATA_DIR"] == "" {
			b, ok, e := readConfig(join(home, ".zcode", "v2", "setting.json"))
			if e != nil {
				return "", e
			}
			if ok {
				var v map[string]any
				if json.Unmarshal(b, &v) != nil {
					return "", errors.New("ZCode bootstrap is invalid")
				}
				if base, ok := v["dataBaseDir"]; ok {
					str, ok := base.(string)
					if !ok || !filepath.IsAbs(str) {
						return "", errors.New("ZCode active data root is unknown")
					}
					root = join(str, ".zcode")
				}
			}
		}
		if r.Env["ZCODE_PERSONAL_PROVIDER_CONFIG_FILE"] != "" {
			return r.envHome("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE", "")
		}
		file = join("v2", "config.json")
		if exists(join(root, "v2", "provider_config.json")) {
			file = join("v2", "provider_config.json")
		}
	case "aside":
		root, err = asideRoot(home)
		if err != nil {
			return "", err
		}
		b, ok, e := readConfig(join(root, "accounts.json"))
		if e != nil || !ok {
			return "", errors.New("launch and sign into Aside before configuring")
		}
		var v struct {
			Current *int `json:"currentAccountId"`
		}
		if json.Unmarshal(b, &v) != nil || v.Current == nil || *v.Current < 0 {
			return "", errors.New("Aside active account is unknown")
		}
		var manifest map[string]json.RawMessage
		_ = json.Unmarshal(b, &manifest)
		if accounts, ok := manifest["accounts"]; ok {
			var rows []struct {
				ID      *int  `json:"id"`
				Current *bool `json:"current"`
			}
			if json.Unmarshal(accounts, &rows) != nil || len(rows) < 1 || len(rows) > 128 {
				return "", errors.New("invalid Aside account registry")
			}
			seen := map[int]bool{}
			for _, row := range rows {
				if row.ID == nil || *row.ID < 0 || seen[*row.ID] || (row.Current != nil && *row.Current != (*row.ID == *v.Current)) {
					return "", errors.New("invalid or duplicate Aside account")
				}
				seen[*row.ID] = true
			}
			if !seen[*v.Current] {
				return "", errors.New("Aside current account is not registered")
			}
		}
		root = join(root, "u", fmt.Sprint(*v.Current))
		file = "models.json"
	case "raycast":
		if r.OS != "darwin" && r.OS != "windows" {
			return "", errors.New("Raycast integration requires macOS or Windows")
		}
		root = join(home, ".config", "raycast", "ai")
		file = "providers.yaml"
	case "cline":
		if r.Env["CLINE_PROVIDER_SETTINGS_PATH"] != "" {
			path, e := r.envHome("CLINE_PROVIDER_SETTINGS_PATH", "")
			if strings.EqualFold(filepath.Base(path), "models.json") {
				return "", errors.New("Cline provider path must differ from models.json")
			}
			return path, e
		}
		root, err = r.envHome("CLINE_DIR", ".cline")
		root = join(root, "data")
		if r.Env["CLINE_DATA_DIR"] != "" {
			root, err = r.envHome("CLINE_DATA_DIR", "")
		}
		root = join(root, "settings")
		file = "providers.json"
	case "commandcode":
		if r.OS == "windows" && filepath.IsAbs(r.Env["HOME"]) {
			home = r.Env["HOME"]
		}
		root = join(home, ".commandcode")
		file = "providers.json"
	case "droid":
		root = join(home, ".factory")
		file = "settings.json"
	case "gentle-shell":
		bootstrap, e := r.envHome("GENTLE_SHELL_CONFIG", ".gentle-shell/config.json")
		if e != nil {
			return "", e
		}
		root, err = r.envHome("GENTLE_SHELL_HOME", ".gentle-shell/agent")
		b, ok, e := readConfig(bootstrap)
		if e != nil {
			return "", e
		}
		if ok {
			var v struct {
				Home json.RawMessage `json:"home"`
			}
			if json.Unmarshal(b, &v) == nil && len(v.Home) > 0 {
				var selector string
				if json.Unmarshal(v.Home, &selector) == nil {
					if selector == "link" {
						return "", errors.New("Gentle Shell link mode requires the Pi integration")
					}
					if selector != "" && selector != "isolated" {
						if !filepath.IsAbs(selector) {
							return "", errors.New("Gentle Shell saved home must be absolute")
						}
						root = selector
					}
				}
			}
		}
		pi, _ := r.envHome("PI_CODING_AGENT_DIR", ".pi/agent")
		if root == pi || root == join(home, ".pi", "agent") || root == r.Env["GENTLE_SHELL_USER_PI_HOME"] {
			return "", errors.New("Gentle Shell aliases Pi; use the Pi integration")
		}
		file = "models.json"
	case "codex":
		root, err = r.envHome("CODEX_HOME", ".codex")
		file = "config.toml"
	case "claude":
		root, err = r.envHome("CLAUDE_CONFIG_DIR", ".claude")
		file = "settings.json"
	case "grok":
		root, err = r.envHome("GROK_HOME", ".grok")
		file = "config.toml"
	case "claude-desktop":
		if r.OS != "darwin" && r.OS != "windows" {
			return "", errors.New("Claude Desktop requires macOS or Windows")
		}
		root = r.Env["CLAUDE_USER_DATA_DIR"]
		if root == "" {
			root = join(home, "Library", "Application Support", "Claude-3p")
			if r.OS == "windows" {
				local := r.Env["LOCALAPPDATA"]
				if local == "" {
					local = join(home, "AppData", "Local")
				}
				root = join(local, "Claude-3p")
			}
		}
		root = join(root, "configLibrary")
		file = "aiprouter.json"
	default:
		return "", errors.New("unknown client")
	}
	if err != nil {
		return "", err
	}
	path := join(root, file)
	if !filepath.IsAbs(path) {
		return "", errors.New("client path must be absolute")
	}
	return path, nil
}

var kiloCandidates = []string{"kilo.jsonc", "kilo.json", "opencode.jsonc", "opencode.json", "config.json"}
