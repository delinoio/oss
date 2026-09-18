package runmoor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PowerController interface {
	Set(bool) *Problem
	Release()
}

type PowerManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	input   io.WriteCloser
	done    chan error
	retry   time.Time
	failure *Problem
}

func powerCommand() (string, []string) {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/caffeinate", []string{"-i", "-w", strconv.Itoa(os.Getpid())}
	}
	return "systemd-inhibit", []string{"--what=sleep", "--mode=block", "--who=Runmoor", "--why=Active ephemeral runners", "cat"}
}
func (p *PowerManager) Set(active bool) *Problem {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !active {
		p.release()
		p.failure = nil
		return nil
	}
	if p.cmd != nil {
		select {
		case <-p.done:
			p.cmd = nil
			p.failure = problem(ErrPower, "The OS sleep inhibitor exited.", "Check user-session power permissions; jobs continue without guaranteed sleep inhibition.")
			p.retry = time.Now().Add(time.Minute)
		default:
			return nil
		}
	}
	if time.Now().Before(p.retry) {
		return p.failure
	}
	name, args := powerCommand()
	cmd := exec.Command(name, args...)
	cmd.Env = minimalEnv()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	detach(cmd)
	pipe, e := cmd.StdinPipe()
	if e == nil {
		e = cmd.Start()
	}
	if e != nil {
		if pipe != nil {
			pipe.Close()
		}
		p.failure = problem(ErrPower, "Cannot acquire OS sleep inhibition.", "Check caffeinate or systemd-inhibit availability and user-session permissions; work continues.")
		p.retry = time.Now().Add(time.Minute)
		return p.failure
	}
	p.cmd = cmd
	p.input = pipe
	p.done = make(chan error, 1)
	go func() { p.done <- cmd.Wait() }()
	p.failure = nil
	return nil
}
func (p *PowerManager) release() {
	if p.input != nil {
		p.input.Close()
		p.input = nil
	}
	if p.cmd != nil {
		interruptProcess(p.cmd)
		select {
		case <-p.done:
		case <-time.After(time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
		p.cmd = nil
	}
}
func (p *PowerManager) Release() { p.mu.Lock(); defer p.mu.Unlock(); p.release() }
func platformCheck(ctx context.Context) error {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return problem(ErrPlatform, "Unsupported host CPU architecture.", "Use Ubuntu amd64/arm64 or macOS arm64.")
	}
	if runtime.GOOS == "darwin" {
		if runtime.GOARCH != "arm64" {
			return problem(ErrPlatform, "Intel Macs are unsupported.", "Use Apple Silicon with macOS 14 or later.")
		}
		b, e := (OSCommand{}).Run(ctx, "/usr/bin/sw_vers", []string{"-productVersion"}, minimalEnv(), nil)
		major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(string(b)), ".")[0])
		if e != nil || major < 14 {
			return problem(ErrPlatform, "macOS 14 or later is required.", "Upgrade the host operating system.")
		}
		return nil
	}
	if runtime.GOOS == "linux" {
		b, e := os.ReadFile("/etc/os-release")
		if e != nil {
			return problem(ErrPlatform, "Cannot identify the Linux distribution.", "Use Ubuntu 22.04 or later.")
		}
		values := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				values[parts[0]] = strings.Trim(parts[1], `"`)
			}
		}
		version, _ := strconv.ParseFloat(values["VERSION_ID"], 64)
		if values["ID"] != "ubuntu" || version < 22.04 {
			return problem(ErrPlatform, "This preview supports Ubuntu 22.04 or later.", "Use a supported Ubuntu host.")
		}
		return nil
	}
	return problem(ErrPlatform, "This operating system is unsupported.", "Use Ubuntu amd64/arm64 or macOS arm64.")
}
func diagnosticDir(c Config) string { return filepath.Join(c.Storage.State, "diagnostics") }
