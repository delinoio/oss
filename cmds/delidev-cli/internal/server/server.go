package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const DefaultListen = "127.0.0.1:46310"

type Config struct {
	DataDir                   string
	Listen                    string
	TLSCertificate            string
	TLSKey                    string
	AllowedOrigins            []string
	Logger                    *slog.Logger
	accountSecrets            accountSecrets
	disableCatalogMaintenance bool
}
type Endpoint struct {
	URL             string    `json:"url"`
	ServerID        domain.ID `json:"server_id"`
	Version         string    `json:"version"`
	ProtocolVersion int       `json:"protocol_version"`
	StartedAt       time.Time `json:"started_at"`
}
type writeControllerKey struct{}

type Service struct {
	delidevv1connect.UnimplementedSystemServiceHandler
	delidevv1connect.UnimplementedResourceServiceHandler
	delidevv1connect.UnimplementedConfigurationServiceHandler
	delidevv1connect.UnimplementedDeviceServiceHandler
	delidevv1connect.UnimplementedWorkerServiceHandler
	delidevv1connect.UnimplementedAccountServiceHandler
	delidevv1connect.UnimplementedProviderServiceHandler
	accountOnce        sync.Once
	accountGate        chan struct{}
	accountChecks      map[domain.ID]map[domain.ID]accountCheck
	accountSecrets     accountSecrets
	ownedVault         *credentials.Vault
	Store              *store.Store
	Identity           security.Identity
	Endpoint           Endpoint
	logger             *slog.Logger
	stop               context.CancelFunc
	stopping           atomic.Bool
	connectionsMu      sync.Mutex
	connections        map[domain.ID]map[domain.ID]context.CancelFunc
	pairAttempts       map[string]attemptWindow
	workerStreams      map[domain.ID]workerStream
	executionOnce      sync.Once
	executionAuthority *executionAuthority
}

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
	service := &Service{Store: state, Identity: identity, Endpoint: Endpoint{URL: protocol + "://" + listener.Addr().String(), ServerID: identity.ServerID, Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, StartedAt: time.Now().UTC()}, logger: config.Logger, stop: stop, accountSecrets: config.accountSecrets}
	defer service.closeAccountSecrets()
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
	service.executionAuthority.close()
	if err := service.closeAccountSecrets(); err != nil {
		return domain.SafeError(err)
	}
	config.Logger.Info("server_stopped", "server_id", identity.ServerID)
	return nil
}

