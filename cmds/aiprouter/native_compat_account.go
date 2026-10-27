// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

var errNativeAccount = errors.New("native Codex account identity could not be verified")

// ObserveNativeCodexAccount reads native login state only for a separately
// selected trial. Credentials go directly to the pinned HTTPS account endpoint,
// never to the router or the compatibility proxy. Local JWT claims are hints,
// not authentication; the authoritative usage response must match them.
func ObserveNativeCodexAccount(ctx context.Context, req NativeRuntimeRequest) (NativeCompatibilityAccount, error) {
	if runtime.GOOS != "windows" || req.Mode != NativeCompatibilityCodexAccountUITrial || !req.Trial || req.Build != NativeCompatibilityCodexBuild {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	directory := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if directory == "" {
		directory = filepath.Join(home, ".codex")
	} else if directory == "~" {
		directory = home
	} else if strings.HasPrefix(directory, "~/") || strings.HasPrefix(directory, `~\`) {
		directory = filepath.Join(home, directory[2:])
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	return observeNativeCodexAccount(ctx, filepath.Join(directory, "auth.json"), transport)
}

type nativeAccountCredential struct {
	account NativeCompatibilityAccount
	access  string
	info    os.FileInfo
}

func readNativeAccountCredential(path string) (nativeAccountCredential, error) {
	var result nativeAccountCredential
	file, err := os.Open(path)
	if err != nil {
		return result, errNativeAccount
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1048576 {
		return result, errNativeAccount
	}
	data, err := io.ReadAll(io.LimitReader(file, 1048577))
	if err != nil || len(data) > 1048576 {
		return result, errNativeAccount
	}
	after, err := file.Stat()
	if err != nil || !sameNativeAccountFile(before, after) {
		return result, errNativeAccount
	}
	current, err := os.Stat(path)
	if err != nil || !sameNativeAccountFile(after, current) {
		return result, errNativeAccount
	}
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			ID      string `json:"id_token"`
			Access  string `json:"access_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.Mode != "chatgpt" || auth.Tokens.Access == "" || auth.Tokens.Account == "" {
		return result, errNativeAccount
	}
	parts := strings.Split(auth.Tokens.ID, ".")
	if len(parts) != 3 {
		return result, errNativeAccount
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return result, errNativeAccount
	}
	var jwt struct {
		Auth struct {
			Account string `json:"chatgpt_account_id"`
			User    string `json:"chatgpt_user_id"`
			Plan    string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(claims, &jwt) != nil || jwt.Auth.Account != auth.Tokens.Account || jwt.Auth.User == "" || (jwt.Auth.Plan != "plus" && jwt.Auth.Plan != "pro") {
		return result, errNativeAccount
	}
	// A digest binds the complete native credential snapshot without persisting its
	// tokens. File metadata also fences replacement and same-content rewrites.
	h := sha256.New()
	h.Write(data)
	h.Write([]byte(after.ModTime().UTC().Format(time.RFC3339Nano)))
	h.Write(nativeAccountFileIdentity(after))
	result.account = NativeCompatibilityAccount{ID: jwt.Auth.Account, UserID: jwt.Auth.User, Plan: jwt.Auth.Plan, Structure: "personal", CredentialGeneration: hex.EncodeToString(h.Sum(nil))}
	result.access = auth.Tokens.Access
	result.info = after
	return result, nil
}
func sameNativeAccountFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}
func observeNativeCodexAccount(ctx context.Context, path string, transport http.RoundTripper) (NativeCompatibilityAccount, error) {
	if transport == nil {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	before, err := readNativeAccountCredential(path)
	if err != nil {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", nil)
	if err != nil {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	req.Header.Set("Authorization", "Bearer "+before.access)
	req.Header.Set("ChatGPT-Account-ID", before.account.ID)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(data) > 65536 {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	var usage struct {
		Account   string `json:"account_id"`
		User      string `json:"user_id"`
		Plan      string `json:"plan_type"`
		Structure string `json:"account_structure"`
	}
	if json.Unmarshal(data, &usage) != nil || usage.Account != before.account.ID || usage.User != before.account.UserID || usage.Plan != before.account.Plan || usage.Structure != "" && usage.Structure != "personal" {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	after, err := readNativeAccountCredential(path)
	if err != nil || before.account != after.account || before.access != after.access || !sameNativeAccountFile(before.info, after.info) {
		return NativeCompatibilityAccount{}, errNativeAccount
	}
	return before.account, nil
}

// Include stable OS identity fields without access time: reading auth.json must
// not itself rotate the binding. Unix supplies inode/change time; Windows
// supplies file creation time in Win32FileAttributeData.
func nativeAccountFileIdentity(info os.FileInfo) []byte {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil
	}
	fields := map[string]any{}
	for _, name := range []string{"Dev", "Ino", "Ctim", "Ctimespec", "CreationTime"} {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			fields[name] = field.Interface()
		}
	}
	data, _ := json.Marshal(fields)
	return data
}
