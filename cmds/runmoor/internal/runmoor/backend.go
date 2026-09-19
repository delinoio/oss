package runmoor

import (
	"context"
	"time"
)

type Observation struct {
	Exists   bool
	Running  bool
	ExitCode *int
	Handle   Handle
}

// Prepare publishes every resource handle before moving to the next side
// effect. All resources also carry deterministic installation/runner ownership
// so reconciliation can recover a crash between creation and publication.
type Driver interface {
	Validate(context.Context, Config, Pool, Snapshot) error
	Prepare(context.Context, Config, Pool, Runner, Snapshot, string, func(Handle) error) error
	Inspect(context.Context, Config, Runner, Snapshot) (Observation, error)
	Stop(context.Context, Config, Runner, Snapshot) error
	Cleanup(context.Context, Config, Runner, Snapshot) error
}
type DriverFactory func(Backend) (Driver, error)

func defaultDriver(kind Backend) (Driver, error) {
	switch kind {
	case Docker:
		return &DockerDriver{}, nil
	case Tart:
		return &TartDriver{Exec: OSCommand{}}, nil
	default:
		return nil, problem(ErrConfig, "Unknown backend.", "Select docker or tart.")
	}
}
func diskCheck(c Config) error {
	for _, path := range []string{c.Storage.State, c.Storage.Data} {
		n, err := freeDisk(path)
		if err != nil {
			return err
		}
		if n < uint64(c.Host.MinFreeDiskMiB)*1024*1024 {
			return problem(ErrDisk, "Free space is below the configured reserve.", "Free unrelated disk space or explicitly remove unused images; active work is retained.")
		}
	}
	return nil
}
func nowUTC() time.Time { return time.Now().UTC() }
