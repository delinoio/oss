package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

// Lifecycle is infrastructure intent, separate from a live server's readiness
// and its database receipts. The private record survives an abnormal exit.
type Lifecycle struct {
	Version       int          `json:"version"`
	State         DesiredState `json:"state"`
	Generation    domain.ID    `json:"generation"`
	Configuration string       `json:"configuration"`
}

// LockLifecycle serializes intent changes and the complete native start barrier.
// Controllers must release it before waiting for a child server to become ready.
func LockLifecycle(root string) (*security.Lock, error) {
	return security.TryLock(filepath.Join(root, "server-lifecycle.lock"))
}

func ReadLifecycle(root string) (Lifecycle, error) {
	raw, err := security.ReadPrivate(filepath.Join(root, "server-lifecycle.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return Lifecycle{}, nil
	}
	var value Lifecycle
	if err != nil || domain.Decode(raw, &value) != nil || value.Version != 1 || value.Generation.Validate() != nil || (value.State != DesiredRunning && value.State != DesiredStopped) {
		return Lifecycle{}, invalidLifecycle()
	}
	if decoded, err := hex.DecodeString(value.Configuration); err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value.Configuration {
		return Lifecycle{}, invalidLifecycle()
	}
	return value, nil
}

func configurationDigest(config Config) string {
	listen := config.Listen
	if listen == "" {
		listen = DefaultListen
	}
	raw, _ := json.Marshal(struct {
		Listen, Certificate, Key string
		Origins                  []string
	}{listen, config.TLSCertificate, config.TLSKey, config.AllowedOrigins})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (value Lifecycle) Matches(config Config) bool {
	return value.Configuration == configurationDigest(config)
}

// Call only with LockLifecycle held. No caller may replace malformed intent.
func WriteRunning(root string, config Config) (Lifecycle, error) {
	if _, err := ReadLifecycle(root); err != nil {
		return Lifecycle{}, err
	}
	value := Lifecycle{Version: 1, State: DesiredRunning, Generation: domain.NewID(), Configuration: configurationDigest(config)}
	return value, writeLifecycle(root, value)
}

func writeLifecycle(root string, value Lifecycle) error {
	raw, err := json.Marshal(value)
	if err == nil {
		err = security.WriteAtomic(filepath.Join(root, "server-lifecycle.json"), raw)
	}
	if err != nil {
		return domain.Fail(domain.RecoveryRequired, "Server lifecycle intent could not be committed.", "Preserve the private scope and retry the original operation after checking storage.")
	}
	return nil
}

// Stop intent is committed before the RPC receipt. If the later database commit
// fails, restart remains conservatively suppressed; no termination is claimed.
func writeStopped(root string, requestID domain.ID, configDigest string) error {
	value, err := ReadLifecycle(root)
	if err != nil {
		return err
	}
	if value.Version == 0 {
		value = Lifecycle{Version: 1, Configuration: configDigest}
	}
	value.State, value.Generation = DesiredStopped, requestID
	return writeLifecycle(root, value)
}

func invalidLifecycle() error {
	return domain.Fail(domain.RecoveryRequired, "Server lifecycle intent is invalid or inaccessible.", "Preserve the original private record; automatic startup cannot reset it.")
}

// SuppressLocalRestart is a same-user, existing-owner offline fallback. It does
// not report that a server or any owned work has actually stopped.
func SuppressLocalRestart(root string, requestID domain.ID) error {
	if err := requestID.Validate(); err != nil {
		return err
	}
	if err := security.CheckPrivateDir(root); err != nil {
		return domain.SafeError(err)
	}
	if _, err := security.LoadIdentity(root); err != nil {
		return invalidLifecycle()
	}
	lock, err := LockLifecycle(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	return writeStopped(root, requestID, configurationDigest(Config{}))
}

// SuppressCompletedServiceRestart carries a joined service Stop into ordinary
// automatic startup. Match the original generation so a replacement foreground
// controller can never have its newer intent suppressed by an old service exit.
func SuppressCompletedServiceRestart(root string, generation domain.ID) error {
	if err := generation.Validate(); err != nil {
		return err
	}
	lock, err := LockLifecycle(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	value, err := ReadLifecycle(root)
	if err != nil || value.Generation != generation || value.State == DesiredStopped {
		return err
	}
	return writeStopped(root, domain.NewID(), value.Configuration)
}
