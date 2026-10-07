// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestManagedBundleCaptureIsOnceOnlyIncludingFailedIdentity(t *testing.T) {
	config, _ := fixtureManagedProfile(t)
	for _, mode := range []string{"unchanged", "rotated", "foreign", "uncertain", "unchanged-tokens", "expired"} {
		t.Run(mode, func(t *testing.T) {
			capture, err := newManagedBundleCapture(config.Bundle)
			if err != nil {
				t.Fatal(err)
			}
			if raw, err := capture.take(); err == nil || len(raw) != 0 {
				t.Fatal("bundle delivery preceded original capture")
			}
			latest := bytes.Clone(config.Bundle)
			defer clear(latest)
			if mode != "unchanged" {
				auth, _, err := subscription.ParseGrok(latest)
				if err != nil {
					t.Fatal(err)
				}
				auth.Created, auth.Expires = time.Now().UTC(), time.Now().UTC().Add(2*time.Hour)
				if mode != "unchanged-tokens" {
					auth.Key = "fixture-rotated-access-token"
				}
				if mode == "foreign" {
					auth.User = "fixture-other-user"
				}
				if mode == "expired" {
					auth.Created, auth.Expires = time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Minute)
				}
				latest, _ = json.Marshal(map[string]subscription.GrokAuth{subscription.GrokScope: auth})
			}
			reads := 0
			read := func() ([]byte, error) {
				reads++
				if mode == "uncertain" {
					return nil, subscription.InvalidGrok()
				}
				return bytes.Clone(latest), nil
			}
			succeeded := mode == "unchanged" || mode == "rotated"
			first, second := capture.capture(read), capture.capture(read)
			if (first == nil) != succeeded || (second == nil) != succeeded || reads != 1 {
				t.Fatal("capture retried or accepted incomplete identity/rotation")
			}
			raw, err := capture.take()
			defer clear(raw)
			if (err == nil) != succeeded || succeeded && !bytes.Equal(raw, latest) {
				t.Fatal("original captured bundle changed before protected handoff")
			}
			if again, err := capture.take(); err == nil || len(again) != 0 {
				t.Fatal("original protected handoff was duplicated")
			}
			if err := capture.capture(func() ([]byte, error) { t.Error("native capture reopened after closure"); return nil, nil }); (err == nil) != succeeded {
				t.Fatal("closed original capture outcome changed")
			}
		})
	}
}
