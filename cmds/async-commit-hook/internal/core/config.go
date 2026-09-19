package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type EnvironmentInput struct {
	Name       string `toml:"name" json:"name"`
	Secret     bool   `toml:"secret" json:"secret"`
	Credential string `toml:"credential" json:"credential,omitempty"`
	Required   bool   `toml:"required" json:"required"`
}
type Report struct {
	Kind ReportKind `toml:"kind" json:"kind"`
	Path string     `toml:"path" json:"path"`
}
type Command struct {
	Command     string             `toml:"command" json:"command"`
	DependsOn   []string           `toml:"depends_on" json:"depends_on"`
	OS          []string           `toml:"os" json:"os"`
	Shell       Shell              `toml:"shell" json:"shell"`
	Optional    bool               `toml:"optional" json:"optional"`
	Policy      Policy             `toml:"policy" json:"policy"`
	Group       string             `toml:"group" json:"group,omitempty"`
	Environment []EnvironmentInput `toml:"environment" json:"environment"`
	Reports     []Report           `toml:"reports" json:"reports"`
}
type Project struct {
	Version  int                `toml:"version" json:"version"`
	DiffBase string             `toml:"diff_base" json:"diff_base,omitempty"`
	PrePush  PushPolicy         `toml:"pre_push" json:"pre_push"`
	Checks   map[string]Command `toml:"checks" json:"checks"`
}
type Credential struct {
	Env  string `toml:"env" json:"env,omitempty"`
	File string `toml:"file" json:"file,omitempty"`
}

// Keep day-to-duration conversion within the signed nanosecond range.
const maxRetentionAgeDays = int((1<<63 - 1) / int64(24*time.Hour))

type Retention struct {
	MaxAgeDays int   `toml:"max_age_days" json:"max_age_days"`
	MaxBytes   int64 `toml:"max_bytes" json:"max_bytes"`
}
type Personal struct {
	Version     int                   `toml:"version"`
	Mode        Mode                  `toml:"mode"`
	APIPort     int                   `toml:"api_port"`
	StateDir    string                `toml:"state_dir"`
	Credentials map[string]Credential `toml:"credentials"`
	Retention   Retention             `toml:"retention"`
}
type Paths struct {
	Config  string
	State   string
	Control string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	config := os.Getenv("ACH_CONFIG")
	if config == "" {
		config = filepath.Join(home, ".config", "async-commit-hook", "config.toml")
	}
	config, err = filepath.Abs(config)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Config: config, State: filepath.Join(home, ".local", "share", "async-commit-hook"), Control: filepath.Join(home, ".config", "async-commit-hook", "control")}, nil
}
func ReadPersonal(paths Paths) (Personal, error) {
	p := Personal{Version: 1, Mode: Daemon, APIPort: 46309, StateDir: paths.State, Credentials: map[string]Credential{}}
	b, err := os.ReadFile(paths.Config)
	if err != nil && !os.IsNotExist(err) {
		return p, Wrap("config-read", err)
	}
	if err == nil {
		if err = toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields().Decode(&p); err != nil {
			return p, E("invalid-config", err.Error(), 2)
		}
	}
	if p.Version != 1 || (p.Mode != Daemon && p.Mode != OnDemand) || p.APIPort < 1024 || p.APIPort > 65535 || p.Retention.MaxAgeDays < 0 || p.Retention.MaxAgeDays > maxRetentionAgeDays || p.Retention.MaxBytes < 0 {
		return p, E("invalid-config", fmt.Sprintf("version must be 1, mode daemon/on-demand, port 1024..65535, retention age 0..%d days and bytes nonnegative", maxRetentionAgeDays), 2)
	}
	if strings.HasPrefix(p.StateDir, "~/") {
		home, _ := os.UserHomeDir()
		p.StateDir = filepath.Join(home, p.StateDir[2:])
	}
	if !filepath.IsAbs(p.StateDir) {
		return p, E("invalid-config", "state_dir must be absolute", 2)
	}
	for name, c := range p.Credentials {
		if (c.Env == "") == (c.File == "") {
			return p, E("invalid-credential", "credential "+name+" requires exactly one of env or file", 2)
		}
		if c.File != "" && !filepath.IsAbs(c.File) {
			return p, E("invalid-credential", "credential file must be absolute", 2)
		}
	}
	return p, nil
}

