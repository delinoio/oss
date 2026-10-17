// SPDX-License-Identifier: Apache-2.0
package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/delinoio/oss/servers/delcore-api/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignature(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	secret := "test-secret"
	body := []byte(`{"event_id":"evt_1"}`)
	timestamp := fmt.Sprint(now.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + ":"))
	mac.Write(body)
	header := "ts=" + timestamp + ";h1=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifySignature(secret, header, body, now) {
		t.Fatal("valid signature rejected")
	}
	for _, bad := range []string{header + ";ts=" + timestamp, strings.Replace(header, "h1=", "h1=00", 1), "ts=bad;h1=bad"} {
		if VerifySignature(secret, bad, body, now) {
			t.Fatal("invalid signature accepted")
		}
	}
	if VerifySignature(secret, header, body, now.Add(6*time.Minute)) || VerifySignature(secret, header, append(body, ' '), now) {
		t.Fatal("replay or altered body accepted")
	}
}
func TestProviderFailures(t *testing.T) {
	for _, status := range []int{400, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing auth")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			c := New(config.Config{PaddleURL: server.URL, PaddleToken: "fixture", TimeoutSeconds: 1, MaxPayloadBytes: 1024})
			_, e := c.Request(context.Background(), "POST", "/transactions", map[string]string{})
			want := ErrPermanent
			if status == 429 {
				want = ErrTransient
			}
			if status == 500 {
				want = ErrAmbiguous
			}
			if e != want {
				t.Fatalf("%v != %v", e, want)
			}
		})
	}
}
func TestEmailDeliveryResult(t *testing.T) {
	for _, tc := range []struct {
		body string
		want error
	}{{`{"success":true,"result":{"queued":["recipient@example.test"]}}`, nil}, {`{"success":true,"result":{"permanent_bounces":["recipient@example.test"]}}`, ErrPermanent}, {`{"success":true,"result":{}}`, ErrTransient}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/accounts/account/email/sending/send" {
				t.Error(r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, tc.body)
		}))
		c := New(config.Config{EmailURL: server.URL, EmailAccount: "account", EmailToken: "fixture", EmailSender: "sender@example.test", TimeoutSeconds: 1, MaxPayloadBytes: 1024})
		e := c.SendEmail(context.Background(), "recipient@example.test", "Test", "Text")
		server.Close()
		if e != tc.want {
			t.Fatalf("%v != %v", e, tc.want)
		}
	}
}
