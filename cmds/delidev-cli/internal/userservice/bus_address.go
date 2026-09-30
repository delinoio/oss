package userservice

import (
	"encoding/hex"
	"net/url"
	"path/filepath"
	"strings"
)

// Only one filesystem-backed Unix bus is eligible. Standard escaped paths and
// optional GUID metadata do not enable abstract, remote or autolaunch fallback.
func existingBusPath(address, runtimeRoot string) (string, error) {
	path := ""
	if address == "" {
		if !filepath.IsAbs(runtimeRoot) {
			return "", unavailable()
		}
		path = filepath.Join(runtimeRoot, "bus")
	} else {
		if len(address) > 8192 || !strings.HasPrefix(address, "unix:") || strings.ContainsAny(address, ";\r\n\x00") {
			return "", unavailable()
		}
		seen := map[string]bool{}
		for _, field := range strings.Split(strings.TrimPrefix(address, "unix:"), ",") {
			key, raw, ok := strings.Cut(field, "=")
			if !ok || seen[key] {
				return "", unavailable()
			}
			seen[key] = true
			value, err := url.PathUnescape(raw)
			if err != nil {
				return "", unavailable()
			}
			switch key {
			case "path":
				path = value
			case "guid":
				bytes, err := hex.DecodeString(value)
				if err != nil || len(bytes) != 16 {
					return "", unavailable()
				}
			default:
				return "", unavailable()
			}
		}
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\r\n\x00") {
		return "", unavailable()
	}
	return path, nil
}
