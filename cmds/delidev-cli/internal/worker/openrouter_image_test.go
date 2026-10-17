// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestOpenRouterImageOriginalResultSurvivesBackendRecreation(t *testing.T) {
	var pixels bytes.Buffer
	_ = png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	encoded := base64.StdEncoding.EncodeToString(pixels.Bytes())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer original-execution" {
			t.Error("original execution token lost")
		}
		var body struct {
			Model      string `json:"model"`
			Prompt     string `json:"prompt"`
			Background string `json:"background"`
			N          int    `json:"n"`
			Images     []struct {
				URL string `json:"image_url"`
			} `json:"images"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "gpt-image-2" || body.N != 1 {
			t.Error("tool request changed")
		}
		if call == 1 && r.URL.Path != "/api-proxy/v1/images/generations" {
			t.Error("create route changed")
		}
		if call == 2 && (r.URL.Path != "/api-proxy/v1/images/edits" || len(body.Images) != 1 || body.Images[0].URL != "data:image/png;base64,"+encoded) {
			t.Error("retained original image lost")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 123, "data": []any{map[string]string{"b64_json": encoded}}})
	}))
	defer server.Close()
	backend := openRouterImageBackend{client: server.Client(), origin: server.URL, token: "original-execution", directory: t.TempDir(), settings: codex.ThreadSettings{Cwd: t.TempDir()}}
	url, err := backend.run(context.Background(), []byte(`{"prompt":"fixture","transparent_background":true}`))
	if err != nil || url != "data:image/png;base64,"+encoded {
		t.Fatal("original generation failed", err)
	}
	resumed := backend
	if _, err := resumed.run(context.Background(), []byte(`{"prompt":"edit","num_last_images_to_include":1}`)); err != nil {
		t.Fatal("resume lost private image result", err)
	}
	for _, raw := range []string{`{"prompt":"x","referenced_image_paths":["/outside/image.png"]}`, `{"prompt":"x","num_last_images_to_include":0}`, `{"prompt":"x","num_last_images_to_include":1,"referenced_image_paths":["x"]}`, `{"prompt":"x","unknown":true}`} {
		if _, err := backend.run(context.Background(), []byte(raw)); err == nil {
			t.Fatal("unsupported request succeeded")
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.run(canceled, []byte(`{"prompt":"canceled"}`)); err == nil {
		t.Fatal("canceled operation succeeded")
	}
	if calls.Load() != 2 {
		t.Fatal("invalid or canceled operation reached provider")
	}
}

func TestSelectedDynamicBackendUsesOriginalDurableFence(t *testing.T) {
	for _, delivery := range []domain.DynamicReplyDelivery{domain.DynamicTransmitted, domain.DynamicUncertain, domain.DynamicNotSent} {
		t.Run(string(delivery), func(t *testing.T) {
			c, _, event := dynamicWorkerFixture(t)
			c.publisher.input.NativeOpenRouterTools = true
			retained := c.dynamicTools[event.ItemID]
			namespace := "delidev_image"
			identity := domain.DynamicToolIdentity{Tool: "imagegen", Namespace: &namespace, ArgumentsDigest: strings.Repeat("a", 64)}
			retained.Tool.Request.Identity = identity
			retained.Tool.Snapshot.Identity = identity
			c.dynamicTools[event.ItemID] = retained
			event.DynamicTool.Request.Identity = identity
			root := filepath.Join(c.publisher.config.Root, "jobs", string(c.publisher.job), "dynamic-replies", "by-call", dynamicJournalKey(event.ItemID))
			client := &fixtureDynamicResponder{t: t, root: root, delivery: delivery, lost: delivery == domain.DynamicUncertain}
			calls := 0
			send := func(ctx context.Context, token, turn, claim domain.ID, commit func() error) (domain.DynamicToolReply, error) {
				calls++
				reply, err := client.ReplyDynamicUnavailable(ctx, token, turn, claim, commit)
				reply.Unavailable = false
				return reply, err
			}
			err := c.deliverDynamicReply(context.Background(), context.Background(), event, client, openRouterImageDynamicBackend, send)
			if (err == nil) == (delivery == domain.DynamicUncertain) || calls != 1 {
				t.Fatal("backend delivery fact changed", err)
			}
			retained = c.dynamicTools[event.ItemID]
			retained.Tool.Reply = nil
			c.dynamicTools[event.ItemID] = retained
			_ = c.deliverDynamicReply(context.Background(), context.Background(), event, client, openRouterImageDynamicBackend, send)
			if calls != 1 {
				t.Fatal("duplicate delivery repeated backend")
			}
		})
	}
}

func TestOpenRouterImageReadUsesAdmittedRootHandle(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	outside := filepath.Join(parent, "outside")
	for _, directory := range []string{workspace, outside} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var original, foreign bytes.Buffer
	_ = png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	_ = png.Encode(&foreign, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if err := os.WriteFile(filepath.Join(workspace, "original.png"), original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "original.png"), foreign.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Replace the pathname after admission. The held directory still owns
	// the original object; reopening the absolute pathname would leak foreign bytes.
	heldPath := workspace + "-held"
	if err := os.Rename(workspace, heldPath); err != nil {
		// os.OpenRoot holds a Windows handle without delete sharing, which
		// prevents replacement itself. Keep testing reads and escaping links.
		if runtime.GOOS != "windows" || !errors.Is(err, syscall.Errno(32)) {
			t.Fatal(err)
		}
		heldPath = workspace
	} else if err := os.Symlink(outside, workspace); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	data, _, err := readOpenRouterRootImage(root, "original.png")
	if err != nil || !bytes.Equal(data, original.Bytes()) {
		t.Fatal("read abandoned the admitted directory", err)
	}
	for _, relative := range []string{"swapped.png", filepath.Join("swapped-parent", "original.png")} {
		link := relative
		target := filepath.Join(outside, "original.png")
		if strings.Contains(relative, string(filepath.Separator)) {
			link = "swapped-parent"
			target = outside
		}
		if err := os.Symlink(target, filepath.Join(heldPath, link)); err != nil {
			t.Fatal(err)
		}
		if data, _, err := readOpenRouterRootImage(root, relative); err == nil || data != nil {
			t.Fatal("replacement symlink escaped admitted root")
		}
	}
}

func TestOpenRouterRecentImagesIgnoreInterruptedAtomicPublication(t *testing.T) {
	var pixels bytes.Buffer
	_ = png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	encoded := base64.StdEncoding.EncodeToString(pixels.Bytes())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 2 {
			var body struct {
				Images []struct {
					URL string `json:"image_url"`
				} `json:"images"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Images) != 1 || body.Images[0].URL != "data:image/png;base64,"+encoded {
				t.Error("interrupted cache publication replaced the previous valid image")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 123, "data": []any{map[string]string{"b64_json": encoded}}})
	}))
	defer server.Close()
	backend := openRouterImageBackend{client: server.Client(), origin: server.URL, token: "fixture", directory: t.TempDir()}
	if _, err := backend.run(context.Background(), []byte(`{"prompt":"first"}`)); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(backend.directory, security.OwnedTemporaryPrefix(string(domain.NewID())+".png")+"interrupted")
	if err := os.WriteFile(pending, []byte("partial PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	resumed := backend
	if _, err := resumed.run(context.Background(), []byte(`{"prompt":"edit","num_last_images_to_include":1}`)); err != nil {
		t.Fatal("pending cache file blocked the earlier image", err)
	}
	files, err := filepath.Glob(filepath.Join(backend.directory, "*.png"))
	if err != nil || len(files) != 2 {
		t.Fatal("atomic publication lost valid cache entries", err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, pixels.Bytes()) {
			t.Fatal("published a partial PNG", err)
		}
	}
	data, err := os.ReadFile(pending)
	if err != nil || string(data) != "partial PNG" {
		t.Fatal("resume adopted or removed another interrupted publication", err)
	}
}
