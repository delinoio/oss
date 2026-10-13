// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/managedmcp"
)

// resolveManagedMCP reads only the execution's frozen generations from this
// Worker's own catalog and vault. The server never receives these values, and a
// definition without protected authentication never unlocks the native vault.
func resolveManagedMCP(ctx context.Context, config Config, credential Credential, definitions []domain.ManagedMCPDefinition) ([]codex.ManagedMCPServer, error) {
	catalog := managedmcp.Manager{Root: config.Root, ServerID: credential.ServerID, MachineID: credential.MachineID, WorkerDeviceID: credential.DeviceID, Logger: config.Logger}
	for _, definition := range definitions {
		if definition.Authentication != domain.MCPNoAuthentication {
			vault, err := credentials.Open(filepath.Join(config.Root, "managed-mcp-vault"), credential.ServerID, config.Logger)
			if err != nil {
				return nil, err
			}
			defer vault.Close()
			catalog.Secrets = vault
			break
		}
	}
	resolved, err := catalog.Resolve(ctx, definitions, domain.Codex)
	if err != nil {
		return nil, err
	}
	servers := make([]codex.ManagedMCPServer, 0, len(resolved))
	for i := range resolved {
		r := &resolved[i]
		servers = append(servers, codex.ManagedMCPServer{Identity: r.Identity(), Transport: r.Definition.Transport, Command: r.Definition.Command, Arguments: r.Definition.Arguments, Cwd: r.Definition.Cwd, Endpoint: r.Definition.Endpoint, Environment: r.Environment, Headers: r.Headers, BearerToken: r.BearerToken})
	}
	return servers, nil
}

func clearManagedMCP(servers []codex.ManagedMCPServer) {
	for i := range servers {
		clear(servers[i].Environment)
		clear(servers[i].Headers)
		servers[i].BearerToken = ""
	}
}

// checkManagedMCPProfile admits selected servers only for the ordinary Codex
// adapter that publishes verified support. Read-only Sidechat, other harnesses
// and every unverified adapter keep their existing boundary.
func checkManagedMCPProfile(configuration domain.ExecutionConfiguration) error {
	if len(configuration.ManagedMCP) > 0 && (configuration.Harness != domain.Codex || configuration.SidechatPolicy != "") {
		return domain.Fail(domain.Unsupported, "This native adapter has no verified managed MCP runtime.", "Keep selected definitions and use a harness whose adapter published verified support; read-only Sidechat never receives external servers.")
	}
	return nil
}

type managedMCPVerifier interface {
	VerifyManagedMCP(context.Context) ([]codex.MCPServerOutcome, error)
	ReconnectManagedMCP(context.Context, domain.ID) ([]codex.MCPServerOutcome, error)
}

// verifyManagedMCPBeforeInput proves the exact selected inventory before the
// first input is reserved. A server that failed or was cancelled gets one
// bounded reconnect of the same frozen definition; authentication-required
// servers need the owner and are never retried automatically. No call, tool
// input or elicitation is resent by this step.
func verifyManagedMCPBeforeInput(ctx context.Context, client managedMCPVerifier, logger *slog.Logger) error {
	outcomes, err := client.VerifyManagedMCP(ctx)
	logOutcomes := func(stage string, outcomes []codex.MCPServerOutcome) {
		if logger == nil {
			return
		}
		for _, outcome := range outcomes {
			logger.InfoContext(ctx, "native_execution_managed_mcp_outcome", "stage", stage, "definition_id", outcome.Identity.DefinitionID, "revision", outcome.Identity.Revision, "state", outcome.State)
		}
	}
	logOutcomes("initial", outcomes)
	if err == nil {
		return nil
	}
	var retry domain.ID
	for _, outcome := range outcomes {
		if outcome.State == domain.MCPStartupAuthenticationRequired {
			return err
		}
		if retry == "" && (outcome.State == domain.MCPStartupFailed || outcome.State == domain.MCPStartupCancelled) {
			retry = outcome.Identity.DefinitionID
		}
	}
	if retry == "" || domain.SafeError(err).Code != domain.Unavailable {
		return err
	}
	outcomes, err = client.ReconnectManagedMCP(ctx, retry)
	logOutcomes("reconnect", outcomes)
	return err
}
