package runmoor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type SecretRef struct {
	Env  string `toml:"env" json:"env,omitempty"`
	File string `toml:"file" json:"file,omitempty"`
}
type Connection struct {
	Name           string    `toml:"name" json:"name"`
	Target         string    `toml:"target" json:"target"`
	Auth           AuthKind  `toml:"auth" json:"auth"`
	Credential     SecretRef `toml:"credential" json:"credential"`
	ClientID       string    `toml:"client_id" json:"client_id,omitempty"`
	InstallationID int64     `toml:"installation_id" json:"installation_id,omitempty"`
}
type Pool struct {
	Name            string    `toml:"name" json:"name"`
	Connection      string    `toml:"connection" json:"connection"`
	ScaleSet        string    `toml:"scale_set" json:"scale_set"`
	RunnerGroup     string    `toml:"runner_group" json:"runner_group,omitempty"`
	Labels          []string  `toml:"labels" json:"labels"`
	Backend         Backend   `toml:"backend" json:"backend"`
	Mode            Mode      `toml:"mode" json:"mode"`
	Arch            string    `toml:"arch" json:"arch"`
	Image           string    `toml:"image" json:"image"`
	RunnerPath      string    `toml:"runner_path" json:"runner_path"`
	RunnerVersion   string    `toml:"runner_version" json:"runner_version"`
	MinIdle         int       `toml:"min_idle" json:"min_idle"`
	MaxRunners      int       `toml:"max_runners" json:"max_runners"`
	Resources       Resources `toml:"resources" json:"resources"`
	DaemonResources Resources `toml:"daemon_resources" json:"daemon_resources"`
	DaemonImage     string    `toml:"daemon_image" json:"daemon_image,omitempty"`
}
type Storage struct {
	State string `toml:"state" json:"state"`
	Data  string `toml:"data" json:"data"`
}
type Budget struct {
	MaxRunners     int   `toml:"max_runners" json:"max_runners"`
	CPU            int   `toml:"cpu" json:"cpu"`
	MemoryMiB      int64 `toml:"memory_mib" json:"memory_mib"`
	MinFreeDiskMiB int64 `toml:"min_free_disk_mib" json:"min_free_disk_mib"`
}
type Timeouts struct {
	DockerPreparation string `toml:"docker_preparation" json:"docker_preparation"`
	TartPreparation   string `toml:"tart_preparation" json:"tart_preparation"`
	Job               string `toml:"job" json:"job"`
}
type Logging struct {
	Format  string `toml:"format" json:"format"`
	Level   string `toml:"level" json:"level"`
	NoColor bool   `toml:"no_color" json:"no_color"`
}
type Config struct {
	SchemaVersion  int          `toml:"schema_version" json:"schema_version"`
	Storage        Storage      `toml:"storage" json:"storage"`
	Host           Budget       `toml:"host" json:"host"`
	Timeouts       Timeouts     `toml:"timeouts" json:"timeouts"`
	Logging        Logging      `toml:"logging" json:"logging"`
	DockerSocket   string       `toml:"docker_socket" json:"docker_socket"`
	TartExecutable string       `toml:"tart_executable" json:"tart_executable"`
	Connections    []Connection `toml:"connections" json:"connections"`
	Pools          []Pool       `toml:"pools" json:"pools"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var targetPattern = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9][A-Za-z0-9_.-]*(/[A-Za-z0-9][A-Za-z0-9_.-]*)?$`)
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func xdg(name, suffix string) string {
	if v := os.Getenv(name); filepath.IsAbs(v) {
		return filepath.Join(v, "runmoor")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, suffix, "runmoor")
}
func DefaultConfigPath() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "config.toml")
}
func LoadConfig(path string) (Config, error) {
	var c Config
	data, err := readPrivate(path, 1<<20)
	if err != nil {
		return c, err
	}
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return c, problem(ErrConfig, "Cannot decode TOML schema v1.", "Check the TOML reference; remove unknown fields and literal credentials.")
	}
	return NormalizeConfig(c)
}
func NormalizeConfig(c Config) (Config, error) {
	fail := func(s string) (Config, error) {
		return c, problem(ErrConfig, s, "Edit the configuration and run 'runmoor config validate'.")
	}
	if c.SchemaVersion != 1 {
		return fail("Only TOML schema_version = 1 is supported.")
	}
	if c.Storage.State == "" {
		c.Storage.State = xdg("XDG_STATE_HOME", ".local/state")
	}
	if c.Storage.Data == "" {
		c.Storage.Data = xdg("XDG_DATA_HOME", ".local/share")
	}
	for _, p := range []string{c.Storage.State, c.Storage.Data} {
		if !filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\n\r") {
			return fail("Storage paths must be absolute paths.")
		}
	}
	c.Storage.State = filepath.Clean(c.Storage.State)
	c.Storage.Data = filepath.Clean(c.Storage.Data)
	if len(filepath.Join(c.Storage.State, "control.sock")) > 100 {
		return fail("The control socket path exceeds the portable Unix socket limit; choose a shorter state path.")
	}
	if c.Timeouts.DockerPreparation == "" {
		c.Timeouts.DockerPreparation = "5m"
	}
	if c.Timeouts.TartPreparation == "" {
		c.Timeouts.TartPreparation = "10m"
	}
	if c.Timeouts.Job == "" {
		c.Timeouts.Job = "6h"
	}
	for _, s := range []string{c.Timeouts.DockerPreparation, c.Timeouts.TartPreparation, c.Timeouts.Job} {
		d, e := time.ParseDuration(s)
		if e != nil || d <= 0 || d > 7*24*time.Hour {
			return fail("Timeouts must be positive durations no longer than seven days.")
		}
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "text"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format != "text" && c.Logging.Format != "json" {
		return fail("Logging format must be text or json.")
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fail("Unknown logging level.")
	}
	if c.TartExecutable == "" {
		c.TartExecutable = "tart"
	}
	if c.DockerSocket != "" && (!strings.HasPrefix(c.DockerSocket, "unix:///") || strings.ContainsAny(c.DockerSocket, "\x00\n\r")) {
		return fail("Docker must use a local unix:/// socket; remote engines are unsupported.")
	}
	if c.Host.MaxRunners <= 0 || c.Host.MaxRunners > 10000 || c.Host.CPU <= 0 || c.Host.CPU > 100000 || c.Host.MemoryMiB <= 0 || c.Host.MemoryMiB > 1<<40 || c.Host.MinFreeDiskMiB <= 0 || c.Host.MinFreeDiskMiB > 1<<40 {
		return fail("Explicit positive host concurrency, CPU, memory and minimum free disk budgets are required.")
	}
	connections := map[string]Connection{}
	for i := range c.Connections {
		v := &c.Connections[i]
		v.Target = strings.TrimSuffix(v.Target, "/")
		if !safeName.MatchString(v.Name) || !targetPattern.MatchString(v.Target) || strings.Contains(v.Target, "/../") {
			return fail("Connection names and GitHub.com repository/organization targets must be valid.")
		}
		if _, ok := connections[v.Name]; ok {
			return fail("Connection names must be unique.")
		}
		if v.Auth != PAT && v.Auth != App {
			return fail("Authentication must be pat or app.")
		}
		if (v.Credential.Env == "") == (v.Credential.File == "") {
			return fail("Each credential requires exactly one env or file reference.")
		}
		if v.Credential.Env != "" && !envName.MatchString(v.Credential.Env) {
			return fail("Credential environment names are invalid.")
		}
		if v.Credential.File != "" && !filepath.IsAbs(v.Credential.File) {
			return fail("Credential files require absolute paths.")
		}
		if v.Auth == App && (v.ClientID == "" || v.InstallationID <= 0) {
			return fail("App authentication requires client_id and installation_id.")
		}
		if v.Auth == PAT && (v.ClientID != "" || v.InstallationID != 0) {
			return fail("PAT authentication cannot include App settings.")
		}
		connections[v.Name] = *v
	}
	names := map[string]bool{}
	identities := map[string]bool{}
	idle := Resources{}
	idleCount, vmCount := 0, 0
	for i := range c.Pools {
		p := &c.Pools[i]
		conn, ok := connections[p.Connection]
		if !ok || !safeName.MatchString(p.Name) || names[p.Name] || !safeName.MatchString(p.ScaleSet) {
			return fail("Pools require unique names, valid scale-set names and an existing connection.")
		}
		names[p.Name] = true
		identity := poolIdentity(*p, conn)
		if identities[identity] {
			return fail("Duplicate target, runner group and scale-set identity.")
		}
		identities[identity] = true
		if p.RunnerGroup != "" && strings.Count(strings.TrimPrefix(conn.Target, "https://github.com/"), "/") != 0 {
			return fail("Runner groups are available only for organization registration.")
		}
		if p.RunnerGroup != "" && !safeName.MatchString(p.RunnerGroup) {
			return fail("Runner group names must use letters, digits, dots, underscores or hyphens.")
		}
		if p.Arch != "amd64" && p.Arch != "arm64" {
			return fail("Pool arch must be amd64 or arm64.")
		}
		if p.Arch != runtime.GOARCH {
			return fail("CPU emulation is unsupported; pool architecture must match the host.")
		}
		if p.Mode == "" {
			p.Mode = Plain
		}
		if p.Mode != Plain && p.Mode != DinD {
			return fail("Execution mode must be plain or dind.")
		}
		if p.Backend != Docker && p.Backend != Tart {
			return fail("Backend must be docker or tart.")
		}
		if p.Backend == Tart && (runtime.GOOS != "darwin" || p.Arch != "arm64" || p.Mode != Plain) {
			return fail("Tart requires macOS arm64 and plain execution mode.")
		}
		if p.RunnerPath == "" {
			p.RunnerPath = "/home/runner"
			if p.Backend == Tart {
				p.RunnerPath = "/Users/runner/actions-runner"
			}
		}
		if !strings.HasPrefix(p.RunnerPath, "/") || strings.ContainsAny(p.RunnerPath, "\x00\n\r") || strings.Contains(p.RunnerPath, "..") {
			return fail("runner_path must be an absolute clean guest path.")
		}
		if !versionPattern.MatchString(p.RunnerVersion) {
			return fail("Every pool must pin an exact runner_version.")
		}
		if p.Backend == Docker && !imagePattern.MatchString(p.Image) {
			return fail("Docker images require immutable sha256 digests.")
		}
		if p.Backend == Tart && !validID(p.Image) {
			return fail("Tart pools require a sealed image revision UUID-v7.")
		}
		if p.Mode == DinD {
			if !imagePattern.MatchString(p.DaemonImage) || !validResources(p.DaemonResources) {
				return fail("Docker-in-Docker requires a digest-pinned daemon image and explicit daemon resources.")
			}
		} else if p.DaemonImage != "" || p.DaemonResources != (Resources{}) {
			return fail("Daemon settings require dind mode.")
		}
		if !validResources(p.Resources) || p.MinIdle < 0 || p.MaxRunners <= 0 || p.MaxRunners > 10000 || p.MinIdle > p.MaxRunners {
			return fail("Pool resource and runner limits are invalid.")
		}
		cost := p.Cost()
		if cost.CPU > c.Host.CPU || cost.MemoryMiB > c.Host.MemoryMiB {
			return fail("A runner and its daemon must fit the host resource budget.")
		}
		labels := map[string]bool{}
		for _, l := range p.Labels {
			if !safeName.MatchString(l) || labels[strings.ToLower(l)] || strings.HasPrefix(l, "runmoor-owner-") {
				return fail("Routing labels must be unique safe names outside the reserved runmoor-owner- prefix.")
			}
			labels[strings.ToLower(l)] = true
		}
		idle.CPU += cost.CPU * p.MinIdle
		idle.MemoryMiB += cost.MemoryMiB * int64(p.MinIdle)
		idleCount += p.MinIdle
		if p.Backend == Tart {
			vmCount += p.MinIdle
		}
	}
	if idle.CPU > c.Host.CPU || idle.MemoryMiB > c.Host.MemoryMiB || idleCount > c.Host.MaxRunners || vmCount > 2 {
		return fail("Minimum idle allocations cannot fit the configured host budget or two-VM limit.")
	}
	return c, nil
}
func validResources(r Resources) bool {
	return r.CPU > 0 && r.CPU <= 100000 && r.MemoryMiB > 0 && r.MemoryMiB <= 1<<40
}
func validID(s string) bool    { u, e := uuidParse(s); return e == nil && u == s }
func (p Pool) Cost() Resources { return p.Resources.Add(p.DaemonResources) }
func poolIdentity(p Pool, c Connection) string {
	g := p.RunnerGroup
	if g == "" {
		g = "default"
	}
	return strings.ToLower(c.Target + "|" + g + "|" + p.ScaleSet)
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (c Config) Connection(name string) Connection {
	for _, v := range c.Connections {
		if v.Name == name {
			return v
		}
	}
	return Connection{}
}
func (c Config) Preparation(b Backend) time.Duration {
	s := c.Timeouts.DockerPreparation
	if b == Tart {
		s = c.Timeouts.TartPreparation
	}
	d, _ := time.ParseDuration(s)
	return d
}
func (c Config) JobTimeout() time.Duration { d, _ := time.ParseDuration(c.Timeouts.Job); return d }
func ResolveSecret(ref SecretRef) (string, error) {
	var b []byte
	if ref.File != "" {
		v, err := readPrivate(ref.File, 64<<10)
		if err != nil {
			return "", problem(ErrAuth, "Credential file cannot be read securely.", "Use an owner-only regular file and resume the affected pool.")
		}
		b = v
	} else {
		b = []byte(os.Getenv(ref.Env))
	}
	if len(b) == 0 || len(b) > 64<<10 {
		return "", problem(ErrAuth, "Credential reference is empty or exceeds 64 KiB.", "Set the referenced environment variable or protected file, then resume the pool.")
	}
	return strings.TrimSpace(string(b)), nil
}
func InitConfig(path string) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return problem(ErrConfig, "Cannot create configuration without overwriting an existing file.", "Choose a new --config path or edit the existing file.")
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, configSkeleton, runtime.GOARCH)
	return err
}

