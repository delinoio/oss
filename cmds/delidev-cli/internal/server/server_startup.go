// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
)

func LoadEndpoint(root string) (Endpoint, error) {
	raw, err := security.ReadPrivate(filepath.Join(root, "server.json"), 8192)
	if err != nil {
		return Endpoint{}, domain.Fail(domain.ServerUnavailable, "No running server endpoint is available.", "Run `delidev server start` explicitly.")
	}
	var result Endpoint
	if err := domain.Decode(raw, &result); err != nil {
		return Endpoint{}, err
	}
	return result, nil
}

func validateConfig(config Config) (net.IP, error) {
	host, _, err := net.SplitHostPort(config.Listen)
	if err != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid server listener.", "Use an explicit IP:port such as 127.0.0.1:46310 or [::1]:46310.")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, domain.Fail(domain.InvalidArgument, "The listener must use an explicit IP address.", "Use loopback by default; remote selection never changes listener configuration.")
	}
	if (config.TLSCertificate == "") != (config.TLSKey == "") {
		return nil, domain.Fail(domain.MissingInput, "TLS requires both a certificate and private key.", "Configure --tls-cert and --tls-key together.")
	}
	if !ip.IsLoopback() && config.TLSCertificate == "" {
		return nil, domain.Fail(domain.PermissionDenied, "Non-loopback listeners require TLS.", "Configure TLS explicitly before exposing the authenticated server.")
	}
	for _, origin := range config.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "tauri") || strings.Contains(origin, "*") {
			return nil, domain.Fail(domain.InvalidArgument, "An allowed origin is invalid.", "Configure exact scheme and authority values without wildcards.")
		}
	}
	return ip, nil
}

