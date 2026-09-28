package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeEffectiveSettings(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private accountless runtime required")
	}
	for _, effort := range []NativeEffort{HighEffort, "", MaxEffort} {
		t.Run("effort-"+string(effort), func(t *testing.T) {
			cfg, logs := apiFixtureConfig(t, "native-effective-settings")
			cfg.Process.Executable = binary
			cfg.Model = "claude-sonnet-4-6"
			cfg.Effort = effort
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The native initializer's optional loopback health check is served
				// locally. No Messages, login or hosted network request is permitted.
				if r.Method == http.MethodHead && r.URL.Path == "/api-proxy/api/hello" && r.URL.RawQuery == "" {
					w.WriteHeader(404)
					return
				}
				t.Error("settings read attempted a provider operation")
				w.WriteHeader(400)
			}))
			defer endpoint.Close()
			cfg.API.ServerOrigin = endpoint.URL
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s, err := OpenAPIStream(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}()
			initial, ok := s.InitialAppliedSettings()
			if !ok || initial.Model != cfg.Model || (effort != "" && (initial.Effort == nil || *initial.Effort != effort)) {
				t.Fatal("native initial settings were not retained")
			}
			current, err := s.ReadAppliedSettings(ctx, domain.NewID(), cfg.Model, effort)
			if err != nil || !reflect.DeepEqual(initial, current) {
				t.Fatal("native applied read changed without a control operation", err)
			}
			if current.Effort != nil {
				t.Log("native observed effort", *current.Effort)
			}
			if _, err := s.ReadAppliedSettings(ctx, domain.NewID(), cfg.Model, LowEffort); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("changed immutable effort did not reject the actual native observation", err)
			}
			after, _ := s.InitialAppliedSettings()
			if !reflect.DeepEqual(initial, after) {
				t.Fatal("failed comparison rewrote native startup evidence")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{nativeAPIFixtureToken, "private-api-instructions-sentinel"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("private settings entered logs")
				}
			}
		})
	}
}
