// SPDX-License-Identifier: Apache-2.0
// Trajectory policy adapted from the pinned source in LICENSE-MIT.
package devin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

var _ inference.ExecutingAdapter = (*Adapter)(nil)

// Execute sends exactly one main operation through the enclosing executor. Core
// retains authority over recovery, credential rotation and the logical budget.
func (a *Adapter) Execute(ctx context.Context, in inference.AdapterInput, x inference.AdapterExecutor, store inference.AdapterStateStore) (*http.Response, inference.Wire, error) {
	if x == nil {
		return nil, inference.Chat, &inference.Error{Status: 500, Code: "devin_executor_required"}
	}
	s := attempt(ctx)
	if s == nil {
		return nil, inference.Chat, &inference.Error{Status: 500, Code: "devin_attempt_context_required"}
	}
	s.mu.Lock()
	err := s.bind(in)
	cascade := s.cascade
	s.mu.Unlock()
	if err != nil {
		return nil, inference.Chat, err
	}
	release := func() error { return nil }
	if in.Operation == inference.Generate {
		trajectory, done, err := claimTrajectory(ctx, in, cascade, store)
		if err != nil {
			return nil, inference.Chat, err
		}
		release = done
		s.mu.Lock()
		s.options.trajectoryID = trajectory
		s.mu.Unlock()
	}
	owned := true
	defer func() {
		if owned {
			_ = release()
		}
	}()
	request, err := a.Build(ctx, in)
	if err != nil {
		return nil, inference.Chat, err
	}
	response, err := x.Do(ctx, request)
	if err != nil {
		return nil, request.ResponseWire, err
	}
	markUnknown := func() {
		if accounting, ok := x.(inference.AttemptAccounting); ok {
			accounting.RecordUnmeasuredAttempt()
		}
	}
	if response == nil || response.Body == nil {
		markUnknown()
		return nil, request.ResponseWire, errWire
	}
	// Mock/service executors may not preserve Request. Operation is authoritative;
	// attach the actual built URL so the existing unary search decoder is selected.
	if response.Request == nil {
		response.Request, _ = http.NewRequest(request.Method, request.URL, nil)
	}
	if in.Operation == inference.Generate && response.StatusCode >= 200 && response.StatusCode < 300 {
		response, err = inspectConnect(ctx, response)
		if err != nil {
			markUnknown()
			return nil, request.ResponseWire, err
		}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		original := response
		response, err = a.DecodeWithInput(ctx, in, response)
		if err != nil {
			_ = original.Body.Close()
			markUnknown()
			return nil, request.ResponseWire, err
		}
	}
	response.Body = newLeaseBody(ctx, response.Body, release)
	owned = false
	return response, request.ResponseWire, nil
}

type trajectoryState struct {
	Version    int    `json:"version"`
	Trajectory string `json:"trajectory"`
	Token      string `json:"token,omitempty"`
}

var errLeaseChanged = errors.New("devin trajectory claim no longer current")

func claimTrajectory(ctx context.Context, in inference.AdapterInput, cascade string, store inference.AdapterStateStore) (string, func() error, error) {
	fresh, err := uuid()
	if err != nil {
		return "", nil, err
	}
	noop := func() error { return nil }
	var request object
	if json.Unmarshal(in.Body, &request) == nil && request["store"] == false {
		return fresh, noop, nil
	}
	updater, ok := store.(inference.AdapterStateUpdater)
	if !ok {
		return fresh, noop, nil
	}
	own := strings.TrimSpace(in.ClientHeaders.Get("thread-id"))
	if own == "" {
		own = strings.TrimSpace(in.ClientHeaders.Get("session-id"))
	}
	if own == "" {
		own = strings.TrimSpace(in.ClientHeaders.Get("session_id"))
	}
	parent := strings.TrimSpace(in.ClientHeaders.Get("x-codex-parent-thread-id"))
	keyParts := []string{"cascade", cascade}
	if own != "" {
		keyParts = []string{"conversation", parent, own}
	}
	if own == "" && cascade == "" {
		return fresh, noop, nil
	}
	encoded, _ := json.Marshal(keyParts)
	sum := sha256.Sum256(encoded)
	key := "trajectory:" + hex.EncodeToString(sum[:])
	token, err := uuid()
	if err != nil {
		return "", nil, err
	}
	trajectory, acquired := fresh, false
	err = updater.Update(ctx, key, StateTTL, func(current []byte) ([]byte, error) {
		// A missing/expired record always allocates a NEW UUID. An old active stream
		// can therefore never share its trajectory with a replacement after expiry.
		entry := trajectoryState{Version: 1, Trajectory: fresh}
		if len(current) > 0 {
			if len(current) > StateMaxEntryBytes || json.Unmarshal(current, &entry) != nil || entry.Version != 1 || !uuidShape.MatchString(entry.Trajectory) || (entry.Token != "" && !uuidShape.MatchString(entry.Token)) {
				return nil, inference.ErrContinuation
			}
			if entry.Token != "" {
				trajectory, acquired = fresh, false
				return current, nil
			}
		}
		trajectory, acquired = entry.Trajectory, true
		entry.Token = token
		return json.Marshal(entry)
	})
	if errors.Is(err, inference.ErrUnsupported) {
		return fresh, noop, nil
	}
	if err != nil {
		return "", nil, err
	}
	if !acquired {
		return fresh, noop, nil
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			// Cancellation must still release a claim, but never bypass the updater's
			// current-key, membership or deletion checks. Cleanup is independently bounded.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			releaseErr = updater.Update(cleanup, key, StateTTL, func(current []byte) ([]byte, error) {
				var entry trajectoryState
				if len(current) == 0 {
					return nil, errLeaseChanged
				}
				if len(current) > StateMaxEntryBytes || json.Unmarshal(current, &entry) != nil || entry.Version != 1 {
					return nil, inference.ErrContinuation
				}
				if entry.Token != token || entry.Trajectory != trajectory {
					return nil, errLeaseChanged
				}
				entry.Token = ""
				return json.Marshal(entry)
			})
			if errors.Is(releaseErr, errLeaseChanged) {
				releaseErr = nil
			}
		})
		return releaseErr
	}
	return trajectory, release, nil
}

