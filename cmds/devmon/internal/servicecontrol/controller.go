package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/state"
)

var ErrUnsupportedPlatform = errors.New("service management is only supported on darwin")

const heartbeatStaleThreshold = 20 * time.Second

type Manager interface {
	Install(ctx context.Context) error
	Uninstall(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Status(ctx context.Context) (Summary, error)
}

type Summary struct {
	Domain        string         `json:"domain"`
	Daemon        UnitStatus     `json:"daemon"`
	Menubar       UnitStatus     `json:"menubar"`
	StatusFile    string         `json:"status_file"`
	ConfigFile    string         `json:"config_file"`
	DaemonLogFile string         `json:"daemon_log_file"`
	State         state.Snapshot `json:"state"`
	DaemonHealth  DaemonHealth   `json:"daemon_health"`
	Message       string         `json:"message,omitempty"`
}

type UnitStatus struct {
	Label     string `json:"label"`
	PlistPath string `json:"plist_path"`
	Loaded    bool   `json:"loaded"`
}

type DaemonHealth string

const (
	DaemonHealthRunning DaemonHealth = "running"
	DaemonHealthStopped DaemonHealth = "stopped"
	DaemonHealthError   DaemonHealth = "error"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type defaultCommandRunner struct{}

func (defaultCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	return command.CombinedOutput()
}

type StateReader interface {
	Read() (state.Snapshot, error)
}

type managerOptions struct {
	commandRunner CommandRunner
	stateReader   StateReader
	nowFn         func() time.Time
	logger        *slog.Logger
}

type Option func(*managerOptions)

func WithCommandRunner(commandRunner CommandRunner) Option {
	return func(options *managerOptions) {
		options.commandRunner = commandRunner
	}
}

func WithStateReader(stateReader StateReader) Option {
	return func(options *managerOptions) {
		options.stateReader = stateReader
	}
}

func WithNowFn(nowFn func() time.Time) Option {
	return func(options *managerOptions) {
		options.nowFn = nowFn
	}
}

func NewManager(logger *slog.Logger, options ...Option) (Manager, error) {
	mergedOptions := &managerOptions{
		commandRunner: defaultCommandRunner{},
		nowFn:         time.Now,
		logger:        logger,
	}
	for _, option := range options {
		option(mergedOptions)
	}

	if mergedOptions.stateReader == nil {
		stateStore, err := state.NewStore("", logger)
		if err != nil {
			return nil, fmt.Errorf("initialize state store: %w", err)
		}
		mergedOptions.stateReader = stateStore
	}

	return newManager(mergedOptions)
}
