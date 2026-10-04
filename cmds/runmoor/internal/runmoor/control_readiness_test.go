package runmoor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func serveControlFixture(t *testing.T, c Config, handler http.HandlerFunc) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(listener.Addr().String(), 0600); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
}

func TestControlDistinguishesRequestInterruption(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private Unix control sockets")
	}
	for _, scenario := range []string{"headers deadline", "body deadline", "cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := fixtureStore(t)
			entered := make(chan struct{})
			serveControlFixture(t, c, func(w http.ResponseWriter, r *http.Request) {
				if scenario == "body deadline" {
					_, _ = io.WriteString(w, `{"schema_version":`)
					w.(http.Flusher).Flush()
				}
				close(entered)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if scenario == "cancellation" {
				go func() { <-entered; cancel() }()
			}
			_, err := SendControl(ctx, c, ControlRequest{Action: "status"})
			requireCode(t, err, ErrControl)
			want := "timed out"
			if scenario == "cancellation" {
				want = "canceled"
			}
			if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "stale socket") || strings.Contains(err.Error(), "incompatible") {
				t.Fatalf("request interruption was reported as a manager failure: %v", err)
			}
		})
	}
}

func TestControlRetainsConnectionAndProtocolFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private Unix control sockets")
	}
	for _, protocol := range []bool{false, true} {
		t.Run(fmt.Sprintf("protocol=%t", protocol), func(t *testing.T) {
			c, _ := fixtureStore(t)
			if protocol {
				serveControlFixture(t, c, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, `{"schema_version":99}`)
				})
			} else {
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(c.Storage.State, "control.sock"), Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				listener.SetUnlinkOnClose(false)
				if err = os.Chmod(listener.Addr().String(), 0600); err != nil {
					t.Fatal(err)
				}
				if err = listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
			_, err := SendControl(context.Background(), c, ControlRequest{Action: "status"})
			requireCode(t, err, ErrControl)
			want := "stale socket"
			if protocol {
				want = "incompatible response"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatal("connection or protocol failure changed classification", err)
			}
		})
	}
}

type waitingGuestCommand struct{ *tartFixture }

func (f waitingGuestCommand) RunPinned(ctx context.Context, name string, args, env []string, in io.Reader, dir *os.File) ([]byte, error) {
	if args[0] == "exec" {
		<-ctx.Done()
		return nil, errors.New("private guest transport details")
	}
	return f.tartFixture.RunPinned(ctx, name, args, env, in, dir)
}

func TestControlGuestProbeReturnsBeforeClientDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private Unix control sockets")
	}
	m, c, _, _, _ := testManager(t)
	driver, fixture := fakeTart(c)
	m.Images.Tart = driver
	im, err := m.Images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Images.Operate(context.Background(), c, ImageRequest{Action: "open", ID: im.ID}); err != nil {
		t.Fatal(err)
	}
	driver.Exec = waitingGuestCommand{fixture}
	server, err := m.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = SendControl(ctx, c, ControlRequest{Action: "image", Image: &ImageRequest{Action: "probe", ID: im.ID}})
	requireCode(t, err, ErrPreparation)
	if ctx.Err() != nil || strings.Contains(err.Error(), "private guest") {
		t.Fatalf("guest readiness lost its bounded response or exposed transport details: %v", err)
	}
	resp, err := SendControl(ctx, c, ControlRequest{Action: "status"})
	if err != nil || !resp.Status.Running {
		t.Fatal("a slow guest incorrectly stopped the manager", err)
	}
}

func TestControlGuestProbeCancelsWhileWaitingForImageLock(t *testing.T) {
	m, _, _, _, _ := testManager(t)
	m.imageMu.Lock()
	defer m.imageMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finished := make(chan ControlResponse, 1)
	go func() {
		finished <- m.Control(ctx, ControlRequest{Action: "image", Image: &ImageRequest{Action: "probe", ID: newID()}})
	}()
	select {
	case resp := <-finished:
		requireCode(t, resp.Problem, ErrPreparation)
	case <-time.After(time.Second):
		t.Fatal("canceled readiness check remained blocked on image serialization")
	}
}
