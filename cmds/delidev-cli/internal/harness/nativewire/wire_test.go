package nativewire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func init() {
	if len(os.Args) != 3 || os.Args[1] != "__delidev_wire_fixture" {
		return
	}
	if os.Args[2] == "blocked-input" {
		for {
			time.Sleep(time.Second)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), MaxFrame+1)
	write := func(value any) { raw, _ := json.Marshal(value); _, _ = os.Stdout.Write(append(raw, '\n')) }
	for scanner.Scan() {
		var request envelope
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		switch request.Method {
		case "unicode":
			raw, _ := json.Marshal(map[string]any{"id": request.ID, "result": map[string]string{"text": "한글 😀"}})
			for _, value := range append(raw, '\n') {
				_, _ = os.Stdout.Write([]byte{value})
			}
		case "error":
			write(map[string]any{"id": request.ID, "error": map[string]any{"code": -32602, "message": "private-native-error", "data": map[string]string{"token": "secret-provider-value"}}})
		case "delayed":
			time.Sleep(200 * time.Millisecond)
			write(map[string]any{"id": request.ID, "result": map[string]bool{"accepted": true}})
		case "interactions":
			write(map[string]any{"id": 7, "method": "approval", "params": map[string]string{"command": "private-command"}})
			write(map[string]any{"method": "progress", "params": map[string]int{"step": 1}, "emittedAtMs": int64(1790213339000)})
			write(map[string]any{"id": request.ID, "result": map[string]bool{"accepted": true}})
		case "malformed":
			fmt.Fprintln(os.Stdout, `{"id":1,"id":2,"result":{}}`)
		case "both":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"result\":{},\"error\":null}\n", request.ID)
		case "invalid-error":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"message\":\"private-error\"}}\n", request.ID)
		case "oversize":
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), MaxFrame+1))
		case "events":
			for i := 0; i < maxEvents+1; i++ {
				write(map[string]any{"method": "progress", "params": map[string]int{"step": i}})
			}
		case "exit":
			os.Exit(9)
		case "":
			if string(request.ID) != "7" {
				os.Exit(4)
			}
			write(map[string]any{"method": "resolved", "params": request.Result})
		default:
			write(map[string]any{"id": request.ID, "result": map[string]bool{"ok": true}})
		}
	}
	os.Exit(0)
}
func startFixture(t *testing.T, mode string) (*Connection, process.Config, *bytes.Buffer) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	cfg := process.Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: executable, Args: []string{"__delidev_wire_fixture", mode}, Cwd: t.TempDir(), Env: []string{}, Logger: slog.New(slog.NewJSONHandler(&log, nil))}
	c, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c, cfg, &log
}
func TestNativeWirePartialFramesAndRedactedErrors(t *testing.T) {
	c, config, log := startFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := c.Call(ctx, domain.NewID(), "unicode", struct{}{})
	if err != nil || !strings.Contains(string(response.Result), "한글 😀") {
		t.Fatalf("split unicode: %s %v", response.Result, err)
	}
	response, err = c.Call(ctx, domain.NewID(), "error", struct{}{})
	if err != nil || response.ErrorCode == nil || *response.ErrorCode != -32602 {
		t.Fatalf("missing typed native error: %+v %v", response, err)
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
		t.Fatal("native error content escaped")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "private") || strings.Contains(log.String(), "secret") {
		t.Fatal("native content logged")
	}
	if err := process.ReconcileOwner(config.Directory, config.OwnerID); err != nil {
		t.Fatal(err)
	}
}
func TestNativeWireUncertainDeliveryAndLateAcknowledgment(t *testing.T) {
	c, _, _ := startFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	id := domain.NewID()
	_, err := c.Call(ctx, id, "delayed", struct{}{})
	cancel()
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("lost acknowledgment was replayable: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, id, "delayed", struct{}{}); err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("request replay allowed")
	}
	event, err := c.Next(ctx)
	if err != nil || event.Kind != LateResponse || string(event.ID) != `"`+string(id)+`"` || string(event.Response.Result) != `{"accepted":true}` {
		t.Fatalf("lost late acceptance: %+v %v", event, err)
	}
	if _, err := c.Call(ctx, domain.NewID(), "unicode", struct{}{}); err != nil {
		t.Fatal(err)
	}
}
func TestNativeWireConcurrentInteractionReplies(t *testing.T) {
	c, _, _ := startFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, domain.NewID(), "interactions", struct{}{}); err != nil {
		t.Fatal(err)
	}
	event, err := c.Next(ctx)
	if err != nil || event.Kind != ServerRequest || event.Method != "approval" {
		t.Fatalf("missing native request: %+v %v", event, err)
	}
	progress, err := c.Next(ctx)
	if err != nil || progress.Kind != Notification || progress.EmittedAtMS == nil || *progress.EmittedAtMS != 1790213339000 {
		t.Fatalf("missing notification: %+v %v", progress, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- c.Reply(ctx, event, map[string]string{"decision": "decline"}) }()
	}
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatal("native response duplicated")
	}
	resolved, err := c.Next(ctx)
	if err != nil || resolved.Method != "resolved" || string(resolved.Params) != `{"decision":"decline"}` {
		t.Fatalf("wrong native response: %+v %v", resolved, err)
	}
}
func TestNativeWireProtocolFailuresAndBoundsStopOwnedScope(t *testing.T) {
	for _, method := range []string{"malformed", "both", "invalid-error", "oversize", "events", "exit"} {
		t.Run(method, func(t *testing.T) {
			c, config, _ := startFixture(t, "normal")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := c.Call(ctx, domain.NewID(), method, struct{}{})
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("failure asserted no delivery: %v", err)
			}
			select {
			case <-c.Done():
			case <-ctx.Done():
				t.Fatal("scope did not stop")
			}
			problem := c.Err()
			if problem == nil {
				t.Fatal("missing native failure")
			}
			if (method == "oversize" || method == "events") && problem.Code != domain.ResourceExhausted {
				t.Fatalf("wrong limit classification: %v", problem)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := process.ReconcileOwner(config.Directory, config.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeWireBlockedInputIsBounded(t *testing.T) {
	c, config, _ := startFixture(t, "blocked-input")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.Call(ctx, domain.NewID(), "blocked", map[string]string{"text": strings.Repeat("x", 900<<10)})
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("blocked write result: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(config.Directory, config.OwnerID); err != nil {
		t.Fatal(err)
	}
}
func TestNativeWireCanceledBeforeWriteDoesNotConsumeIdentity(t *testing.T) {
	c, _, _ := startFixture(t, "normal")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := domain.NewID()
	_, err := c.Call(ctx, id, "unicode", struct{}{})
	if err == nil || domain.SafeError(err).Code != domain.Canceled {
		t.Fatalf("pre-send cancel: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, id, "unicode", struct{}{}); err != nil {
		t.Fatal(err)
	}
}
