// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type appsController struct {
	calls    map[string]trackedAppCall
	original domain.CodexAppConfiguration
	current  domain.CodexAppConfiguration
	version  string
	path     string
	cwd      string
	baseline [32]byte
	requests map[domain.ID]bool
}

func appsUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original Codex app operation needs reconciliation.", "Retain its account, configuration generation and operation receipt; do not repeat an uncertain native operation.")
}

func (c *Client) originalAppsEligible() bool {
	return c.appsProfile && c.mode == ThreadProtocol && c.version == "0.162.0" && c.managedHome != "" && c.managedHome == c.home && c.api == nil && c.sidechat == "" && c.problem == nil
}

func (c *Client) readAppsConfigLocked(ctx context.Context, cwd string) (json.RawMessage, [32]byte, error) {
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", map[string]any{"cwd": cwd, "includeLayers": false})
	if err != nil || response.ErrorCode != nil {
		return nil, [32]byte{}, incompatible()
	}
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if domain.DecodeBounded(response.Result, &result, 4<<20) != nil || result.Config == nil {
		return nil, [32]byte{}, incompatible()
	}
	apps := result.Config["apps"]
	delete(result.Config, "apps")
	// Fingerprint only for private equality checks. Neither the configuration
	// nor this fingerprint is a public execution or credential grant.
	canonical, err := json.Marshal(result.Config)
	if err != nil {
		return nil, [32]byte{}, incompatible()
	}
	return apps, sha256.Sum256(canonical), nil
}

func (c *Client) writeAppsPolicyLocked(ctx context.Context, request domain.ID, selection domain.CodexAppConfiguration, expected *string) (string, error) {
	policy, err := nativeAppsPolicy(selection)
	if err != nil {
		return "", err
	}
	response, err := c.wire.Call(ctx, request, "config/batchWrite", struct {
		Edits []struct {
			KeyPath string          `json:"keyPath"`
			Value   json.RawMessage `json:"value"`
			Merge   string          `json:"mergeStrategy"`
		} `json:"edits"`
		FilePath        string  `json:"filePath"`
		ExpectedVersion *string `json:"expectedVersion"`
		Reload          bool    `json:"reloadUserConfig"`
	}{Edits: []struct {
		KeyPath string          `json:"keyPath"`
		Value   json.RawMessage `json:"value"`
		Merge   string          `json:"mergeStrategy"`
	}{{"apps", policy, "replace"}}, FilePath: c.apps.path, ExpectedVersion: expected, Reload: true})
	if err != nil || response.ErrorCode != nil {
		return "", appsUncertain()
	}
	written, err := decodeNativeAppsWrite(response.Result, c.apps.path)
	if err != nil {
		return "", appsUncertain()
	}
	return written.Version, nil
}

// PrepareCodexApps runs before binding the original root thread or inference.
// Its caller must first retain the server-owned once-only operation intent and
// the protected original-account execution lease. This method creates no login,
// account, plugin installation, arbitrary MCP server or extra native process.
func (c *Client) PrepareCodexApps(ctx context.Context, request domain.ID, selection domain.CodexAppConfiguration, cwd string) (returned error) {
	if request.Validate() != nil || selection.Validate() != nil || !filepath.IsAbs(cwd) {
		return incompatible()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	if !c.originalAppsEligible() || c.thread != "" || c.apps != nil {
		return incompatible()
	}
	_, baseline, err := c.readAppsConfigLocked(ctx, cwd)
	if err != nil {
		return err
	}
	c.apps = &appsController{original: selection.Clone(), current: selection.Clone(), path: filepath.Join(c.managedHome, "config.toml"), cwd: cwd, baseline: baseline, requests: map[domain.ID]bool{request: true}}
	// Even an explicit native error may follow a file commit or reload. Keep
	// the original state and fence all input instead of replaying the write.
	defer func() {
		if returned != nil {
			c.problem = appsUncertain()
			returned = c.problem
		}
	}()
	version, err := c.writeAppsPolicyLocked(ctx, request, selection, nil)
	if err != nil {
		return err
	}
	c.apps.version = version
	apps, after, err := c.readAppsConfigLocked(ctx, cwd)
	if err != nil || after != baseline || verifyEffectiveApps(apps, selection) != nil {
		return appsUncertain()
	}
	return nil
}

// ReadCodexApps publishes only a complete snapshot of the retained original
// thread/account. A refresh error cannot be replaced with cached success.
func (c *Client) ReadCodexApps(ctx context.Context) ([]domain.CodexApp, error) {
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	if !c.originalAppsEligible() || c.thread.Validate() != nil || c.apps == nil {
		return nil, incompatible()
	}
	apps, after, err := c.readAppsConfigLocked(ctx, c.apps.cwd)
	if err != nil || after != c.apps.baseline || verifyEffectiveApps(apps, c.apps.current) != nil {
		c.problem = appsUncertain()
		return nil, c.problem
	}
	directory, err := c.readAppsDirectoryLocked(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := c.readInstalledAppsLocked(ctx)
	if err != nil {
		return nil, err
	}
	descriptions, err := c.readSelectedAppsLocked(ctx, c.apps.current.AppIDs)
	if err != nil {
		return nil, err
	}
	return joinAppsSnapshot(c.apps.current, directory, installed, descriptions)
}

// RevokeCodexApps never admits new authority in a live controller. File CAS,
// resolved policy and original-thread catalog invalidation are separate proofs.
// In-flight earlier accepted calls can finish before the catalog write barrier;
// a receipt does not claim rollback of their external effects or their cleanup.
func (c *Client) RevokeCodexApps(ctx context.Context, request domain.ID, next domain.CodexAppConfiguration, cwd string) (returned error) {
	if request.Validate() != nil || !filepath.IsAbs(cwd) {
		return incompatible()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	if !c.originalAppsEligible() || c.thread.Validate() != nil || c.apps == nil || cwd != c.apps.cwd || !c.apps.current.RemovalOnly(next) || c.apps.requests[request] || len(c.apps.requests) >= maxTrackedTurns {
		return incompatible()
	}
	c.apps.requests[request] = true
	defer func() {
		if returned != nil {
			c.problem = appsUncertain()
			returned = c.problem
		}
	}()
	version, err := c.writeAppsPolicyLocked(ctx, request, next, &c.apps.version)
	if err != nil {
		return err
	}
	c.apps.version = version
	apps, after, err := c.readAppsConfigLocked(ctx, cwd)
	if err != nil || after != c.apps.baseline || verifyEffectiveApps(apps, next) != nil {
		return appsUncertain()
	}
	fresh, err := c.readInstalledAppsLocked(ctx)
	if err != nil || !appsRemovalObserved(c.apps.current, next, fresh) {
		return appsUncertain()
	}
	c.apps.current = next.Clone()
	return nil
}
