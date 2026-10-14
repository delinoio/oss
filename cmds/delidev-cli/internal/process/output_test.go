// SPDX-License-Identifier: Apache-2.0
package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type outputWriterFunc func([]byte) (int, error)

func (f outputWriterFunc) Write(raw []byte) (int, error) { return f(raw) }

func TestTerminalOutputDrainAfterDeliveryFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		canceled bool
	}{
		{name: "writer failure"},
		{name: "writer cancellation", canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, source := io.Pipe()
			defer reader.Close()
			defer source.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deliveryError := errors.New("delivery failed")
			entered := make(chan struct{})
			failed := make(chan struct{})
			completed := make(chan error, 1)
			writer := outputWriterFunc(func([]byte) (int, error) {
				close(entered)
				if test.canceled {
					<-ctx.Done()
					return 0, ctx.Err()
				}
				return 0, deliveryError
			})
			go func() {
				completed <- copyNativeOutput(reader, writer, func() { close(failed) })
			}()
			writePipe(t, source, []byte("before failure"))
			awaitOutput(t, entered)
			if test.canceled {
				cancel()
				deliveryError = context.Canceled
			}
			awaitOutput(t, failed)
			// The next write can finish only when the same reader keeps draining.
			writePipe(t, source, []byte("after failure"))
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-completed:
				if !errors.Is(err, deliveryError) {
					t.Fatalf("delivery error = %v, want %v", err, deliveryError)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("output drain did not finish after native pipe closure")
			}
		})
	}
}

func TestTerminalOutputDeliverySuccess(t *testing.T) {
	var output bytes.Buffer
	if err := copyNativeOutput(bytes.NewBufferString("terminal output"), &output, func() {
		t.Fatal("successful delivery terminated the native process")
	}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "terminal output" {
		t.Fatalf("delivered %q", output.String())
	}
}

func writePipe(t *testing.T, pipe *io.PipeWriter, raw []byte) {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := pipe.Write(raw)
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native pipe write remained blocked")
	}
}

func awaitOutput(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("output pump did not reach the expected stage")
	}
}
