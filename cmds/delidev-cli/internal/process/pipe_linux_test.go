package process

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func init() {
	if len(os.Args) != 5 || os.Args[1] != "__delidev_pipe_failure_fixture" {
		return
	}
	failAt, _ := strconv.Atoi(os.Args[4])
	calls := 0
	os.Exit(superviseProcessWithPipe(os.Args[2], os.Args[3], func() (*os.File, *os.File, error) {
		calls++
		if calls == failAt {
			return nil, nil, syscall.EMFILE
		}
		return os.Pipe()
	}))
}

func TestOutputPipeFailureCompletesUnlaunchedScope(t *testing.T) {
	for _, failAt := range []int{2, 3} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private")
			if err := security.PrivateDir(dir); err != nil {
				t.Fatal(err)
			}
			boot, err := bootIdentity()
			if err != nil {
				t.Fatal(err)
			}
			scope := processScope{Version: 1, OwnerID: domain.NewID(), Boot: boot}
			if err := saveScope(dir, scope); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(dir, "control.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_ = listener.SetDeadline(time.Now().Add(10 * time.Second))
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "__delidev_pipe_failure_fixture", dir, socket, strconv.Itoa(failAt))
			child.Env = []string{"PATH=/usr/bin:/bin"}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Process.Kill()
			conn, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			var ready processFrame
			if err := json.NewDecoder(conn).Decode(&ready); err != nil || ready.Kind != processReady {
				t.Fatal("supervisor readiness failed", err)
			}
			marker := filepath.Join(dir, "launched")
			command := processCommand{Path: "/bin/sh", Args: []string{"sh", "-c", `printf launched > "$1"`, "sh", marker}, Env: []string{"PATH=/usr/bin:/bin"}, Dir: dir}
			if err := json.NewEncoder(conn).Encode(command); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err == nil || child.ProcessState.ExitCode() != 3 {
				t.Fatal("injected pipe failure was not reached", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("native command launched despite pipe failure", err)
			}
			saved, err := readScope(dir)
			if err != nil || !saved.Started || !saved.Complete || saved.OwnerID != scope.OwnerID {
				t.Fatal("unlaunched scope retained incomplete ownership", err)
			}
			if err := ReconcileProcess(saved.Owner); err != nil {
				t.Fatal("dead supervisor requires recovery despite no native launch", err)
			}
		})
	}
}
