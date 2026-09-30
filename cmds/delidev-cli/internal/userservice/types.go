// Package userservice controls optional current-user infrastructure registrations.
// Its receipts never grant session execution or replace native cleanup evidence.
package userservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type Kind string
type Action string
type State string

const (
	Server    Kind   = "server"
	Worker    Kind   = "worker"
	Install   Action = "install"
	Start     Action = "start"
	Stop      Action = "stop"
	Remove    Action = "remove"
	Absent    State  = "absent"
	Stopped   State  = "stopped"
	Starting  State  = "starting"
	Running   State  = "running"
	Stopping  State  = "stopping"
	Uncertain State  = "uncertain"
)

func (k Kind) Valid() bool   { return k == Server || k == Worker }
func (a Action) Valid() bool { return a == Install || a == Start || a == Stop || a == Remove }

type ServerOptions struct {
	Listen         string   `json:"listen"`
	TLSCertificate string   `json:"tls_certificate,omitempty"`
	TLSKey         string   `json:"tls_key,omitempty"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
}

type Spec struct {
	Options        ServerOptions `json:"options"`
	Version        int           `json:"version"`
	ID             domain.ID     `json:"id"`
	Kind           Kind          `json:"kind"`
	Platform       string        `json:"platform"`
	User           string        `json:"user"`
	Root           string        `json:"root"`
	RootIdentity   string        `json:"root_identity"`
	Binary         string        `json:"binary"`
	BinaryIdentity string        `json:"binary_identity"`
	BinaryDigest   string        `json:"binary_digest"`
	Name           string        `json:"name"`
	DefinitionPath string        `json:"definition_path"`
}
type Observation struct {
	Present bool
	Enabled bool
	PID     int
}
type Backend interface {
	Inspect(context.Context, Spec) (Observation, error)
	Install(context.Context, Spec) error
	Enable(context.Context, Spec) error
	Start(context.Context, Spec) error
	Disable(context.Context, Spec) error
	Remove(context.Context, Spec) error
}
type Status struct {
	Kind         Kind      `json:"kind"`
	ID           domain.ID `json:"id,omitempty"`
	Revision     uint64    `json:"revision,string"`
	State        State     `json:"state"`
	Desired      State     `json:"desired"`
	LoginEnabled bool      `json:"login_enabled"`
	// Controller completion is independent of retained native session recovery.
	CleanupConfirmed bool `json:"cleanup_confirmed"`
}
type Result struct {
	Status    Status    `json:"service"`
	RequestID domain.ID `json:"request_id"`
	Replayed  bool      `json:"replayed"`
}
type Receipt struct {
	ID      domain.ID `json:"id"`
	Digest  string    `json:"digest"`
	Done    bool      `json:"done"`
	Settled bool      `json:"settled"`
}
type Event struct {
	Revision  uint64    `json:"revision"`
	RequestID domain.ID `json:"request_id"`
	Action    Action    `json:"action"`
	Desired   State     `json:"desired"`
}
type record struct {
	Spec     Spec      `json:"spec"`
	Revision uint64    `json:"revision"`
	Desired  State     `json:"desired"`
	Removed  bool      `json:"removed"`
	Receipts []Receipt `json:"receipts"`
	Events   []Event   `json:"events"`
}
type runtimeRecord struct {
	StartID  domain.ID `json:"start_id"`
	ID       domain.ID `json:"id"`
	Instance domain.ID `json:"instance"`
	PID      int       `json:"pid"`
	Birth    string    `json:"birth"`
	Complete bool      `json:"complete"`
}
type Manager struct {
	Options ServerOptions
	Root    string
	Kind    Kind
	Backend Backend
	Logger  *slog.Logger
	// Authorize is rechecked before each native side effect, including receipt retry.
	Authorize func(context.Context) error
}

func New(root string, kind Kind, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{Root: root, Kind: kind, Backend: nativeBackend{}, Logger: logger}
}
func failure() error {
	return domain.Fail(domain.RecoveryRequired, "User service ownership or cleanup could not be confirmed.", "Preserve the registration and private state; inspect the current user's native service manager before retrying.")
}
func unavailable() error {
	return domain.Fail(domain.Unavailable, "The current user's service manager is unavailable.", "Use a logged-in GUI session on macOS, the existing user bus on Linux, or an interactive Windows session; do not enable linger or system services.")
}
func conflict() error {
	return domain.Fail(domain.Conflict, "The service revision or request identity changed.", "Read current service status and retry the original request unchanged.")
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func (m *Manager) path(suffix string) string {
	return filepath.Join(m.Root, "user-service-"+string(m.Kind)+suffix)
}
func (m *Manager) check() error {
	if !m.Kind.Valid() || !filepath.IsAbs(m.Root) || filepath.Clean(m.Root) != m.Root {
		return domain.Fail(domain.InvalidArgument, "Invalid user service scope.", "Select server or worker and an absolute private scope.")
	}
	return security.CheckPrivateDir(m.Root)
}
func (m *Manager) load() (record, error) {
	var r record
	raw, err := security.ReadPrivate(m.path(".json"), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil || domain.Decode(raw, &r) != nil {
		return r, failure()
	}
	user, err := currentUser()
	if err != nil || r.Spec.Version != 1 || r.Spec.ID.Validate() != nil || r.Spec.Kind != m.Kind || r.Spec.Root != m.Root || r.Spec.Platform != runtime.GOOS || r.Spec.User != user || r.Revision == 0 || (r.Desired != Stopped && r.Desired != Running) || len(r.Receipts) > 1024 || len(r.Events) != len(r.Receipts) {
		return r, failure()
	}
	seen := map[domain.ID]bool{}
	for i, receipt := range r.Receipts {
		e := r.Events[i]
		if receipt.ID.Validate() != nil || seen[receipt.ID] || (receipt.Done && !receipt.Settled) || len(receipt.Digest) != 64 || e.RequestID != receipt.ID || e.Revision != uint64(i+1) || !e.Action.Valid() || (e.Desired != Stopped && e.Desired != Running) {
			return r, failure()
		}
		seen[receipt.ID] = true
	}
	if r.Revision != uint64(len(r.Events)) {
		return r, failure()
	}
	return r, nil
}
func (m *Manager) save(r record) error {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 1<<20 {
		return failure()
	}
	if security.WriteAtomic(m.path(".json"), raw) != nil {
		return failure()
	}
	return nil
}
func (m *Manager) validate(s Spec) error {
	if !s.Kind.Valid() || s.Name != serviceName(s.User, s.Root, s.Kind) || (s.Platform != "windows" && (!filepath.IsAbs(s.DefinitionPath) || filepath.Clean(s.DefinitionPath) != s.DefinitionPath)) || (s.Platform == "windows" && s.DefinitionPath != "\\"+s.Name) {
		return failure()
	}
	root, err := fileIdentity(s.Root)
	if err != nil || root != s.RootIdentity {
		return failure()
	}
	identity, hash, err := binaryIdentity(s.Binary)
	if err != nil || identity != s.BinaryIdentity || hash != s.BinaryDigest {
		return failure()
	}
	return nil
}
func binaryIdentity(path string) (string, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0) {
		return "", "", failure()
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", failure()
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !os.SameFile(before, info) || before.Size() > 256<<20 {
		return "", "", failure()
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (256<<20)+1))
	after, e := f.Stat()
	current, e2 := os.Lstat(path)
	if err != nil || e != nil || e2 != nil || n != before.Size() || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", "", failure()
	}
	identity, err := fileIdentity(path)
	return identity, hex.EncodeToString(h.Sum(nil)), err
}
func serviceName(user, root string, kind Kind) string {
	return "io.delino.delidev." + string(kind) + "." + digest([]string{user, root})[:32]
}
func definitionPath(s Spec) string {
	home, _ := os.UserHomeDir()
	switch s.Platform {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", s.Name+".plist")
	case "linux":
		base := os.Getenv("XDG_CONFIG_HOME")
		if !filepath.IsAbs(base) {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "systemd", "user", s.Name+".service")
	case "windows":
		return "\\" + s.Name
	}
	return ""
}
func newSpec(root string, kind Kind, options ServerOptions) (Spec, error) {
	binary, err := os.Executable()
	if err != nil {
		return Spec{}, failure()
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return Spec{}, failure()
	}
	user, err := currentUser()
	if err != nil {
		return Spec{}, failure()
	}
	if strings.ContainsAny(root+binary, "\x00\r\n") {
		return Spec{}, failure()
	}
	ri, err := fileIdentity(root)
	if err != nil {
		return Spec{}, failure()
	}
	bi, bh, err := binaryIdentity(binary)
	if err != nil {
		return Spec{}, err
	}
	s := Spec{Options: options, Version: 1, ID: domain.NewID(), Kind: kind, Platform: runtime.GOOS, User: user, Root: root, RootIdentity: ri, Binary: binary, BinaryIdentity: bi, BinaryDigest: bh, Name: serviceName(user, root, kind)}
	s.DefinitionPath = definitionPath(s)
	if s.DefinitionPath == "" {
		return Spec{}, unavailable()
	}
	return s, nil
}