const configSkeleton = `# Runmoor preview. Fill every REQUIRED value before running.
# Credentials are references only. File references require owner-only permissions.
schema_version = 1
# docker_socket = "unix:///var/run/docker.sock"
# tart_executable = "tart"

[storage]
# Defaults follow XDG_CONFIG_HOME, XDG_STATE_HOME and XDG_DATA_HOME on both OSes.
# state = "/absolute/private/state"
# data = "/absolute/private/data"

[host]
max_runners = 0 # REQUIRED
cpu = 0 # REQUIRED: total CPU cores available to all runner and daemon containers
memory_mib = 0 # REQUIRED
min_free_disk_mib = 0 # REQUIRED

[timeouts]
docker_preparation = "5m"
tart_preparation = "10m"
job = "6h"

[logging]
format = "text" # text or json
level = "info"
no_color = false # --no-color or NO_COLOR also disables ANSI output

[[connections]]
name = "personal"
target = "https://github.com/OWNER/REPOSITORY" # organization: https://github.com/OWNER
auth = "pat" # app additionally requires client_id and installation_id
credential = { env = "RUNMOOR_PAT" } # or { file = "/absolute/private/credential" }

[[pools]]
name = "linux"
connection = "personal"
scale_set = "runmoor-linux"
labels = ["runmoor-linux"]
backend = "docker"
mode = "plain" # dind requires daemon_image and daemon_resources
arch = "%s"
image = "REQUIRED_IMAGE_AT_SHA256_DIGEST"
runner_version = "2.337.0" # verify current support and replace images explicitly
runner_path = "/home/runner"
min_idle = 0
max_runners = 1
resources = { cpu = 0, memory_mib = 0 } # REQUIRED
# daemon_image = "docker@sha256:REQUIRED_DIGEST"
# daemon_resources = { cpu = 1, memory_mib = 1024 }
`