var checkName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const maxProjectConfigBytes = 1024 * 1024

func ParseProject(b []byte) (Project, error) {
	p := Project{}
	if len(b) > maxProjectConfigBytes {
		return p, E("invalid-config", "project configuration exceeds 1 MiB", 2)
	}
	if err := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields().Decode(&p); err != nil {
		return p, E("invalid-config", err.Error(), 2)
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}
func (p *Project) Validate() error {
	if p.Version != 1 {
		return E("unsupported-version", "project version must be 1", 2)
	}
	if p.PrePush == "" {
		p.PrePush = PushBlock
	}
	if p.PrePush != PushBlock && p.PrePush != PushWait && p.PrePush != PushRun {
		return E("invalid-config", "pre_push must be block, wait, or run-and-wait", 2)
	}
	reportOwners := map[string]string{}
	environmentSecrecy := map[string]bool{}
	for name, c := range p.Checks {
		if !checkName.MatchString(name) || strings.TrimSpace(c.Command) == "" || strings.ContainsRune(c.Command, 0) {
			return E("invalid-check", "invalid name or empty/NUL command: "+name, 2)
		}
		if c.Policy == "" {
			c.Policy = Parallel
		}
		if c.Policy != Parallel && c.Policy != Queue && c.Policy != Replace {
			return E("invalid-policy", name+": use parallel, queue or replace", 2)
		}
		if len(c.Group) > 128 || strings.ContainsAny(c.Group, "\x00\r\n") {
			return E("invalid-group", name, 2)
		}
		if c.Shell != "" && c.Shell != Sh && c.Shell != Bash && c.Shell != PowerShell && c.Shell != Pwsh {
			return E("invalid-shell", name, 2)
		}
		seen := map[string]bool{}
		for _, v := range c.OS {
			if (v != "darwin" && v != "linux" && v != "windows") || seen[v] {
				return E("invalid-os", name, 2)
			}
			seen[v] = true
		}
		seen = map[string]bool{}
		for _, d := range c.DependsOn {
			if _, ok := p.Checks[d]; !ok || d == name || seen[d] {
				return E("invalid-dependency", name+": "+d, 2)
			}
			seen[d] = true
		}
		seen = map[string]bool{}
		for _, e := range c.Environment {
			if !envName.MatchString(e.Name) || seen[e.Name] || (e.Credential != "" && !e.Secret) {
				return E("invalid-environment", name+": invalid/duplicate name or non-secret credential", 2)
			}
			// Environment names are case-insensitive on Windows. Enforce one
			// secrecy classification across the portable project graph before
			// any public input can be read into a durable run snapshot.
			key := strings.ToUpper(e.Name)
			if secret, exists := environmentSecrecy[key]; exists && secret != e.Secret {
				return E("invalid-environment", "environment input "+e.Name+" has conflicting secret/public declarations", 2)
			}
			environmentSecrecy[key] = e.Secret
			seen[e.Name] = true
		}
		seen = map[string]bool{}
		for _, r := range c.Reports {
			if (r.Kind != JUnit && r.Kind != GoTest) || !SafeRelative(r.Path) || seen[r.Path] {
				return E("invalid-report", name+": reports require unique workspace-relative file paths and supported kinds", 2)
			}
			key := strings.ToLower(r.Path)
			if owner, exists := reportOwners[key]; exists {
				return E("invalid-report", name+": report output is already owned by check "+owner, 2)
			}
			reportOwners[key] = name
			seen[r.Path] = true
		}
		p.Checks[name] = c
	}
	for name, c := range p.Checks {
		for _, dependency := range c.DependsOn {
			for _, platform := range []string{"darwin", "linux", "windows"} {
				if c.Applies(platform) && !p.Checks[dependency].Applies(platform) {
					return E("invalid-dependency", name+": prerequisite "+dependency+" does not apply on "+platform, 2)
				}
			}
		}
	}
	_, err := p.Order()
	return err
}
func SafeRelative(p string) bool {
	return p != "" && !filepath.IsAbs(p) && !strings.ContainsAny(p, "\\\x00") && p != "." && p != ".." && !strings.HasPrefix(p, "../") && filepath.ToSlash(filepath.Clean(p)) == p && !strings.Contains(p, ":")
}
func (p Project) Order() ([]string, error) {
	names := make([]string, 0, len(p.Checks))
	for n := range p.Checks {
		names = append(names, n)
	}
	sort.Strings(names)
	states := map[string]int{}
	order := []string{}
	var visit func(string) error
	visit = func(n string) error {
		if states[n] == 1 {
			return E("dependency-cycle", "cycle at "+n, 2)
		}
		if states[n] == 2 {
			return nil
		}
		states[n] = 1
		for _, d := range p.Checks[n].DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		states[n] = 2
		order = append(order, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}
func (c Command) Applies(osName string) bool {
	if len(c.OS) == 0 {
		return true
	}
	for _, v := range c.OS {
		if v == osName {
			return true
		}
	}
	return false
}
func (c Command) SelectedShell(osName string) Shell {
	if c.Shell != "" {
		return c.Shell
	}
	if osName == "windows" {
		return PowerShell
	}
	return Sh
}
func Hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func Fingerprint(p Project, env map[string]string) string {
	return Hash(Encode(struct {
		Project     Project
		Environment map[string]string
		OS, Arch    string
	}{p, env, runtime.GOOS, runtime.GOARCH}))
}
func PublicEnvironment(p Project) (map[string]string, error) {
	values := map[string]string{}
	for _, c := range p.Checks {
		if !c.Applies(runtime.GOOS) {
			continue
		}
		for _, e := range c.Environment {
			if e.Secret {
				continue
			}
			value, ok := os.LookupEnv(e.Name)
			if !ok && e.Required {
				return nil, E("environment-missing", "required environment variable "+e.Name+" is unavailable", 2)
			}
			if ok {
				values[e.Name] = value
			}
		}
	}
	return values, nil
}
func (p Personal) CommandEnvironment(c Command, public map[string]string) ([]string, []string, error) {
	env := SystemEnvironment()
	secrets := []string{}
	for _, e := range c.Environment {
		value, ok := public[e.Name]
		if e.Secret {
			if e.Credential != "" {
				ref, found := p.Credentials[e.Credential]
				if !found {
					return nil, nil, E("credential-missing", "configure credential reference "+e.Credential, 2)
				}
				if ref.Env != "" {
					value, ok = os.LookupEnv(ref.Env)
				} else {
					b, err := os.ReadFile(ref.File)
					if err != nil {
						return nil, nil, E("credential-unavailable", "cannot read credential reference "+e.Credential, 3)
					}
					value = string(b)
					ok = true
				}
			} else {
				value, ok = os.LookupEnv(e.Name)
			}
		}
		if !ok && e.Required {
			return nil, nil, E("environment-missing", "required input "+e.Name+" is unavailable; restore its local reference and rerun", 2)
		}
		if ok {
			if strings.ContainsRune(value, 0) {
				return nil, nil, E("invalid-environment", "NUL in declared input "+e.Name, 2)
			}
			env[e.Name] = value
			if e.Secret && value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(result)
	return result, secrets, nil
}
func SystemEnvironment() map[string]string {
	out := map[string]string{}
	for _, k := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "SystemRoot", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "LANG", "LC_ALL"} {
		if v, ok := os.LookupEnv(k); ok {
			out[k] = v
		}
	}
	return out
}

const DefaultProject = `version = 1
pre_push = "block"

# Define commands before committing. No applicable checks is not validation.
# [checks.test]
# command = "go test ./..."
# policy = "parallel"
# [[checks.test.reports]]
# kind = "go-test-json"
# path = "test-results.json"
`
