// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

const managedProxyOrigin = "https://cli-chat-proxy.grok.com/v1"

type managedOpeningConfig struct {
	managedProfileConfig
	// The original protected lease owner installs cleanup before any credential
	// publication. It must separately join process/file cleanup and Take/Finish.
	beforeAuthentication func(string) error
	fixtureProxy         string
}

func managedEnvironment(environment []string, fixture string, bundle []byte) ([]string, error) {
	origin := managedProxyOrigin
	if fixture != "" {
		// A synthetic native fixture may use its own loopback responder. This
		// private hook cannot send an actual managed account to another origin.
		auth, _, err := subscription.ParseGrok(bundle)
		u, parseErr := url.Parse(fixture)
		if err != nil || auth.Key != "fixture-managed-access-token" || auth.Refresh != "fixture-managed-refresh-token" || auth.User != "fixture-managed-user" || parseErr != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawPath != "" || u.Path != "/v1" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != fixture {
			return nil, incompatible()
		}
		origin = fixture
	}
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, incompatible()
		}
		switch key {
		case "GROK_XAI_API_BASE_URL", "GROK_MODELS_BASE_URL", "GROK_MODELS_LIST_URL":
			// These discovery-only blocks select native API-key authentication.
			// The managed process instead owns the fixed first-party OIDC route.
			continue
		case credentialVariable, "XAI_API_KEY", "GROK_CLI_CHAT_PROXY_BASE_URL":
			return nil, incompatible()
		}
		result = append(result, entry)
	}
	return append(result, "GROK_CLI_CHAT_PROXY_BASE_URL="+origin), nil
}

// openManaged establishes the private OIDC handshake only. It remains separate
// from API opening, Worker dispatch, repository manifests and Resume admission.
func openManaged(ctx context.Context, config managedOpeningConfig) (connection *apiConnection, returned error) {
	if config.beforeAuthentication == nil {
		return nil, subscription.InvalidGrok()
	}
	managed, err := buildManagedProfile(config.managedProfileConfig)
	if err != nil {
		return nil, err
	}
	environment, err := probeEnvironment(config.Probe)
	if err != nil {
		return nil, err
	}
	wireEnvironment, err := managedEnvironment(environment, config.fixtureProxy, config.Bundle)
	if err != nil {
		return nil, err
	}
	if err := createNativeLogGuard(config.Probe.Home); err != nil {
		return nil, err
	}
	profile := managed.profile
	ready, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	phase := inspectPhase
	defer func() {
		if logger := config.Probe.Process.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "grok_managed_profile_failed", "owner_id", config.Probe.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "grok_managed_profile_initialized", "owner_id", config.Probe.Process.OwnerID, "profile_version", SupportedVersion)
			}
		}
	}()
	prepared := config.Probe.Process
	prepared.Env = environment
	if err := inspect(ready, prepared); err != nil {
		return nil, err
	}
	if err := profile.instructions.write(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(profile.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, incompatible()
	}
	_, writeErr := f.Write(profile.configuration)
	syncErr, closeErr := f.Sync(), f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || profile.check() != nil {
		return nil, incompatible()
	}
	for index, cwd := range []string{prepared.Cwd, config.Workspace} {
		phase = runtimeInspectPhase
		if index == 1 {
			phase = workspaceInspectPhase
		}
		inspection := prepared
		inspection.Cwd = cwd
		if err := inspectProfile(ready, inspection, profile); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(config.Probe.Home, "auth.json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) || profile.check() != nil {
		return nil, subscription.InvalidGrok()
	}
	// Cleanup is installed at this boundary, including atomic-write failures and
	// native startup failures. The harness cannot return the protected lease.
	if err := config.beforeAuthentication(config.Probe.Home); err != nil {
		return nil, err
	}
	if err := security.WriteAtomicOwned(path, config.Bundle); err != nil {
		return nil, subscription.InvalidGrok()
	}
	prepared.Env = wireEnvironment
	prepared.Args = []string{"--no-auto-update", "agent", "stdio"}
	phase = launchPhase
	wire, err := nativewire.StartJSONRPC(ctx, prepared)
	if err != nil {
		return nil, nativeLaunchError(err)
	}
	defer func() {
		if returned != nil && wire.Close() != nil {
			phase, returned = cleanupPhase, probeCleanupRequired()
		}
	}()
	phase = initializePhase
	response, err := wire.Call(ready, domain.NewID(), "initialize", initializeParams{ProtocolVersion: 1, ClientInfo: clientInfo{Name: "delidev", Title: "DeliDev", Version: "0.1.0"}})
	if err != nil {
		return nil, err
	}
	if response.ErrorCode != nil || validateInitializeResult(response.Result, prepared.Cwd, &profile) != nil {
		return nil, incompatible()
	}
	phase = authenticatePhase
	response, err = wire.Call(ready, domain.NewID(), "authenticate", struct {
		Method string `json:"methodId"`
		Meta   struct {
			Headless bool `json:"headless"`
		} `json:"_meta"`
	}{Method: "cached_token", Meta: struct {
		Headless bool `json:"headless"`
	}{Headless: true}})
	if err != nil {
		return nil, err
	}
	bundle, readErr := security.ReadPrivate(path, subscription.MaxBundle)
	defer clear(bundle)
	if readErr != nil || response.ErrorCode != nil || managed.validateAuthentication(response.Result, bundle) != nil || profile.checkInitialized() != nil {
		return nil, subscription.InvalidGrok()
	}
	inspection := prepared
	inspection.Cwd = config.Workspace
	// Later inspections retain their read-only blocked-catalog environment and
	// never grant a native HTTP call with the protected credentials.
	inspection.Env = environment
	return &apiConnection{wire: wire, profile: profile, workspace: config.Workspace, inspection: inspection, gate: make(chan struct{}, 1)}, nil
}
