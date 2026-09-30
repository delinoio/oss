package userservice

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

type nativeBackend struct{}
type boundedOutput struct {
	bytes.Buffer
	full bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		b.full = true
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
func nativeCommand(ctx context.Context, name string, args []string, env []string, input []byte) ([]byte, error) {
	return nativeCommandTimeout(ctx, name, args, env, input, 5*time.Second)
}

func nativeCommandTimeout(ctx context.Context, name string, args []string, env []string, input []byte, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = io.Discard
	var out boundedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if out.full {
		return nil, unavailable()
	}
	return out.Bytes(), nil
}
func nativeEnv(platform string) []string {
	if platform == "windows" {
		return []string{"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("WINDIR")}
	}
	env := []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	if platform == "linux" {
		// systemctl --user receives only the existing user-bus lookup context.
		// Never autolaunch a bus, enable linger or inherit product/credential values.
		for _, key := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
			if v, ok := os.LookupEnv(key); ok {
				env = append(env, key+"="+v)
			}
		}
	}
	return env
}
