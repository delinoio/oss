package updates

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const maxManifestBytes = 256 * 1024

var supportedTargets = map[string]map[string]struct{}{
	"darwin":  {"x86_64": {}, "aarch64": {}},
	"windows": {"x86_64": {}, "aarch64": {}},
	"linux":   {"x86_64": {}, "aarch64": {}},
}

var supportedPackages = map[string]map[string]struct{}{
	"darwin":  {"macos-app": {}},
	"windows": {"windows-nsis": {}, "windows-msi": {}},
	"linux":   {"linux-appimage": {}, "linux-deb": {}},
}

type Handler struct {
	manifests fs.FS
}

func NewHandler(manifests fs.FS) *Handler {
	return &Handler{manifests: manifests}
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/vnd.devhud.update-manifest+json; version=1")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Vary", "X-DevHud-Package")
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	channel := request.PathValue("channel")
	platform := request.PathValue("platform")
	architecture, hasJSONSuffix := strings.CutSuffix(request.PathValue("artifact"), ".json")
	packageKind := request.Header.Get("X-DevHud-Package")
	if !hasJSONSuffix || channel != "stable" || !targetSupported(platform, architecture) || !packageSupported(platform, packageKind) {
		http.Error(response, "unsupported updater target", http.StatusBadRequest)
		return
	}
	if handler.manifests == nil {
		http.NotFound(response, request)
		return
	}
	manifestPath := path.Join(channel, platform, architecture, packageKind+".json")
	if strings.Contains(manifestPath, "..") {
		http.Error(response, "unsupported updater target", http.StatusBadRequest)
		return
	}
	manifestFile, err := handler.manifests.Open(manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, "manifest unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = manifestFile.Close() }()
	manifest, err := io.ReadAll(io.LimitReader(manifestFile, int64(maxManifestBytes)+1))
	if err != nil {
		http.Error(response, "manifest unavailable", http.StatusServiceUnavailable)
		return
	}
	if len(manifest) == 0 || len(manifest) > maxManifestBytes || !json.Valid(manifest) {
		http.Error(response, "manifest unavailable", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Length", stringLength(len(manifest)))
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(manifest)
	}
}

func targetSupported(platform, architecture string) bool {
	architectures, ok := supportedTargets[platform]
	if !ok {
		return false
	}
	_, ok = architectures[architecture]
	return ok
}

func packageSupported(platform, packageKind string) bool {
	packages, ok := supportedPackages[platform]
	if !ok {
		return false
	}
	_, ok = packages[packageKind]
	return ok
}

func stringLength(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
