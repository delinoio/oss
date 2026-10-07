package opencode

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// openAPISession owns a fresh process; it never adopts a discovered endpoint.
// It remains private until durable Worker/account/publication integration can
// supply its claims and handle every native lifecycle and recovery outcome.
func openAPISession(ctx context.Context, config apiSessionConfig) (api *sessionAPI, returned error) {
	return openAPISessionRestoring(ctx, config, nil)
}

func openAPISessionRestoring(ctx context.Context, config apiSessionConfig, restore *checkpointResume) (api *sessionAPI, returned error) {
	phase := runtimePhase
	defer func() {
		if config.Probe.Process.Logger != nil {
			if returned != nil {
				config.Probe.Process.Logger.WarnContext(ctx, "OpenCode owned API initialization failed", "owner_id", config.Probe.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
			} else {
				config.Probe.Process.Logger.InfoContext(ctx, "OpenCode owned API initialization completed", "owner_id", config.Probe.Process.OwnerID, "native_version", api.nativeVersion)
			}
		}
	}()
	if ctx.Err() != nil {
		return nil, unavailable()
	}
	env, profile, err := prepareAPISession(config)
	if err != nil {
		return nil, err
	}
	managed, err := managedConfigPaths()
	if err != nil {
		return nil, err
	}
	if err := inspectManagedConfig(managed); err != nil {
		return nil, err
	}
	if restore != nil {
		if err := restore.stage(ctx, config, profile); err != nil {
			return nil, err
		}
	}
	// Staging can take time. Recheck every original source even when this
	// session has no additive instructions or additional repositories.
	if err := profile.inspectInstructions(); err != nil {
		return nil, err
	}
	runtimePath := filepath.Dir(config.Probe.Home)
	runtimeIdentity, err := os.Lstat(runtimePath)
	if err != nil || !runtimeIdentity.IsDir() {
		return nil, sessionUncertain()
	}
	workspaceIdentity, err := os.Lstat(config.Workspace)
	if err != nil || !workspaceIdentity.IsDir() {
		return nil, sessionUncertain()
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, unavailable()
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, unavailable()
	}
	defer reservation.Close()
	port := strconv.Itoa(reservation.Addr().(*net.TCPAddr).Port)
	address := net.JoinHostPort("127.0.0.1", port)
	origin := "http://" + address
	// Startup has its own deadline; successful initialization must not leave
	// the long-lived native process attached to a canceled startup context.
	lifetime, cancel := context.WithCancel(ctx)
	ready, cancelReady := context.WithTimeout(lifetime, 30*time.Second)
	defer cancelReady()
	output := &startupOutput{expected: "opencode server listening on " + origin, ready: make(chan struct{}), cancel: cancel}
	prepared := config.Probe.Process
	prepared.Env = append(env, "OPENCODE_SERVER_USERNAME=delidev", "OPENCODE_SERVER_PASSWORD="+password)
	prepared.Args = []string{"serve", "--hostname=127.0.0.1", "--port=" + port, "--mdns=false"}
	prepared.Stdout, prepared.Stderr = output, &diagnosticOutput{output: output}
	phase = launchPhase
	handle, err := process.Start(lifetime, prepared)
	if err != nil {
		cancel()
		return nil, launchError(err)
	}
	client, transport := probeHTTPClient(address)
	closeOwned := func(cleanup context.Context, recover bool) error {
		if cleanup.Err() != nil {
			return cleanup.Err()
		}
		cancel()
		closed := handle.Close()
		transport.CloseIdleConnections()
		if closed != nil && !recover {
			return closed
		}
		if err := process.ReconcileOwnerContext(cleanup, prepared.Directory, prepared.OwnerID); err != nil {
			return err
		}
		select {
		case <-handle.Done():
			_ = handle.Wait() // Joins original output drains, independent of exit code.
			return nil
		case <-cleanup.Done():
			return cleanup.Err()
		}
	}
	defer func() {
		if returned == nil {
			return
		}
		// Caller cancellation cannot abandon an owned initialization process.
		// Keep journals/state on uncertainty; no replacement or state deletion.
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := closeOwned(cleanup, false); err != nil {
			phase, returned = cleanupPhase, sessionUncertain()
		} else if problem := output.status(); problem != nil {
			returned = problem
		}
	}()
	if err := reservation.Close(); err != nil {
		return nil, unavailable()
	}
	if err := handle.Resume(); err != nil {
		return nil, launchError(err)
	}
	phase = listenerPhase
	select {
	case <-output.ready:
	case <-ready.Done():
		return nil, unavailable()
	case <-handle.Done():
		return nil, unavailable()
	}
	api = &sessionAPI{
		client: client, origin: origin, password: password, cwd: config.Workspace,
		runtimeHome:       filepath.Dir(config.Probe.Home),
		checkpointProcess: checkpointProcessScope(config.Probe.Process),
		claim:             config.Claim, logger: prepared.Logger, owner: prepared.OwnerID, gate: make(chan struct{}, 1),
		apiProfile: profile, rejectionPolicy: config.Rejection,
		closeOwned:     func(ctx context.Context) error { return closeOwned(ctx, false) },
		reconcileOwned: func(ctx context.Context) error { return closeOwned(ctx, true) },
	}
	api.alive = func() error {
		if lifetime.Err() != nil {
			return unavailable()
		}
		select {
		case <-handle.Done():
			return unavailable()
		default:
			if problem := output.status(); problem != nil {
				return problem
			}
			return nil
		}
	}
	read := func(path, credential string, status int) ([]byte, error) {
		// Initialization reads remain distinct from live stream reconciliation.
		if err := api.alive(); err != nil {
			return nil, err
		}
		return readHTTP(ready, client, origin, path, credential, status)
	}
	api.verifyStreamOwner = func(ctx context.Context) error {
		if ctx.Err() != nil {
			return unavailable()
		}
		original := handle.Identity()
		current, err := process.ProcessIdentity(original.PID)
		if err != nil || original.Birth == "" || current.Birth != original.Birth || current.Group != original.Group {
			return sessionUncertain()
		}
		if err := api.alive(); err != nil {
			return err
		}
		for _, directory := range []struct {
			path     string
			original os.FileInfo
		}{{runtimePath, runtimeIdentity}, {config.Workspace, workspaceIdentity}} {
			current, err := os.Lstat(directory.path)
			if err != nil || !current.IsDir() || !os.SameFile(current, directory.original) {
				return sessionUncertain()
			}
		}
		if err := inspectManagedConfig(managed); err != nil {
			return err
		}
		readOriginal := func(path, credential string, expected int) ([]byte, error) {
			raw, err := readHTTP(ctx, client, origin, path, credential, expected)
			if err == nil {
				err = api.accountReconciliationRead(raw)
			}
			return raw, err
		}
		for _, credential := range []string{"", "incorrect-" + password} {
			if _, err := readOriginal("/global/health", credential, http.StatusUnauthorized); err != nil {
				return err
			}
		}
		health, err := readOriginal("/global/health", password, http.StatusOK)
		if err != nil {
			return err
		}
		if validateHealth(health) != nil {
			return incompatible()
		}
		global, err := readOriginal("/global/config", password, http.StatusOK)
		if err != nil {
			return err
		}
		if validateEmptyConfig(global) != nil {
			return incompatible()
		}
		return nil
	}
	phase = authPhase
	for _, credential := range []string{"", "incorrect-" + password} {
		if _, err := read("/global/health", credential, 401); err != nil {
			return nil, err
		}
	}
	phase = healthPhase
	health, err := read("/global/health", password, 200)
	if err != nil {
		return nil, err
	}
	if validateHealth(health) != nil {
		return nil, incompatible()
	}
	var healthMetadata struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(health, &healthMetadata)
	api.nativeVersion = healthMetadata.Version
	phase = configPhase
	global, err := read("/global/config", password, 200)
	if err != nil {
		return nil, err
	}
	if validateEmptyConfig(global) != nil {
		return nil, incompatible()
	}
	phase = schemaPhase
	schema, err := read("/doc", password, 200)
	if err != nil {
		return nil, err
	}
	initializationOperations := append(slices.Clone(operations),
		operationProfile{"/config", "get", "config.get", "200", "application/json"},
		operationProfile{"/provider", "get", "provider.list", "200", "application/json"},
		operationProfile{"/path", "get", "path.get", "200", "application/json"},
		operationProfile{"/agent", "get", "app.agents", "200", "application/json"},
	)
	if restore != nil && restore.source.Project == "global" && restore.source.Snapshot != nil {
		initializationOperations = append(initializationOperations, operationProfile{"/project/current", "get", "project.current", "200", "application/json"})
		api.projectProfile = true
	}
	if validateSchemaOperations(schema, initializationOperations) != nil {
		return nil, incompatible()
	}
	phase = configPhase
	if err := inspectManagedConfig(managed); err != nil {
		return nil, err
	}
	if err := api.verifyAPIProfile(ready); err != nil {
		return nil, err
	}
	if err := api.verifyNativeContext(ready, config); err != nil {
		return nil, err
	}
	if err := api.verifyAPIProfile(ready); err != nil {
		return nil, err
	}
	if err := inspectManagedConfig(managed); err != nil {
		return nil, err
	}
	if err := api.alive(); err != nil {
		return nil, err
	}
	return api, nil
}
