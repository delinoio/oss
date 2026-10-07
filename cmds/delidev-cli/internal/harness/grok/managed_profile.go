// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/pelletier/go-toml/v2"
)

type authenticationProfile uint8

const (
	apiAuthentication authenticationProfile = iota
	managedAuthentication
)

func (p apiProfile) selector() string {
	if p.authentication == managedAuthentication {
		return p.nativeModel
	}
	return selectedModel
}

func (p apiProfile) selectorName() string {
	if p.authentication == managedAuthentication {
		return p.nativeName
	}
	return selectedModelName
}

// These private data validators establish no process, account lease or send
// authority. Production admission remains closed until the separate managed
// execution, repository and complete-history profiles are accepted.
type managedProfileConfig struct {
	Probe         ProbeConfig        `json:"-"`
	Workspace     string             `json:"-"`
	Instructions  string             `json:"-"`
	Model         string             `json:"-"`
	ModelName     string             `json:"-"`
	ContextTokens uint64             `json:"-"`
	Mode          domain.SessionMode `json:"-"`
	Bundle        []byte             `json:"-"`
}

type managedProfile struct {
	profile  apiProfile
	identity subscription.Identity
}

func buildManagedProfile(config managedProfileConfig) (managedProfile, error) {
	if config.Mode == "" {
		config.Mode = domain.ExecuteMode
	}
	if !config.Mode.Valid() || config.Probe.Version != SupportedVersion || config.Probe.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Probe.Process.Executable) || domain.Text(config.Instructions, "instructions", domain.MaxAppliedInstructions, false) != nil || !text(config.Model, 256) || config.Model == selectedModel || !text(config.ModelName, 256) || config.ContextTokens < 1024 || config.ContextTokens > 1_000_000_000 {
		return managedProfile{}, incompatible()
	}
	workspace, err := filepath.EvalSymlinks(config.Workspace)
	if err != nil || !filepath.IsAbs(config.Workspace) || workspace != filepath.Clean(config.Workspace) {
		return managedProfile{}, incompatible()
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return managedProfile{}, incompatible()
	}
	root := filepath.Dir(config.Probe.Home)
	for _, pair := range [][2]string{{workspace, root}, {root, workspace}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return managedProfile{}, incompatible()
		}
	}
	auth, identity, err := subscription.ParseGrok(config.Bundle)
	if err != nil || !auth.Expires.After(time.Now().UTC().Add(time.Minute)) {
		return managedProfile{}, subscription.InvalidGrok()
	}
	// Built-in models retain session-token authentication. Defining an API
	// provider/model table or a custom models endpoint would select API-key
	// authentication and must never receive a managed subscription credential.
	configuration, err := toml.Marshal(map[string]any{
		"auth": map[string]any{"preferred_method": "oidc"},
		"models": map[string]any{
			"default": config.Model, "session_summary": config.Model,
			"image_description": config.Model, "web_search": config.Model,
			"allowed_models": []string{config.Model}, "max_retries": 0,
		},
		"session": map[string]any{"load_envrc": false},
		"cli":     map[string]any{"auto_update": false},
	})
	if err != nil || len(configuration) > 16<<10 {
		return managedProfile{}, incompatible()
	}
	return managedProfile{profile: apiProfile{authentication: managedAuthentication, nativeModel: config.Model, nativeName: config.ModelName, model: config.Model, mode: config.Mode, contextTokens: config.ContextTokens, configuration: configuration, instructions: instructionProfile{path: filepath.Join(config.Probe.Home, "Agents.md"), contents: config.Instructions}, path: filepath.Join(config.Probe.Home, "config.toml"), logGuard: filepath.Join(config.Probe.Home, "logs")}, identity: identity}, nil
}

func (p managedProfile) validateAuthentication(raw, bundle []byte) error {
	var envelope, metadata map[string]json.RawMessage
	if decode(raw, &envelope) != nil || len(envelope) != 1 || decode(envelope["_meta"], &metadata) != nil || len(metadata) != 14 {
		return subscription.InvalidGrok()
	}
	for _, key := range []string{"email", "auth_mode", "team_id", "is_team_principal", "team_name", "is_zdr", "team_role", "coding_data_retention_opt_out", "can_administer_team", "show_resolved_model", "gate", "subscription_tier", "feedback_trace_offer", "backend_billed"} {
		if _, ok := metadata[key]; !ok {
			return subscription.InvalidGrok()
		}
	}
	var response struct {
		Meta struct {
			Email           json.RawMessage `json:"email"`
			Mode            string          `json:"auth_mode"`
			Team            json.RawMessage `json:"team_id"`
			TeamPrincipal   bool            `json:"is_team_principal"`
			TeamName        json.RawMessage `json:"team_name"`
			ZeroRetention   bool            `json:"is_zdr"`
			TeamRole        json.RawMessage `json:"team_role"`
			RetentionOptOut bool            `json:"coding_data_retention_opt_out"`
			Admin           json.RawMessage `json:"can_administer_team"`
			ResolvedModel   json.RawMessage `json:"show_resolved_model"`
			Gate            json.RawMessage `json:"gate"`
			Tier            json.RawMessage `json:"subscription_tier"`
			Feedback        bool            `json:"feedback_trace_offer"`
			BackendBilled   bool            `json:"backend_billed"`
		} `json:"_meta"`
	}
	auth, identity, err := subscription.ParseGrok(bundle)
	if err != nil || p.profile.authentication != managedAuthentication || !bytes.Equal(subscription.CommitmentInput(p.identity), subscription.CommitmentInput(identity)) || decode(raw, &response) != nil || response.Meta.Mode != "Oidc" || !isNull(response.Meta.Gate) || response.Meta.BackendBilled || response.Meta.Feedback || !auth.Expires.After(time.Now().UTC()) {
		return subscription.InvalidGrok()
	}
	m := response.Meta
	if m.TeamPrincipal != (identity.PrincipalType == "Team") || !matchesNullableString(m.Email, auth.Email) || !matchesNullableString(m.Team, auth.TeamID) || !matchesNullableString(m.TeamName, auth.TeamName) || !matchesNullableString(m.TeamRole, auth.TeamRole) || m.RetentionOptOut != auth.RetentionOptOut {
		return subscription.InvalidGrok()
	}
	for _, value := range []json.RawMessage{m.Admin, m.ResolvedModel} {
		var observed bool
		if !isNull(value) && decode(value, &observed) != nil {
			return subscription.InvalidGrok()
		}
	}
	var tier string
	if !isNull(m.Tier) && (decode(m.Tier, &tier) != nil || domain.Text(tier, "native auth metadata", 256, false) != nil) {
		return subscription.InvalidGrok()
	}
	return nil
}

func matchesNullableString(raw json.RawMessage, expected *string) bool {
	if isNull(raw) {
		return expected == nil
	}
	var observed string
	return expected != nil && decode(raw, &observed) == nil && observed == *expected
}
