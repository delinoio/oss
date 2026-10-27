// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nativeAccountTransport func(*http.Request) (*http.Response, error)

func (f nativeAccountTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func writeNativeAccount(t *testing.T, path, token, plan string) {
	t.Helper()
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "account", "chatgpt_user_id": "user", "chatgpt_plan_type": plan}})
	data, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{"id_token": "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature", "access_token": token, "account_id": "account"}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestNativeAccountAuthoritativeBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeNativeAccount(t, path, "native-secret", "plus")
	transport := nativeAccountTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://chatgpt.com/backend-api/wham/usage" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer native-secret" || r.Header.Get("ChatGPT-Account-ID") != "account" {
			t.Fatal("credential sent to wrong endpoint")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("unbounded verification")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"account_id":"account","user_id":"user","plan_type":"plus"}`)), Header: make(http.Header)}, nil
	})
	first, err := observeNativeCodexAccount(context.Background(), path, transport)
	if err != nil || first.ID != "account" || first.Structure != "personal" || len(first.CredentialGeneration) != 64 {
		t.Fatalf("account binding failed: %v", err)
	}
	second, err := observeNativeCodexAccount(context.Background(), path, transport)
	if err != nil || first != second {
		t.Fatal("unchanged credentials changed binding")
	}
	writeNativeAccount(t, path, "rotated-secret", "plus")
	rotated, err := readNativeAccountCredential(path)
	if err != nil || rotated.account.CredentialGeneration == first.CredentialGeneration {
		t.Fatal("credential rotation not fenced")
	}
}
func TestNativeAccountRefusals(t *testing.T) {
	for _, test := range []string{"account", "user", "plan", "structure", "redirect", "oversize", "rotation", "unsupported-plan", "invalid-json"} {
		t.Run(test, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			writeNativeAccount(t, path, "native-secret", "plus")
			if test == "unsupported-plan" {
				writeNativeAccount(t, path, "native-secret", "team")
			}
			if test == "invalid-json" {
				os.WriteFile(path, []byte("invalid"), 0600)
			}
			calls := 0
			transport := nativeAccountTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `{"account_id":"account","user_id":"user","plan_type":"plus"}`
				status := 200
				header := make(http.Header)
				switch test {
				case "account":
					body = strings.Replace(body, `"account"`, `"other"`, 1)
				case "user":
					body = strings.Replace(body, `"user"`, `"other"`, 1)
				case "plan":
					body = strings.Replace(body, "plus", "pro", 1)
				case "structure":
					body = `{"account_id":"account","user_id":"user","plan_type":"plus","account_structure":"team"}`
				case "redirect":
					status = 302
					header.Set("Location", "https://attacker.invalid/collect")
				case "oversize":
					body = strings.Repeat(" ", 65537)
				case "rotation":
					writeNativeAccount(t, path, "rotated-secret", "plus")
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			account, err := observeNativeCodexAccount(context.Background(), path, transport)
			if err == nil || account != (NativeCompatibilityAccount{}) || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid account accepted or secret exposed")
			}
			if calls > 1 {
				t.Fatal("followed redirect")
			}
			if (test == "unsupported-plan" || test == "invalid-json") && calls != 0 {
				t.Fatal("invalid native login made request")
			}
		})
	}
}
