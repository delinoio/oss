package grok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func reflectedFixtureValue(secret, form string) any {
	switch form {
	case "escaped":
		raw, _ := json.Marshal(secret)
		return json.RawMessage(`"\u00` + hexByte(secret[0]) + string(raw[2:]))
	case "base64":
		return base64.StdEncoding.EncodeToString([]byte(secret))
	case "raw-base64":
		return base64.RawStdEncoding.EncodeToString([]byte(secret))
	case "url-base64":
		return base64.URLEncoding.EncodeToString([]byte(secret))
	case "raw-url-base64":
		return base64.RawURLEncoding.EncodeToString([]byte(secret))
	default:
		return secret
	}
}
func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&15]})
}

func TestAPIProtectedCredentialRefusesOriginalNativeFrames(t *testing.T) {
	for _, phase := range []string{"reflect-init-", "input-reflect-"} {
		for _, form := range []string{"literal", "escaped", "base64", "raw-base64", "url-base64", "raw-url-base64", "caller"} {
			if phase == "reflect-init-" && form == "caller" {
				continue
			}
			t.Run(phase+form, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cfg, logs := fixtureAPIConfig(t, phase+form)
				cfg.Model = turnFixtureModel
				cfg.Probe.Process.ProtectedValues = []string{"additional-protected-fixture"}
				api, err := openAPI(ctx, cfg)
				if phase == "reflect-init-" {
					if err == nil {
						api.Close()
						t.Fatal("reflected initialization accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer api.Close()
				if _, err = api.Create(ctx, domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
					t.Fatal(err)
				}
				text := 0
				_, err = api.RunText(ctx, domain.NewID(), "Ordinary input.", func(context.Context, InputClaim) error { return nil }, func(_ context.Context, o InputObservation) error {
					if o.Kind == InputText {
						text++
					}
					return nil
				})
				if err == nil || text != 0 {
					t.Fatal("protected native text reached publication", err, text)
				}
				if _, err = api.RunText(ctx, domain.NewID(), "Replacement", func(context.Context, InputClaim) error { t.Error("replayed original input"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
					t.Fatal("refusal reopened input")
				}
				if strings.Contains(logs.String(), cfg.Token) || strings.Contains(logs.String(), "additional-protected-fixture") {
					t.Fatal("protected data entered diagnostics")
				}
			})
		}
	}
}
