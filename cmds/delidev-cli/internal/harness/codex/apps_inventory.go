// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Discovery metadata remains private. Only original identities, inert display
// names and distinct native booleans enter the product availability projection.
type appInfoWire struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Description         *string           `json:"description"`
	LogoURL             *string           `json:"logoUrl"`
	LogoURLDark         *string           `json:"logoUrlDark"`
	IconAssets          map[string]string `json:"iconAssets"`
	IconDarkAssets      map[string]string `json:"iconDarkAssets"`
	DistributionChannel *string           `json:"distributionChannel"`
	Branding            json.RawMessage   `json:"branding"`
	AppMetadata         json.RawMessage   `json:"appMetadata"`
	Labels              map[string]string `json:"labels"`
	InstallURL          *string           `json:"installUrl"`
	Accessible          bool              `json:"isAccessible"`
	Enabled             *bool             `json:"isEnabled"`
	PluginDisplayNames  []string          `json:"pluginDisplayNames"`
}
type appsPageWire struct {
	Data       []appInfoWire `json:"data"`
	NextCursor *string       `json:"nextCursor"`
}
type installedAppsWire struct {
	Apps []struct {
		ID       string  `json:"id"`
		Name     *string `json:"runtimeName"`
		Enabled  *bool   `json:"enabled"`
		Callable *bool   `json:"callable"`
	} `json:"apps"`
}

type nativeAppsCall func(context.Context, domain.ID, string, any) (nativewire.Response, error)

func readAppsInventory(ctx context.Context, scope domain.NativeAppScope, thread domain.ID, force bool, call nativeAppsCall) (domain.NativeAppInventory, error) {
	var empty domain.NativeAppInventory
	if scope.Validate() != nil || thread.Validate() != nil {
		return empty, domain.NativeAppsUnavailable()
	}
	result := domain.NativeAppInventory{Scope: scope, InventoryID: domain.NewID(), Discovered: []domain.NativeAppDiscovery{}, Installed: []domain.NativeAppInstalled{}}
	var cursor *string
	seenCursors := map[string]bool{}
	seenIDs := map[string]bool{}
	bytes := 0
	for pages := 0; pages <= domain.MaxSessionNativeApps; pages++ {
		response, err := call(ctx, domain.NewID(), "app/list", struct {
			Cursor   *string   `json:"cursor"`
			Limit    uint32    `json:"limit"`
			ThreadID domain.ID `json:"threadId"`
			Force    bool      `json:"forceRefetch"`
		}{cursor, 100, thread, force})
		if err != nil || response.ErrorCode != nil {
			return empty, domain.NativeAppsUnavailable()
		}
		bytes += len(response.Result)
		if bytes > 8<<20 {
			return empty, domain.NativeAppsUnavailable()
		}
		var page appsPageWire
		if domain.DecodeWithLimit(response.Result, &page, 8<<20) != nil || page.Data == nil {
			return empty, domain.NativeAppsUnavailable()
		}
		for _, app := range page.Data {
			if !domain.NativeAppIDValid(app.ID) || domain.Text(app.Name, "native app name", 1024, true) != nil || seenIDs[app.ID] || len(result.Discovered) >= domain.MaxSessionNativeApps {
				return empty, domain.NativeAppsUnavailable()
			}
			seenIDs[app.ID] = true
			enabled := true
			if app.Enabled != nil {
				enabled = *app.Enabled
			}
			result.Discovered = append(result.Discovered, domain.NativeAppDiscovery{ID: app.ID, Name: app.Name, Accessible: app.Accessible, Enabled: enabled})
		}
		if page.NextCursor == nil {
			break
		}
		if domain.Text(*page.NextCursor, "native app cursor", 4096, true) != nil || seenCursors[*page.NextCursor] || len(page.Data) == 0 || pages == domain.MaxSessionNativeApps {
			return empty, domain.NativeAppsUnavailable()
		}
		seenCursors[*page.NextCursor] = true
		cursor = page.NextCursor
	}
	response, err := call(ctx, domain.NewID(), "app/installed", struct {
		ThreadID domain.ID `json:"threadId"`
		Force    bool      `json:"forceRefresh"`
	}{thread, force})
	if err != nil || response.ErrorCode != nil {
		return empty, domain.NativeAppsUnavailable()
	}
	bytes += len(response.Result)
	if bytes > 8<<20 {
		return empty, domain.NativeAppsUnavailable()
	}
	var installed installedAppsWire
	if domain.DecodeWithLimit(response.Result, &installed, 8<<20) != nil || installed.Apps == nil {
		return empty, domain.NativeAppsUnavailable()
	}
	for _, app := range installed.Apps {
		if app.Enabled == nil || app.Callable == nil {
			return empty, domain.NativeAppsUnavailable()
		}
		result.Installed = append(result.Installed, domain.NativeAppInstalled{ID: app.ID, Enabled: *app.Enabled, Callable: *app.Callable})
	}
	if result.Validate() != nil {
		return empty, domain.NativeAppsUnavailable()
	}
	return result, nil
}