func (s *Service) Handler(origins []string, loopback bool) http.Handler {
	s.executionOnce.Do(func() { s.executionAuthority = newExecutionAuthority(s) })
	proxy := apiproxy.New(s.executionAuthority, s.logger)
	mux := http.NewServeMux()
	options := []connect.HandlerOption{connect.WithReadMaxBytes(2 << 20), connect.WithSendMaxBytes(5 << 20)}
	mux.Handle(delidevv1connect.NewSystemServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewResourceServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewConfigurationServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewDeviceServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewWorkerServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewAccountServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewProviderServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewSessionServiceHandler(s, options...))
	mux.Handle(delidevv1connect.NewInteractionServiceHandler(s, options...))
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[origin] = true
	}
	writer := connect.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), writeControllerKey{}, http.NewResponseController(w)))
		correlation := string(domain.NewID())
		r.Header.Set(rpc.CorrelationHeader, correlation)
		w.Header().Set(rpc.CorrelationHeader, correlation)
		w.Header().Set("Cache-Control", "no-store")
		reject := func(err error) {
			safe := domain.SafeError(err)
			s.logger.Warn("rpc_rejected", "correlation_id", correlation, "code", safe.Code)
			_ = writer.Write(w, r, rpc.Error(err, correlation))
		}
		if loopback {
			host, _, err := net.SplitHostPort(r.Host)
			ip := net.ParseIP(host)
			if err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
				reject(domain.Fail(domain.PermissionDenied, "The RPC authority is not an allowed loopback host.", "Use the server's explicit local endpoint."))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, apiproxy.Prefix+"/") {
			// Native execution credentials use a closed private protocol, not
			// owner/client RPC authentication or browser CORS authorization.
			proxy.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !allowed[origin] {
			reject(domain.Fail(domain.PermissionDenied, "The client origin is not allowed.", "Configure the exact trusted desktop origin on the server."))
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Expose-Headers", rpc.CorrelationHeader+", Connect-Content-Encoding, Connect-Accept-Encoding")
		}
		if r.Method == http.MethodOptions {
			if origin == "" || r.Header.Get("Access-Control-Request-Method") != "POST" {
				reject(domain.Fail(domain.PermissionDenied, "Invalid RPC preflight.", "Use the authenticated Connect client."))
				return
			}
			for _, name := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
				switch strings.ToLower(strings.TrimSpace(name)) {
				case "authorization", "content-type", "connect-protocol-version", "connect-timeout-ms", "connect-content-encoding", "connect-accept-encoding":
				default:
					reject(domain.Fail(domain.PermissionDenied, "An RPC preflight header is not allowed.", "Use the standard Connect headers."))
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Connect-Content-Encoding, Connect-Accept-Encoding")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == delidevv1connect.DeviceServicePairDeviceProcedure {
			if !s.pairingAllowed(r.RemoteAddr) {
				reject(domain.Fail(domain.ResourceExhausted, "Pairing attempts are temporarily limited.", "Wait one minute before retrying the existing grant."))
				return
			}
		} else {
			authorized, release, err := s.authorizeRequest(r)
			if err != nil {
				reject(err)
				return
			}
			defer release()
			r = authorized
		}
		started := time.Now()
		mux.ServeHTTP(w, r)
		s.logger.Debug("rpc_finished", "correlation_id", correlation, "duration_ms", time.Since(started).Milliseconds())
	})
}
func (s *Service) GetStatus(_ context.Context, req *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	response := connect.NewResponse(&pb.GetStatusResponse{Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, SchemaVersion: store.SchemaVersion, ServerId: string(s.Identity.ServerID), Listener: s.Endpoint.URL, StartedAt: s.Endpoint.StartedAt.Format(time.RFC3339Nano), Stopping: s.stopping.Load()})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) StopServer(ctx context.Context, req *connect.Request[pb.StopServerRequest]) (*connect.Response[pb.StopServerResponse], error) {
	id := domain.ID(req.Msg.RequestId)
	_, err := s.Store.Mutate(ctx, id, "server.stop", struct {
		StartedAt time.Time `json:"started_at"`
	}{s.Endpoint.StartedAt}, func(*store.Tx) (any, error) {
		return struct {
			Accepted bool `json:"accepted"`
		}{true}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	s.stopping.Store(true)
	// Let the accepted response flush before canceling connection contexts. The
	// durable receipt already prevents an ambiguous client retry from duplicating.
	time.AfterFunc(100*time.Millisecond, s.stop)
	response := connect.NewResponse(&pb.StopServerResponse{RequestId: string(id)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetDoctor(ctx context.Context, req *connect.Request[pb.GetDoctorRequest]) (*connect.Response[pb.GetDoctorResponse], error) {
	_, _, err := s.Store.Snapshot(ctx, store.Filter{Kind: domain.SettingsKind, Limit: 2})
	state := "ready"
	if err != nil {
		state = "failed"
	}
	report := struct {
		Version         string    `json:"version"`
		ServerID        domain.ID `json:"server_id"`
		Listener        string    `json:"listener"`
		Database        string    `json:"database"`
		CredentialStore string    `json:"credential_store"`
		InferenceProbes bool      `json:"inference_probes"`
	}{rpc.Version, s.Identity.ServerID, s.Endpoint.URL, state, "owner-credential-ready", false}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetDoctorResponse{ReportJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func scope(f store.Filter) string {
	return fmt.Sprintf("page:%s:%s:%s", f.Kind, f.SessionID, f.ProjectID)
}

func ValidateConfig(config Config) error {
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	_, err := validateConfig(config)
	return err
}
