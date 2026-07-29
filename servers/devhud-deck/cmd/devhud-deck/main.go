package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/devhud-deck/internal/api"
	"github.com/delinoio/oss/servers/devhud-deck/internal/config"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	"github.com/delinoio/oss/servers/devhud-deck/internal/logging"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/devhud-deck/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/httpserver"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const startupTimeout = 30 * time.Second

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.LookupEnv, logger); err != nil {
		logger.Error("Deck startup failed", "event", "startup_failure")
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupEnv, logger *slog.Logger) error {
	configuration, err := config.Load(lookup)
	if err != nil {
		return err
	}
	cipher, err := security.NewCipher(configuration.EncryptionKey)
	if err != nil {
		return err
	}
	hasher, err := security.NewHasher(configuration.HashingKey)
	if err != nil {
		return err
	}
	pseudonymizer, err := safelog.NewPseudonymizer(configuration.PseudonymKey)
	if err != nil {
		return err
	}
	databaseCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	store, err := database.Open(databaseCtx, configuration.DatabaseURL, cipher, hasher)
	cancel()
	if err != nil {
		return err
	}
	defer store.Close()
	keys, err := auth.NewJWKS(auth.JWKSConfig{URL: configuration.LogtoJWKSURL})
	if err != nil {
		return err
	}
	deckValidator, err := auth.NewValidatorForAudience(auth.Config{
		Issuer: configuration.LogtoIssuer, Audience: config.DeckAudience,
		KeySource: keys,
	})
	if err != nil {
		return err
	}
	delibaseValidator, err := auth.NewValidator(auth.Config{
		Issuer: configuration.LogtoIssuer, Audience: config.DelibaseAudience,
		KeySource: keys,
	})
	if err != nil {
		return err
	}
	dependencies := service.Dependencies{
		Store: store, Hasher: hasher, Pseudonymizer: pseudonymizer,
		Logger: logger,
	}
	handler, err := api.New(api.Dependencies{
		DeckAuthentication:     deckValidator,
		DelibaseAuthentication: delibaseValidator,
		Directory:              store, LifecycleClientID: configuration.LifecycleClientID,
		Health: store, Services: dependencies, Logger: logger,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", configuration.HTTPAddress)
	if err != nil {
		return errors.New("deck startup: listener failed")
	}
	server := httpserver.Server(configuration.HTTPAddress, handler,
		httpserver.DefaultTimeouts())
	logging.Startup(logger, configuration.HTTPAddress)
	defer logging.Shutdown(logger)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.New("deck shutdown failed")
		}
		err := <-serveErrors
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
