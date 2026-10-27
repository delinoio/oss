// SPDX-License-Identifier: Apache-2.0
package main

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/coder/websocket"
	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/commandcode"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/cursor"
	"github.com/delinoio/oss/packages/aiprouter/pricing"
	"github.com/delinoio/oss/packages/aiprouter/store"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
	"net/http"
	"strings"
	"sync"
	"time"
)

type server struct {
	// Optional embedding/test transport policy; production uses the guarded default.
	inferenceDestinations *inference.DestinationPolicy
	releases              *admissionReleaseQueue
	config                Config
	store                 *store.Store
	auth                  *Auth
	telemetry             *telemetry
	sockets               sync.WaitGroup
	artifacts             *artifacts.Service
	priceMu               sync.Mutex
	priceJSON             string
	priceCache            *pricing.Snapshot
	console               http.Handler
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if e := s.store.Ping(ctx); e != nil {
			s.telemetry.database.Set(0)
			http.Error(w, "database unavailable", 503)
			return
		}
		s.telemetry.database.Set(1)
		w.Write([]byte("ready\n"))
	})
	mux.Handle("GET /metrics", s.telemetry.metrics())
	mux.HandleFunc("GET /auth/login", s.auth.login)
	mux.HandleFunc("GET /auth/callback", s.auth.callback)
	mux.HandleFunc("POST /auth/logout", s.auth.logout)
	m := &management{store: s.store, auth: s.auth, telemetry: s.telemetry, inference: &inferenceAdapter{s: s.store, telemetry: s.telemetry}}
	options := []connect.HandlerOption{connect.WithInterceptors(errorInterceptor()), connect.WithReadMaxBytes(2 << 20), connect.WithSendMaxBytes(8 << 20)}
	register := func(path string, h http.Handler) { mux.Handle(path, m.sessionOnly(h)) }
	register(rpc.NewAccountServiceHandler(m, options...))
	register(rpc.NewOrganizationServiceHandler(m, options...))
	register(rpc.NewMembershipServiceHandler(m, options...))
	register(rpc.NewInvitationServiceHandler(m, options...))
	register(rpc.NewOwnershipServiceHandler(m, options...))
	register(rpc.NewKeyServiceHandler(m, options...))
	register(rpc.NewDeviceApprovalServiceHandler(m, options...))
	register(rpc.NewProviderServiceHandler(m, options...))
	register(rpc.NewHostedSettingsServiceHandler(m, options...))
	register(rpc.NewPoolServiceHandler(m, options...))
	register(rpc.NewRoutingServiceHandler(m, options...))
	register(rpc.NewUsageServiceHandler(m, options...))
	register(rpc.NewAuditServiceHandler(m, options...))
	register(rpc.NewOperatorServiceHandler(m, options...))
	path, h := rpc.NewDeviceAuthorizationServiceHandler(m, options...)
	mux.Handle(path, h)
	path, h = rpc.NewClientServiceHandler(m, options...)
	mux.Handle(path, m.clientOnly(h))
	mux.HandleFunc("/v1/", s.serveInference)
	if s.console != nil {
		mux.Handle("/", s.console)
	}
	return s.telemetry.middleware(mux)
}
func (s *server) serveInference(w http.ResponseWriter, r *http.Request) {
	// Settings are read for every admission; a DB outage cannot use old destination policy.
	settings, e := s.store.Settings(r.Context())
	if e != nil {
		http.Error(w, "database unavailable", 503)
		return
	}
	prices, e := s.priceSnapshot(settings.Prices)
	if e != nil {
		http.Error(w, "price configuration unavailable", 503)
		return
	}

	adapter := &inferenceAdapter{s: s.store, telemetry: s.telemetry, prices: prices}
	if s.releases != nil {
		adapter.releaseCompleted = s.releases.Submit
	}
	destinations := inference.DestinationPolicy{ApprovedOrigins: settings.Destinations, Rules: settings.DestinationRules}
	if s.inferenceDestinations != nil {
		destinations = *s.inferenceDestinations
		destinations.Rules = settings.DestinationRules
		destinations.ApprovedOrigins = append(append([]string(nil), destinations.ApprovedOrigins...), settings.Destinations...)
	}
	adapters := providerAdapters()
	adapters[commandcode.Wire] = commandCodeAdapter(adapter, destinations)
	originals := make(map[inference.Wire]inference.Adapter, len(adapters))
	for wire, value := range adapters {
		originals[wire] = value
	}
	adapters[cursor.Wire] = cursor.NewWithVision(hostedVisionPrepare(adapter, s.artifacts, originals))
	recovery := providerRecoveryPolicies(adapters)
	adapters = hostedProviderAdapters(adapters, adapter, s.artifacts)
	h, e := inference.New(inference.Config{Adapters: adapters, RecoveryPolicies: adapter.recoveryPolicies(recovery), RecoveryDestinations: adapter, Authorizer: adapter, Router: adapter, Negotiator: adapter, ResponseObserver: adapter, Views: adapter, Credentials: adapter, Continuations: adapter, CompactionHandoffs: compactionHandoffs{a: adapter}, ProviderStates: adapter, LiveBindings: adapter, SocketDialer: outboundSocketDialer{}, Usage: adapter, Artifacts: artifactAdapter{service: s.artifacts, auth: adapter}, Observer: s.telemetry, Logger: s.telemetry.logger, Destinations: destinations, MaxRequestBytes: 100 << 20})
	if e != nil {
		http.Error(w, "inference unavailable", 503)
		return
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && (r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/realtime" || r.URL.Path == "/v1/live" || r.URL.Path == "/v1/audio/transcriptions/stream" || strings.HasPrefix(r.URL.Path, "/v1/live/") || strings.HasPrefix(r.URL.Path, "/v1/realtime/calls/")) {
		if r.Method != "GET" {
			http.Error(w, "method not allowed", 405)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.config.PublicURL {
			http.Error(w, "origin rejected", 403)
			return
		}
		token := bearer(r)
		if _, e := s.store.AuthenticateKey(r.Context(), token); e != nil {
			status := 503
			if errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrForbidden) {
				status = 401
			}
			http.Error(w, "authentication unavailable", status)
			return
		}
		// Exact origin validation above replaces the library's host-only origin check.
		c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			return
		}
		c.SetReadLimit(liveSocketReadLimit)
		s.sockets.Add(1)
		defer s.sockets.Done()
		if r.URL.Path == "/v1/responses" {
			_ = h.ServeSocket(r.Context(), token, webSocket{c: c})
		} else {
			_ = h.ServeRealtimeSocket(r.Context(), token, r.URL.Path, r.URL.Query(), webSocket{c: c})
		}
		return
	}
	h.ServeHTTP(w, r)
}
