// SPDX-License-Identifier: Apache-2.0
package service

import "time"

type providerStateLimits struct {
	TTL                          time.Duration
	Entries                      int
	MaxEntryBytes, MaxTotalBytes int64
}

// Namespace policies bound the complete owner namespace, not just one response.
// Adapter wire names are the core's durable namespace; unknown namespaces fail closed.
func providerStatePolicy(namespace string) (providerStateLimits, bool) {
	switch namespace {
	case "openai-chat", "openai-responses", "anthropic":
		return providerStateLimits{time.Hour, 256, 4 << 10, 1 << 20}, true
	case "compaction-reasoning":
		return providerStateLimits{24 * time.Hour, 256, 32 << 20, 512 << 20}, true
	case commandCodeEffortsNamespace:
		return providerStateLimits{time.Hour, 64, 4096, 256 << 10}, true
	case "jev-route-pin":
		return providerStateLimits{24 * time.Hour, 512, 4096, 2 << 20}, true
	case "mimo-free":
		return providerStateLimits{time.Hour, 64, 128 << 10, 256 << 10}, true
	case "tool-reasoning-replay":
		return providerStateLimits{time.Hour, 64, 256 << 10, 256 << 10}, true
	case "cursor-agent":
		return providerStateLimits{15 * time.Minute, 64, 16 << 20, 16 << 20}, true
	case "devin":
		return providerStateLimits{24 * time.Hour, 256, 4 << 10, 256 << 10}, true
	case "zed-anthropic", "zed-openai", "zed-google", "zed-xai":
		return providerStateLimits{24 * time.Hour, 256, 4 << 10, 1 << 20}, true
	case "kiro":
		return providerStateLimits{24 * time.Hour, 256, 512 << 10, 16 << 20}, true
	case "google-gemini", "google-antigravity", "google-gemini-cli", "google-vertex":
		return providerStateLimits{time.Hour, 256, 2 << 20, 64 << 20}, true
	default:
		return providerStateLimits{}, false
	}
}
