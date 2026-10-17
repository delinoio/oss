// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// This client has the original execution token and pinned outbound route, never
// the provider credential. The server rechecks account and execution authority.
type openRouterImageBackend struct {
	client                   *http.Client
	config                   Config
	input                    domain.ExecutionJobInput
	settings                 codex.ThreadSettings
	token, origin, directory string
}

func openOpenRouterImageBackend(ctx context.Context, config Config, input domain.ExecutionJobInput, settings codex.ThreadSettings, thread domain.ID, profileRoot, token string, snapshot *networkSnapshot) (*openRouterImageBackend, func(), error) {
	if !input.NativeOpenRouterTools || config.execution == nil || thread.Validate() != nil {
		return nil, nil, publicationUncertain()
	}
	credential := config.execution.Credential
	client, closeIdle, err := networkHTTPClientForNativeRoute(ctx, config, credential, snapshot, nativeForwardHeaderTimeout)
	if err != nil {
		return nil, nil, err
	}
	// Results belong to the native thread within its private Worker/account home.
	// Resume reuses that owner; unrelated sessions and forks acquire no cache.
	directory := filepath.Join(profileRoot, "delidev-tool-images", string(thread))
	if err := security.PrivateDir(directory); err != nil {
		closeIdle()
		return nil, nil, err
	}
	return &openRouterImageBackend{client: client, config: config, input: input, settings: settings, token: token, origin: strings.TrimRight(credential.Endpoint, "/"), directory: directory}, closeIdle, nil
}

func imageBackendError() error {
	return domain.Fail(domain.Unsupported, "The selected OpenRouter image operation failed.", "Inspect the original tool result and correlated operation logs; do not replay an uncertain request automatically.")
}

func (b *openRouterImageBackend) run(ctx context.Context, raw json.RawMessage) (result string, returned error) {
	started := time.Now()
	if b.config.Logger != nil {
		b.config.Logger.InfoContext(ctx, "worker_openrouter_image", "execution_id", b.input.ExecutionID, "tool", "imagegen", "stage", "started")
	}
	defer func() {
		if b.config.Logger != nil {
			b.config.Logger.InfoContext(ctx, "worker_openrouter_image", "execution_id", b.input.ExecutionID, "tool", "imagegen", "stage", "completed", "success", returned == nil, "duration_ms", time.Since(started).Milliseconds())
		}
	}()
	var args struct {
		Prompt      string   `json:"prompt"`
		Transparent bool     `json:"transparent_background"`
		Paths       []string `json:"referenced_image_paths"`
		Last        *int     `json:"num_last_images_to_include"`
	}
	if domain.DecodeBounded(raw, &args, 256<<10) != nil || domain.Text(args.Prompt, "image prompt", domain.MaxPromptBytes, true) != nil || len(args.Paths) > 5 || args.Last != nil && (len(args.Paths) > 0 || *args.Last < 1 || *args.Last > 5) {
		return "", imageBackendError()
	}
	var images []map[string]string
	for _, path := range args.Paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(b.settings.Cwd, path)
		}
		data, media, err := b.readReferenceImage(path)
		if err != nil {
			return "", imageBackendError()
		}
		images = append(images, map[string]string{"image_url": "data:" + string(media) + ";base64," + base64.StdEncoding.EncodeToString(data)})
		clear(data)
	}
	if args.Last != nil {
		directory, err := os.Open(b.directory)
		if err != nil {
			return "", imageBackendError()
		}
		entries, err := directory.ReadDir(10001)
		directory.Close()
		if err != nil && err != io.EOF || len(entries) > 10000 {
			return "", imageBackendError()
		}
		// UUID-v7 result filenames retain generation order without persisting prompts.
		var paths []string
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".png") && domain.ID(strings.TrimSuffix(entry.Name(), ".png")).Validate() == nil {
				paths = append(paths, filepath.Join(b.directory, entry.Name()))
			}
		}
		sort.Strings(paths)
		if len(paths) == 0 {
			return "", imageBackendError()
		}
		count := min(*args.Last, len(paths))
		for _, path := range paths[len(paths)-count:] {
			file, err := os.Open(path)
			if err != nil {
				return "", imageBackendError()
			}
			info, statErr := file.Stat()
			if statErr != nil || !info.Mode().IsRegular() {
				file.Close()
				return "", imageBackendError()
			}
			data, err := io.ReadAll(io.LimitReader(file, domain.MaxInputImageBytes+1))
			file.Close()
			if err != nil || domain.ValidateImageContent(data, domain.ImagePNG) != nil {
				return "", imageBackendError()
			}
			images = append(images, map[string]string{"image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)})
			clear(data)
		}
	}
	background := "opaque"
	if args.Transparent {
		background = "transparent"
	}
	request := map[string]any{"model": "gpt-image-2", "prompt": args.Prompt, "background": background, "n": 1}
	path := "/api-proxy/v1/images/generations"
	if len(images) > 0 {
		path = "/api-proxy/v1/images/edits"
		request["images"] = images
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > 16<<20 {
		return "", imageBackendError()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.origin+path, bytes.NewReader(body))
	if err != nil {
		return "", imageBackendError()
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := b.client.Do(req)
	if err != nil {
		return "", imageBackendError()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", imageBackendError()
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		return "", imageBackendError()
	}
	var output struct {
		Created uint64 `json:"created"`
		Data    []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	if domain.DecodeBounded(data, &output, 16<<20) != nil || len(output.Data) != 1 {
		return "", imageBackendError()
	}
	png, err := base64.StdEncoding.Strict().DecodeString(output.Data[0].Base64)
	if err != nil || domain.ValidateImageContent(png, domain.ImagePNG) != nil {
		return "", imageBackendError()
	}
	defer clear(png)
	file, err := os.OpenFile(filepath.Join(b.directory, string(domain.NewID())+".png"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", imageBackendError()
	}
	_, writeErr := file.Write(png)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return "", imageBackendError()
	}
	return "data:image/png;base64," + output.Data[0].Base64, nil
}

// Bind restricted reads to an admitted directory handle. Root.Open resolves
// symlinks relative to that handle and rejects escapes even if a repository
// process replaces a path component after the lexical admission check.
func (b *openRouterImageBackend) readReferenceImage(path string) ([]byte, domain.ImageMediaType, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", imageBackendError()
	}
	if b.settings.Options.Permission == domain.PermissionFullAccess {
		file, err := os.Open(resolved)
		if err != nil {
			return nil, "", imageBackendError()
		}
		return readOpenRouterImageFile(file)
	}
	for _, rootPath := range append([]string{b.settings.Cwd}, b.settings.WorkspaceRoots...) {
		canonical, err := filepath.EvalSymlinks(rootPath)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(canonical, resolved)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		root, err := os.OpenRoot(canonical)
		if err != nil {
			return nil, "", imageBackendError()
		}
		data, media, err := readOpenRouterRootImage(root, relative)
		root.Close()
		return data, media, err
	}
	return nil, "", imageBackendError()
}

func readOpenRouterRootImage(root *os.Root, relative string) ([]byte, domain.ImageMediaType, error) {
	file, err := root.Open(relative)
	if err != nil {
		return nil, "", imageBackendError()
	}
	return readOpenRouterImageFile(file)
}

func readOpenRouterImageFile(file *os.File) ([]byte, domain.ImageMediaType, error) {
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", imageBackendError()
	}
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxInputImageBytes+1))
	media := domain.ImageMediaType(http.DetectContentType(data))
	if err != nil || domain.ValidateImageContent(data, media) != nil {
		clear(data)
		return nil, "", imageBackendError()
	}
	return data, media, nil
}
