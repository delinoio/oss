package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
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

func TestServeUntilStoppedWaitsForActiveRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseRequest)
		}
	}()
	httpServer := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		response.WriteHeader(http.StatusNoContent)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveUntilStopped(ctx, httpServer, listener, time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}()

	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + listener.Addr().String())
		if requestErr == nil {
			_, requestErr = io.Copy(io.Discard, response.Body)
			requestErr = errors.Join(requestErr, response.Body.Close())
		}
		requestDone <- requestErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not reach the handler")
	}

	cancel()
	select {
	case serveErr := <-serveDone:
		t.Fatalf("server returned before the active request drained: %v", serveErr)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequest)
	released = true
	if requestErr := <-requestDone; requestErr != nil {
		t.Fatal(requestErr)
	}
	if serveErr := <-serveDone; serveErr != nil {
		t.Fatal(serveErr)
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