func Serve(ctx context.Context, config Config, ready func(Endpoint)) error {
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	ip, err := validateConfig(config)
	if err != nil {
		return err
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	if err := security.PrivateDir(config.DataDir); err != nil {
		return domain.SafeError(err)
	}
	lifecycleLock, err := LockLifecycle(config.DataDir)
	if err != nil {
		return err
	}
	defer func() {
		if lifecycleLock != nil {
			lifecycleLock.Close()
		}
	}()
	if config.StartupID != "" {
		intent, err := ReadLifecycle(config.DataDir)
		if err != nil {
			return err
		}
		if intent.State != DesiredRunning || intent.Generation != config.StartupID || !intent.Matches(config) {
			return domain.Fail(domain.Conflict, "This server startup was canceled or superseded.", "Inspect lifecycle intent before explicitly starting again.")
		}
	} else if _, err := ReadLifecycle(config.DataDir); err != nil {
		return err
	}
	var certificate tls.Certificate
	if config.TLSCertificate != "" {
		certificate, err = tls.LoadX509KeyPair(config.TLSCertificate, config.TLSKey)
		if err != nil {
			return domain.Fail(domain.InvalidArgument, "The TLS certificate or key could not be loaded.", "Check the configured files and matching certificate/key without printing key contents.")
		}
	}
	state, err := store.Open(ctx, config.DataDir)
	if err != nil {
		return err
	}
	defer state.Close()
	if config.StartupID == "" {
		if _, err := WriteRunning(config.DataDir, config); err != nil {
			return err
		}
	}
	storedIdentity, err := state.ScopeIdentity(ctx)
	if err != nil {
		return err
	}
	identity, err := security.LoadIdentity(state.Root())
	if errors.Is(err, os.ErrNotExist) && storedIdentity == "" {
		identity, err = security.CreateIdentity(state.Root())
	}
	if err != nil {
		return domain.Fail(domain.RecoveryRequired, "The private owner identity is missing or invalid.", "Restore the matching credential; an existing scope is never silently re-paired.")
	}
	if err := state.BindIdentity(ctx, identity.ServerID); err != nil {
		return err
	}
	if err := state.RestoreBackupDeletionIntents(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), identity.ServerID); err != nil {
		return err
	}
	if err := state.RestoreSessionDeletionIntents(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), identity.ServerID); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return domain.Fail(domain.Unavailable, "The requested listener could not be bound.", "Free the configured port or explicitly select another listener; DeliDev never remaps it automatically.")
	}
	defer listener.Close()
	protocol := "http"
	if config.TLSCertificate != "" {
		protocol = "https"
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	}
	child, stop := context.WithCancel(ctx)
	defer stop()
	service := &Service{userServiceBackend: config.userServiceBackend, userServiceOptions: userservice.ServerOptions{Listen: config.Listen, TLSCertificate: config.TLSCertificate, TLSKey: config.TLSKey, AllowedOrigins: config.AllowedOrigins}, Store: state, Identity: identity, Endpoint: Endpoint{URL: protocol + "://" + listener.Addr().String(), ServerID: identity.ServerID, Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, StartedAt: time.Now().UTC()}, logger: config.Logger, stop: stop, accountSecrets: config.accountSecrets}
	defer service.closeAccountSecrets()
	defer service.closeIntegrationSecrets()
	handler := service.Handler(config.AllowedOrigins, ip.IsLoopback())
	defer service.executionAuthority.close()
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return child }, ErrorLog: slog.NewLogLogger(config.Logger.Handler(), slog.LevelWarn)}
	raw, err := json.Marshal(service.Endpoint)
	if err != nil {
		return err
	}
	if err := security.WriteAtomic(filepath.Join(state.Root(), "server.json"), raw); err != nil {
		return domain.SafeError(err)
	}
	defer os.Remove(filepath.Join(state.Root(), "server.json"))
	// Authenticated HTTP readiness permits immediate controller reuse or Stop.
	// Release the completed native startup barrier before serving any request;
	// logging and maintenance startup must not keep a ready server locked.
	if err := lifecycleLock.Close(); err != nil {
		return domain.SafeError(err)
	}
	lifecycleLock = nil
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	catalogCtx, stopCatalog := context.WithCancel(child)
	catalogDone := make(chan struct{})
	go func() {
		defer close(catalogDone)
		if !config.disableCatalogMaintenance {
			service.runCatalogMaintenance(catalogCtx)
		}
	}()
	defer func() { stopCatalog(); <-catalogDone }()
	dispatchCtx, stopDispatch := context.WithCancel(child)
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		service.runExecutionDispatch(dispatchCtx)
	}()
	defer func() { stopDispatch(); <-dispatchDone }()
	remediationCtx, stopRemediation := context.WithCancel(child)
	remediationDone := make(chan struct{})
	go func() {
		defer close(remediationDone)
		service.runAutomaticPRRemediation(remediationCtx)
	}()
	defer func() { stopRemediation(); <-remediationDone }()
	scheduleCtx, stopSchedules := context.WithCancel(child)
	schedulesDone := make(chan struct{})
	go func() {
		defer close(schedulesDone)
		service.runScheduleDispatch(scheduleCtx)
	}()
	defer func() { stopSchedules(); <-schedulesDone }()
	creationsCtx, stopCreations := context.WithCancel(child)
	creationsDone := make(chan struct{})
	go func() { defer close(creationsDone); service.runBackupCreations(creationsCtx) }()
	defer func() { stopCreations(); <-creationsDone }()
	deletionsCtx, stopDeletions := context.WithCancel(child)
	deletionsDone := make(chan struct{})
	go func() { defer close(deletionsDone); service.runBackupDeletions(deletionsCtx) }()
	defer func() { stopDeletions(); <-deletionsDone }()
	sessionDeletionsCtx, stopSessionDeletions := context.WithCancel(child)
	sessionDeletionsDone := make(chan struct{})
	go func() { defer close(sessionDeletionsDone); service.runSessionDeletions(sessionDeletionsCtx) }()
	defer func() { stopSessionDeletions(); <-sessionDeletionsDone }()
	config.Logger.Info("server_ready", "server_id", identity.ServerID, "listener", service.Endpoint.URL, "version", rpc.Version)
	if ready != nil {
		ready(service.Endpoint)
	}
	select {
	case err := <-done:
		// A failed listener must revoke child request contexts too. Otherwise
		// an active provider request could outlive its vault/database owner.
		stop()
		_ = httpServer.Close()
		if !errors.Is(err, http.ErrServerClosed) {
			config.Logger.Error("server_failed", "cause", "listener_failure")
			return domain.Fail(domain.Unavailable, "The server listener failed.", "Inspect server status and explicitly restart after resolving the failure.")
		}
	case <-child.Done():
		service.stopping.Store(true)
		service.executionAuthority.cancel()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			httpServer.Close()
		}
		<-done
	}
	stopCatalog()
	<-catalogDone
	stopRemediation()
	<-remediationDone
	stopDispatch()
	<-dispatchDone
	stopSchedules()
	<-schedulesDone
	stopCreations()
	<-creationsDone
	stopDeletions()
	<-deletionsDone
	service.executionAuthority.close()
	if err := service.closeAccountSecrets(); err != nil {
		return domain.SafeError(err)
	}
	if err := service.closeIntegrationSecrets(); err != nil {
		return domain.SafeError(err)
	}
	config.Logger.Info("server_stopped", "server_id", identity.ServerID)
	return nil
}

func ValidateConfig(config Config) error {
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	_, err := validateConfig(config)
	return err
}
