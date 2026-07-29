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
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/logging"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/devhud-deck/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/httpserver"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const startupTimeout = 30 * time.Second

type failureReason string

const (
	failureConfiguration      failureReason = "configuration"
	failureEncryption         failureReason = "encryption"
	failureHashing            failureReason = "hashing"
	failurePseudonymization   failureReason = "pseudonymization"
	failureDatabase           failureReason = "database"
	failureGitHubOAuth        failureReason = "github_oauth"
	failureGitHubState        failureReason = "github_callback_state"
	failureGitHubBroker       failureReason = "github_broker"
	failureIdentityKeys       failureReason = "identity_keys"
	failureDeckAuthentication failureReason = "deck_authentication"
	failureBaseAuthentication failureReason = "delibase_authentication"
	failureAPI                failureReason = "api"
	failureListener           failureReason = "listener"
	failureServer             failureReason = "server"
	failureShutdown           failureReason = "shutdown"
	failureUnexpected         failureReason = "unexpected"
)

type classifiedFailure struct {
	reason failureReason
	cause  error
}

func (failure *classifiedFailure) Error() string { return failure.cause.Error() }

func (failure *classifiedFailure) Unwrap() error { return failure.cause }

func classifyFailure(reason failureReason, cause error) error {
	return &classifiedFailure{reason: reason, cause: cause}
}

func safeFailureReason(err error) failureReason {
	var failure *classifiedFailure
	if errors.As(err, &failure) {
		return failure.reason
	}
	return failureUnexpected
}

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.LookupEnv, logger); err != nil {
		logger.Error("Deck startup failed", "event", "startup_failure",
			"reason", safeFailureReason(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupEnv, logger *slog.Logger) error {
	configuration, err := config.Load(lookup)
	if err != nil {
		return classifyFailure(failureConfiguration, err)
	}
	encryptionKeys := make(
		map[string][]byte, len(configuration.PreviousEncryptionKeys)+1)
	for keyID, key := range configuration.PreviousEncryptionKeys {
		encryptionKeys[keyID] = key
	}
	encryptionKeys[configuration.EncryptionKeyID] = configuration.EncryptionKey
	cipher, err := security.NewVersionedCipher(
		configuration.EncryptionKeyID, encryptionKeys)
	if err != nil {
		return classifyFailure(failureEncryption, err)
	}
	hasher, err := security.NewHasher(configuration.HashingKey)
	if err != nil {
		return classifyFailure(failureHashing, err)
	}
	pseudonymizer, err := safelog.NewPseudonymizer(configuration.PseudonymKey)
	if err != nil {
		return classifyFailure(failurePseudonymization, err)
	}
	databaseCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	store, err := database.Open(databaseCtx, configuration.DatabaseURL, cipher, hasher)
	cancel()
	if err != nil {
		return classifyFailure(failureDatabase, err)
	}
	defer store.Close()
	githubOAuth, err := deckgithub.NewOAuth(deckgithub.OAuthConfig{
		ClientID:     configuration.GitHubClientID,
		ClientSecret: configuration.GitHubClientSecret,
		AppSlug:      configuration.GitHubAppSlug,
		CallbackURL:  config.DeckAudience + deckgithub.OAuthCallbackPath,
	}, nil)
	if err != nil {
		return classifyFailure(failureGitHubOAuth, err)
	}
	githubSigner, err := deckgithub.NewStateSigner(
		configuration.GitHubCallbackSigningKey)
	if err != nil {
		return classifyFailure(failureGitHubState, err)
	}
	githubClient := deckgithub.NewClient(nil)
	githubBroker, err := deckgithub.NewBroker(deckgithub.BrokerConfig{
		Signer: githubSigner, OAuth: githubOAuth, Client: githubClient,
		Callbacks: store, Lifecycle: store,
		WebhookSecret: configuration.GitHubWebhookSecret,
	})
	if err != nil {
		return classifyFailure(failureGitHubBroker, err)
	}
	keys, err := auth.NewJWKS(auth.JWKSConfig{URL: configuration.LogtoJWKSURL})
	if err != nil {
		return classifyFailure(failureIdentityKeys, err)
	}
	deckValidator, err := auth.NewValidatorForAudience(auth.Config{
		Issuer: configuration.LogtoIssuer, Audience: config.DeckAudience,
		KeySource: keys,
	})
	if err != nil {
		return classifyFailure(failureDeckAuthentication, err)
	}
	delibaseValidator, err := auth.NewValidator(auth.Config{
		Issuer: configuration.LogtoIssuer, Audience: config.DelibaseAudience,
		KeySource: keys,
	})
	if err != nil {
		return classifyFailure(failureBaseAuthentication, err)
	}
	dependencies := service.Dependencies{
		Store: store, Hasher: hasher, Pseudonymizer: pseudonymizer,
		Logger: logger, GitHubBroker: githubBroker, GitHubClient: githubClient,
		Repositories: service.NewGitHubRepositoryAuthorizer(
			store, githubClient, githubBroker),
	}
	handler, err := api.New(api.Dependencies{
		DeckAuthentication:     deckValidator,
		DelibaseAuthentication: delibaseValidator,
		Directory:              store, LifecycleClientID: configuration.LifecycleClientID,
		Health: store, Services: dependencies, Logger: logger,
		GitHubHTTP: githubBroker.Handler(),
	})
	if err != nil {
		return classifyFailure(failureAPI, err)
	}
	listener, err := net.Listen("tcp", configuration.HTTPAddress)
	if err != nil {
		return classifyFailure(
			failureListener, errors.New("deck startup: listener failed"))
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
			return classifyFailure(failureServer, err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return classifyFailure(
				failureShutdown, errors.New("deck shutdown failed"))
		}
		err := <-serveErrors
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return classifyFailure(failureServer, err)
		}
		return nil
	}
}
