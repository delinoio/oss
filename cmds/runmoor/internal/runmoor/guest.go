package runmoor

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type GuestInput struct {
	ID            string `json:"id"`
	RunnerPath    string `json:"runner_path"`
	RunnerVersion string `json:"runner_version"`
	JIT           string `json:"jit"`
}
type GuestStatus struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	Finished  bool      `json:"finished"`
	ExitCode  int       `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
}

func guestStatePath(id string) string {
	return filepath.Join(os.TempDir(), "runmoor", id, "status.json")
}
func guestWrite(v GuestStatus) error {
	path := guestStatePath(v.ID)
	if e := privateDir(filepath.Dir(path)); e != nil {
		return e
	}
	b, _ := json.Marshal(v)
	f, e := openPrivate(path+".new", os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return e
	}
	return os.Rename(path+".new", path)
}
func guestExecute(command string, args []string, out io.Writer) int {
	if command == "__guest-status" {
		if len(args) != 1 || !validID(args[0]) {
			return 2
		}
		b, e := readPrivate(guestStatePath(args[0]), 4096)
		if e != nil {
			return 1
		}
		_, e = out.Write(b)
		if e != nil {
			return 1
		}
		return 0
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if e != nil {
		return 1
	}
	var in GuestInput
	if json.Unmarshal(b, &in) != nil || !validID(in.ID) || !filepath.IsAbs(in.RunnerPath) || !versionPattern.MatchString(in.RunnerVersion) || in.JIT == "" || strings.ContainsAny(in.JIT, "\x00\n\r") {
		return 2
	}
	if command == "__guest-bootstrap" {
		if _, e = os.Stat(guestStatePath(in.ID)); e == nil {
			return 1
		}
		exe, e := os.Executable()
		if e != nil {
			return 1
		}
		cmd := exec.Command(exe, "__guest-runner")
		cmd.Env = minimalEnv()
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		detach(cmd)
		pipe, e := cmd.StdinPipe()
		if e != nil {
			return 1
		}
		if e = cmd.Start(); e != nil {
			return 1
		}
		if _, e = pipe.Write(b); e != nil {
			return 1
		}
		pipe.Close()
		for i := 0; i < 100; i++ {
			if _, e = os.Stat(guestStatePath(in.ID)); e == nil {
				return 0
			}
			time.Sleep(50 * time.Millisecond)
		}
		return 1
	}
	if command != "__guest-runner" {
		return 2
	}
	v := GuestStatus{ID: in.ID, PID: os.Getpid(), StartedAt: nowUTC()}
	if guestWrite(v) != nil {
		return 1
	}
	cmd := exec.Command(filepath.Join(in.RunnerPath, "run.sh"), "--jitconfig", in.JIT)
	cmd.Dir = in.RunnerPath
	cmd.Env = append(minimalEnv(), "RUNNER_MANUALLY_TRAP_SIG=1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	e = cmd.Run()
	v.Finished = true
	if e != nil {
		v.ExitCode = 1
		if exit, ok := e.(*exec.ExitError); ok {
			v.ExitCode = exit.ExitCode()
		}
	}
	if guestWrite(v) != nil {
		return 1
	}
	return 0
}
