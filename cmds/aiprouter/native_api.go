// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// NativeCatalogAPI supplies surface-specific service aliases and native metadata.
// Management-only mocks may implement ClientAPI without this optional interface.
type NativeCatalogAPI interface {
	ModelsForClient(context.Context, string, string) ([]ClientModel, error)
}

func modelsForClient(ctx context.Context, api ClientAPI, key, client string) ([]ClientModel, error) {
	if native, ok := api.(NativeCatalogAPI); ok {
		return native.ModelsForClient(ctx, key, client)
	}
	return api.Models(ctx, key)
}
func (a *ConnectAPI) nativeSnapshot(ctx context.Context, key, path string, dst any) error {
	if a.HTTP == nil || a.Server == "" {
		return errors.New("native catalog transport unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.Server, "/")+path, nil)
	if err != nil {
		return errors.New("invalid native catalog request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	// Copy the injected transport but always refuse redirects carrying credentials.
	client := *a.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("native catalog redirect refused") }
	res, err := client.Do(req)
	if err != nil {
		return errors.New("native catalog unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("native catalog request rejected")
	}
	const max = 256 << 20 // Matches the pinned upstream remote Codex catalog bound.
	body, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil || len(body) > max {
		return errors.New("native catalog exceeds response limit")
	}
	if err = json.Unmarshal(body, dst); err != nil {
		return errors.New("invalid native catalog response")
	}
	return nil
}
func (a *ConnectAPI) ModelsForClient(ctx context.Context, key, client string) ([]ClientModel, error) {
	switch client {
	case "claude-desktop":
		var snapshot struct {
			Version int                  `json:"version"`
			Models  []ClaudeDesktopModel `json:"models"`
		}
		if err := a.nativeSnapshot(ctx, key, "/v1/models?ids=desktop&format=desktop-config", &snapshot); err != nil {
			return nil, err
		}
		if snapshot.Version != 1 || snapshot.Models == nil {
			return nil, errors.New("invalid Desktop catalog envelope")
		}
		models := make([]ClientModel, 0, len(snapshot.Models))
		for _, row := range snapshot.Models {
			models = append(models, ClientModel{ID: row.Name, Name: row.LabelOverride, Desktop: &row})
		}
		return models, nil
	case "claude":
		var snapshot struct {
			Data []struct {
				ID          string `json:"id"`
				Name        string `json:"display_name"`
				Description string `json:"description"`
			} `json:"data"`
		}
		if err := a.nativeSnapshot(ctx, key, "/v1/models?ids=cli", &snapshot); err != nil {
			return nil, err
		}
		if snapshot.Data == nil {
			return nil, errors.New("invalid Claude catalog envelope")
		}
		models := make([]ClientModel, 0, len(snapshot.Data))
		for _, row := range snapshot.Data {
			if row.ID == "" {
				return nil, errors.New("invalid Claude alias")
			}
			models = append(models, ClientModel{ID: row.ID, Name: row.Name, Description: row.Description})
		}
		return models, nil
	case "codex":
		var snapshot struct {
			Models []map[string]any `json:"models"`
		}
		if err := a.nativeSnapshot(ctx, key, "/v1/catalog", &snapshot); err != nil {
			return nil, err
		}
		if snapshot.Models == nil {
			return nil, errors.New("invalid Codex catalog envelope")
		}
		models := make([]ClientModel, 0, len(snapshot.Models))
		seen := map[string]bool{}
		for _, row := range snapshot.Models {
			slug, _ := row["slug"].(string)
			if slug == "" || seen[slug] {
				return nil, errors.New("invalid or duplicate Codex catalog slug")
			}
			seen[slug] = true
			name, _ := row["display_name"].(string)
			if name == "" {
				name = slug
			}
			models = append(models, ClientModel{ID: slug, Name: name, CodexCatalog: row})
		}
		return models, nil
	default:
		return a.Models(ctx, key)
	}
}
