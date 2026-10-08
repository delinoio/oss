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

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
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

func Serve(ctx context.Context, config Config, ready func(Endpoint)) (result error) {
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
	if config.Desktop == nil {
		lease, err := security.TryLock(filepath.Join(config.DataDir, "desktop-host.lock"))
		if err != nil {
			return err
		}
		defer lease.Close()
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
	defer func() { result = errors.Join(result, state.Close()) }()
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
	listener := config.Listener
	if listener != nil && (config.Desktop == nil || listener.Addr().String() != config.Listen) {
		return domain.Fail(domain.Conflict, "The retained desktop listener does not match its runtime.", "Preserve the original listener ownership.")
	}
	if listener == nil {
		listener, err = net.Listen("tcp", config.Listen)
	}
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
	service := &Service{releaseVerifier: config.releaseVerifier, releaseFactory: config.releaseFactory, userServiceBackend: config.userServiceBackend, userServiceOptions: userservice.ServerOptions{Listen: config.Listen, TLSCertificate: config.TLSCertificate, TLSKey: config.TLSKey, AllowedOrigins: config.AllowedOrigins}, Store: state, Identity: identity, Endpoint: Endpoint{URL: protocol + "://" + listener.Addr().String(), ServerID: identity.ServerID, Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, StartedAt: time.Now().UTC()}, logger: config.Logger, stop: stop, accountSecrets: config.accountSecrets}
	if err := service.retainLostSubscriptionLeases("", "", false); err != nil {
		return err
	}
	if err := service.initializeServerSubscriptions(child); err != nil {
		return err
	}
	if err := service.initializeServerQuotas(domain.WithPrincipal(child, domain.Principal{Type: domain.OwnerDevice})); err != nil {
		return err
	}
	if err := service.initializeOAuth(child); err != nil {
		return err
	}
	defer service.closeAccountSecrets()
	defer service.closeIntegrationSecrets()
	if config.Desktop != nil && config.DesktopCredentials != nil {
		config.DesktopCredentials.attach(child, service)
		defer config.DesktopCredentials.close()
	}
	handler := service.Handler(config.AllowedOrigins, ip.IsLoopback())
	if config.Desktop != nil {
		target := *config.Desktop
		target.ServerID = identity.ServerID
		target.Root = config.DataDir
		if err := desktopruntime.Publish(target); err != nil {
			return err
		}
		defer desktopruntime.Retire(target)
		handler = desktopruntime.Handler(target, handler, config.AllowedOrigins)
	}
	defer service.executionAuthority.close()
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return child }, ErrorLog: slog.NewLogLogger(config.Logger.Handler(), slog.LevelWarn)}
	raw, err := json.Marshal(service.Endpoint)
	if err != nil {
		return err
	}
	if config.Desktop == nil {
		if err := security.WriteAtomic(filepath.Join(state.Root(), "server.json"), raw); err != nil {
			return domain.SafeError(err)
		}
		defer os.Remove(filepath.Join(state.Root(), "server.json"))
	}
	// Authenticated HTTP readiness permits immediate controller reuse or Stop.
	// Release the completed native startup barrier before serving any request;
	// logging and maintenance startup must not keep a ready server locked.
	if err := lifecycleLock.Close(); err != nil {
		return domain.SafeError(err)
	}
	lifecycleLock = nil
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	serverQuotaCtx, stopServerQuota := context.WithCancel(child)
	serverQuotaDone := make(chan struct{})
	go func() { defer close(serverQuotaDone); service.runServerQuotas(serverQuotaCtx) }()
	defer func() { stopServerQuota(); <-serverQuotaDone }()
	subscriptionCtx, stopSubscription := context.WithCancel(child)
	subscriptionDone := make(chan struct{})
	go func() { defer close(subscriptionDone); service.runServerSubscriptions(subscriptionCtx) }()
	defer func() { stopSubscription(); <-subscriptionDone }()
	cleanupCtx, stopCleanup := context.WithCancel(child)
	cleanupDone := make(chan struct{})
	go func() { defer close(cleanupDone); service.runFailedSubscriptionCleanups(cleanupCtx) }()
	defer func() { stopCleanup(); <-cleanupDone }()
	sshCtx, stopSSH := context.WithCancel(child)
	sshDone := make(chan struct{})
	go func() { defer close(sshDone); service.runSSHSetups(sshCtx) }()
	defer func() { stopSSH(); <-sshDone }()
	updateCtx, stopUpdates := context.WithCancel(child)
	updateDone := make(chan struct{})
	go func() { defer close(updateDone); service.runWorkerUpdateMaintenance(updateCtx) }()
	defer func() { stopUpdates(); <-updateDone }()
	quotaCtx, stopQuota := context.WithCancel(child)
	quotaDone := make(chan struct{})
	go func() { defer close(quotaDone); service.runSubscriptionQuotaMaintenance(quotaCtx) }()
	defer func() { stopQuota(); <-quotaDone }()
	skillsCtx, stopSkills := context.WithCancel(child)
	skillsDone := make(chan struct{})
	go func() { defer close(skillsDone); service.runSkillPreparations(skillsCtx) }()
	defer func() { stopSkills(); <-skillsDone }()
	catalogCtx, stopCatalog := context.WithCancel(child)
	catalogDone := make(chan struct{})
	go func() {
		defer close(catalogDone)
		if !config.disableCatalogMaintenance && !config.DisableBackgroundMaintenanceForTesting {
			service.runCatalogMaintenance(catalogCtx)
		}
	}()
	defer func() { stopCatalog(); <-catalogDone }()
	knownCtx, stopKnown := context.WithCancel(child)
	knownDone := make(chan struct{})
	go func() {
		defer close(knownDone)
		if !config.disableKnownModelMaintenance && !config.DisableBackgroundMaintenanceForTesting {
			service.knownSubscriptionModels().Run(knownCtx)
		}
	}()
	defer func() { stopKnown(); <-knownDone }()

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
	imageCleanupCtx, stopImageCleanup := context.WithCancel(child)
	imageCleanupDone := make(chan struct{})
	go func() { defer close(imageCleanupDone); service.runImageDraftCleanups(imageCleanupCtx) }()
	defer func() { stopImageCleanup(); <-imageCleanupDone }()
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
	stopSkills()
	<-skillsDone
	stopCatalog()
	<-catalogDone
	// Catalog refresh may be resolving the account-backed outbound route. Join
	// it before releasing account secrets so no maintenance request can touch a
	// closed vault during the explicit shutdown path.
	stopKnown()
	<-knownDone
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
	if config.Desktop != nil && config.DesktopCredentials != nil {
		config.DesktopCredentials.close()
	}
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