type leaseBody struct {
	io.ReadCloser
	once    sync.Once
	stop    func() bool
	release func() error
	err     error
}

func newLeaseBody(ctx context.Context, body io.ReadCloser, release func() error) *leaseBody {
	b := &leaseBody{ReadCloser: body, release: release}
	// The callback avoids reading stop while AfterFunc is still installing it.
	// sync.Once makes EOF, Close and cancellation wait for the same cleanup.
	b.stop = context.AfterFunc(ctx, func() { b.finish() })
	return b
}
func (b *leaseBody) finish() {
	b.once.Do(func() {
		closeErr := b.ReadCloser.Close()
		b.err = b.release()
		if b.err == nil {
			b.err = closeErr
		}
	})
}
func (b *leaseBody) Close() error { b.stop(); b.finish(); return b.err }
func (b *leaseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		closeErr := b.Close()
		if err == io.EOF && closeErr != nil {
			err = closeErr
		}
	}
	return n, err
}

type prefixBody struct {
	io.Reader
	io.Closer
}

// Inspect only a bounded pre-output prefix. Any emitted event (including usage)
// ends inspection. Buffered bytes are replayed locally exactly once, never sent.
func inspectConnect(ctx context.Context, response *http.Response) (*http.Response, error) {
	const prefixLimit = 64 << 10
	source := &rawIdleBody{ReadCloser: response.Body, idle: 120 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = source.Close() })
	defer stop()
	var prefix bytes.Buffer
	resume := func() (*http.Response, error) {
		response.Body = &prefixBody{Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), source), Closer: source}
		return response, nil
	}
	fail := func(err error) (*http.Response, error) {
		_ = source.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if source.timedOut.Load() {
			return nil, &inference.Error{Status: 504, Code: "devin_stream_idle_timeout"}
		}
		return nil, err
	}
	for count := 0; count < 32 && prefix.Len()+5 <= prefixLimit; count++ {
		var h [5]byte
		n, err := io.ReadFull(source, h[:])
		prefix.Write(h[:n])
		if err != nil {
			return fail(&inference.Error{Status: 502, Code: "devin_truncated_stream"})
		}
		size := int64(binary.BigEndian.Uint32(h[1:]))
		if size > int64(prefixLimit-prefix.Len()) {
			return resume()
		}
		start := prefix.Len() - 5
		if _, err = io.CopyN(&prefix, source, size); err != nil {
			return fail(&inference.Error{Status: 502, Code: "devin_truncated_stream"})
		}
		flags, body, err := readFrame(bytes.NewReader(prefix.Bytes()[start:]))
		if err != nil {
			return fail(err)
		}
		if flags&2 != 0 {
			err = trailer(body)
			if err == nil {
				return resume()
			}
			var refusal *inference.Error
			if !errors.As(err, &refusal) {
				return fail(err)
			}
			_ = source.Close()
			sanitized, _ := json.Marshal(object{"error": object{"code": refusal.Code}})
			clone := new(http.Response)
			*clone = *response
			clone.Header = response.Header.Clone()
			clone.Header.Del("Content-Encoding")
			clone.Header.Set("Content-Type", "application/json")
			clone.StatusCode = refusal.Status
			clone.Status = http.StatusText(refusal.Status)
			clone.ContentLength = int64(len(sanitized))
			clone.Body = io.NopCloser(bytes.NewReader(sanitized))
			return clone, nil
		}
		var emitted bytes.Buffer
		probe := chatStream{writer: &emitted, tools: map[string]int{}, names: map[string]string{}, usage: object{}}
		if err = probe.decodeFrame(body); err != nil || emitted.Len() > 0 {
			return resume()
		}
	}
	return resume()
}
