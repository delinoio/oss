// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestImageHistoryFrameProfileIsExplicitAndNeverGenerates(t *testing.T) {
	managed := Config{Mode: ThreadProtocol, ManagedAuthentication: true, FrameProfile: ImageHistoryFrameProfile, ImageRoot: "/image-root", ImageMachineID: domain.NewID()}
	// Revert recovery observes without credentials: neither managed nor API.
	recovery := managed
	recovery.ManagedAuthentication = false
	for _, test := range []struct {
		name    string
		mutate  func(*Config)
		enabled bool
	}{
		{"managed-compaction", func(*Config) {}, true},
		{"credential-free-recovery", func(c *Config) { *c = recovery }, true},
		{"default-profile", func(c *Config) { c.FrameProfile = DefaultFrameProfile }, false},
		{"generation", func(c *Config) { c.EnableImageGeneration = true }, false},
		{"api-image-history", func(c *Config) { c.API = &APIConfig{} }, true},
		{"sidechat", func(c *Config) { c.Sidechat = ReadOnlySidechatV1 }, false},
		{"fork-history", func(c *Config) { c.ManagedForkHistory = true }, false},
		{"no-root", func(c *Config) { c.ImageRoot = "" }, false},
		{"no-machine", func(c *Config) { c.ImageMachineID = "" }, false},
		{"subscription-mode", func(c *Config) { c.Mode = SubscriptionProtocol }, false},
	} {
		config := managed
		test.mutate(&config)
		if config.imageObservationProfile() != test.enabled {
			t.Fatal("unexpected image history frame profile admission", test.name)
		}
		if !test.enabled && config.FrameProfile == ImageHistoryFrameProfile {
			if _, err := Open(context.Background(), config); err == nil {
				t.Fatal("invalid image history frame profile request was admitted", test.name)
			}
		}
	}
	client := Client{imageObservations: managed.imageObservationProfile()}
	if client.nativeFrameLimit() != 16<<20 || client.historyAggregateLimit() != 16<<20 {
		t.Fatal("image history profile does not scale the history budget")
	}
}

func TestImageHistoryFrameProfileOpensNonGeneratingClients(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    string
		managed bool
	}{
		{"managed-compaction", "thread-managed-ready", true},
		{"credential-free-recovery", "thread-revert-ready", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixtureConfig(t, test.mode)
			config.Mode, config.ManagedAuthentication = ThreadProtocol, test.managed
			config.FrameProfile, config.ImageRoot, config.ImageMachineID = ImageHistoryFrameProfile, t.TempDir(), domain.NewID()
			if !test.managed {
				config.RevertHistory = true
				config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_VERSION_FIXTURE=0.162.0")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, err := Open(ctx, config)
			if err != nil {
				t.Fatal("original image history profile rejected", err)
			}
			defer func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			}()
			if !client.imageObservations || client.imageGeneration || client.nativeFrameLimit() != 16<<20 || client.historyAggregateLimit() != 16<<20 {
				t.Fatal("image history profile did not select the enlarged non-generating profile")
			}
		})
	}
}
