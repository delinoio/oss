package worker

import (
	"context"
	"sync/atomic"
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestClaudeControlsJoinOnceAndKeepTargetedStopLifetime(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	native, cancelNative := context.WithCancel(context.Background())
	defer cancelNative()
	var revoked atomic.Int64
	finish := startClaudeControls(ctx, native, func() { revoked.Add(1); cancelNative() }, Config{}, nil, nil)
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

func TestClaudeUnsupportedControlRevokesOriginalLifetime(t *testing.T) {
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
			finish := startClaudeControls(context.Background(), native, cancelNative, config, nil, nil)
			<-native.Done()
			first := finish()
			if first == nil || finish() != first {
				t.Fatal("control failure lost its stable joined result")
			}
		})
	}
}
