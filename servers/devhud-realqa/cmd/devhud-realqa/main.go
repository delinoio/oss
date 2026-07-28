package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/servers/devhud-realqa/internal/api"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/authn"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/config"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/github"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/logging"
	serverruntime "github.com/delinoio/oss/servers/devhud-realqa/internal/runtime"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const databaseStartupTimeout = 30 * time.Second

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.LookupEnv, logger); err != nil {
		logger.Error("RealQA startup failed", "event", "startup_failure",
			"failure_stage", stage(err))
		os.Exit(1)
	}
}

type startupError struct{ value string }

func (failure *startupError) Error() string { return "RealQA startup failed" }

func stage(err error) string {
	var failure *startupError
	if errors.As(err, &failure) && failure != nil {
		return failure.value
	}
	return "unknown"
}

func run(ctx context.Context, lookup config.LookupEnv, logger *slog.Logger) error {
	configuration, err := config.Load(lookup)
	if err != nil {
		return &startupError{value: "configuration"}
	}
	pseudonymizer, err := safelog.NewPseudonymizer(configuration.LogPseudonymKey)
	if err != nil {
		return &startupError{value: "logging"}
	}
	databaseCtx, cancelDatabase := context.WithTimeout(ctx, databaseStartupTimeout)
	store, err := database.Open(databaseCtx, configuration.DatabaseURL,
		configuration.IdentityHashKey)
	cancelDatabase()
	if err != nil {
		return &startupError{value: "database"}
	}
	defer store.Close()
	keys, err := auth.NewJWKS(auth.JWKSConfig{URL: configuration.LogtoJWKSURL})
	if err != nil {
		return &startupError{value: "authentication"}
	}
	featureValidator, err := auth.NewValidatorForAudience(auth.Config{
		Issuer: configuration.LogtoIssuer, Audience: configuration.LogtoAudience,
		KeySource: keys,
	})
	if err != nil {
		return &startupError{value: "authentication"}
	}
	forwardedValidator, err := auth.NewValidator(auth.Config{
		Issuer:   configuration.LogtoIssuer,
		Audience: configuration.DelibaseLogtoAudience, KeySource: keys,
	})
	if err != nil {
		return &startupError{value: "authentication"}
	}
	authentication, err := authn.New(featureValidator, forwardedValidator,
		configuration.LifecycleLogtoClientID)
	if err != nil {
		return &startupError{value: "authentication"}
	}
	githubState, err := github.NewStateCodec(configuration.GitHubCallbackSigningKey)
	if err != nil {
		return &startupError{value: "github"}
	}
	githubAuthorization, err := github.NewAppAuthorization(
		configuration.GitHubOAuthClientID,
		configuration.GitHubAppSlug,
		githubState,
		nil)
	if err != nil {
		return &startupError{value: "github"}
	}
	githubCredentialVault, err := github.NewAESCredentialVaultWithPreviousKeys(
		configuration.GitHubCredentialKeyID,
		configuration.GitHubCredentialWrappingKey,
		configuration.GitHubCredentialPreviousKeys)
	if err != nil {
		return &startupError{value: "github"}
	}
	githubCallbackStore, err := github.NewPostgresCallbackStore(store)
	if err != nil {
		return &startupError{value: "github"}
	}
	githubClient, err := github.NewClient(github.ClientConfig{
		ProjectPermission: github.ProjectPermission(
			configuration.GitHubProjectPermission),
	})
	if err != nil {
		return &startupError{value: "github"}
	}
	githubProvider, err := github.NewAdapter(
		store, githubCredentialVault, githubClient,
		configuration.GitHubOAuthClientID,
		configuration.GitHubOAuthClientSecret,
		nil)
	if err != nil {
		return &startupError{value: "github"}
	}
	githubCallbacks, err := github.NewCallbackHandler(github.CallbackConfig{
		ClientID:      configuration.GitHubOAuthClientID,
		ClientSecret:  configuration.GitHubOAuthClientSecret,
		WebhookSecret: configuration.GitHubWebhookSecret,
		State:         githubState, Store: githubCallbackStore,
		Vault: githubCredentialVault,
		ProjectPermission: github.ProjectPermission(
			configuration.GitHubProjectPermission),
	})
	if err != nil {
		return &startupError{value: "github"}
	}
	handler, err := api.New(api.Dependencies{
		Authentication: authentication, Health: store, Logger: logger,
		GitHubCallbacks: githubCallbacks,
		Services: service.Dependencies{
			Store: store, GitHub: githubAuthorization,
			GitHubProvider: githubProvider,
			Pseudonymizer:  pseudonymizer, Logger: logger,
		},
	})
	if err != nil {
		return &startupError{value: "handler"}
	}
	listener, err := net.Listen("tcp", configuration.HTTPAddress)
	if err != nil {
		return &startupError{value: "listener"}
	}
	defer listener.Close()
	if err = serverruntime.Serve(ctx, listener, handler, logger,
		configuration.ShutdownTimeout); err != nil {
		return &startupError{value: "runtime"}
	}
	return nil
}
