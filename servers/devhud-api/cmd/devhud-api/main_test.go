package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"testing"
)

func TestDevelopmentPortConflictFailsWithoutRemapping(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:46307")
	if err != nil {
		t.Skipf("fixed port is already occupied: %v", err)
	}
	defer listener.Close()
	setServeEnvironment(t)
	var logs bytes.Buffer
	err = run(context.Background(), []string{"serve"}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err == nil {
		t.Fatal("run succeeded while fixed port was occupied")
	}
	if alternative, probeErr := net.Listen("tcp", "127.0.0.1:46308"); probeErr == nil {
		_ = alternative.Close()
	}
}

func setServeEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DEVHUD_ENVIRONMENT", "development")
	t.Setenv("DEVHUD_DATABASE_URL", "postgres://unused")
	t.Setenv("DEVHUD_PUBLIC_API_URL", "http://localhost:46307")
	t.Setenv("DEVHUD_LOGTO_ISSUER", "http://localhost:3001")
	t.Setenv("DEVHUD_LOGTO_AUDIENCE", "audience")
	t.Setenv("DEVHUD_LOGTO_DESKTOP_CLIENT_ID", "desktop")
	t.Setenv("DEVHUD_LOGTO_IOS_CLIENT_ID", "ios")
	t.Setenv("DEVHUD_LOGTO_ANDROID_CLIENT_ID", "android")
	t.Setenv("DEVHUD_LOGTO_ADMIN_CLIENT_ID", "admin")
	t.Setenv("DEVHUD_ADMIN_REDIRECT_URI", "http://localhost:46306/auth/callback")
	t.Setenv("DEVHUD_PUBLIC_ASSET_BASE_URL", "https://assets.example.com")
	t.Setenv("DEVHUD_IDENTITY_HMAC_KEYS", base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")))
}
