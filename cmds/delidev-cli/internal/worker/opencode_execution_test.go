package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestOpenCodeGeneralRootRejectsEnclosingGitMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows non-VCS native root needs its separate profile")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(parent, "worker", "session")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := workspace.Manifest{Type: domain.GeneralChat, PrimaryPath: cwd}
	root, err := openCodeWorkspaceRoot(manifest, cwd)
	if err != nil || root != string(filepath.Separator) {
		t.Fatal("non-VCS root was not independently resolved", err)
	}
	for _, at := range []string{parent, cwd} {
		for _, directory := range []bool{false, true} {
			path := filepath.Join(at, ".git")
			if directory {
				err = os.Mkdir(path, 0700)
			} else {
				err = os.WriteFile(path, []byte("gitdir: arbitrary"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := openCodeWorkspaceRoot(manifest, cwd); err == nil {
				t.Fatal("enclosing Git authority was silently replaced by global root")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := openCodeWorkspaceRoot(manifest, parent); err == nil {
		t.Fatal("foreign cwd supplied root authority")
	}
	manifest.Repositories = make([]workspace.PreparedRepository, 2)
	if _, err := openCodeWorkspaceRoot(manifest, cwd); err == nil {
		t.Fatal("extra repositories were silently omitted")
	}
}

func TestOpenCodeControlsJoinOnceAndKeepTargetedStopLifetime(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	native, cancelNative := context.WithCancel(context.Background())
	defer cancelNative()
	var revoked atomic.Int64
	finish := startOpenCodeControls(ctx, native, func() { revoked.Add(1); cancelNative() }, Config{}, nil)
	stop()
	for i := 0; i < 3; i++ {
		if err := finish(); err != nil {
			t.Fatal(err)
		}
	}
	if native.Err() != nil || revoked.Load() != 0 {
		t.Fatal("targeted Stop revoked independent native grace")
	}
}

func TestOpenCodeUnsupportedControlRevokesOriginalLifetime(t *testing.T) {
	for _, kind := range []string{"steer", "question-stream", "approval-stream"} {
		t.Run(kind, func(t *testing.T) {
			native, cancelNative := context.WithCancel(context.Background())
			defer cancelNative()
			config := Config{}
			switch kind {
			case "steer":
				ch := make(chan *pb.SteerInputControl, 1)
				ch <- &pb.SteerInputControl{}
				config.steerControls = ch
			case "question-stream":
				ch := make(chan *pb.QuestionResponseControl)
				close(ch)
				config.questionControls = ch
			case "approval-stream":
				ch := make(chan *pb.ApprovalResponseControl)
				close(ch)
				config.approvalControls = ch
			}
			finish := startOpenCodeControls(context.Background(), native, cancelNative, config, nil)
			<-native.Done()
			first := finish()
			if first == nil || finish() != first {
				t.Fatal("control failure lost its stable joined result")
			}
		})
	}
}
