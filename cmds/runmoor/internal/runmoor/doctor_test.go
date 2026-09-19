package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const runnerReleaseFixtureSecret = "fixture-runner-response-secret"

type runnerReleaseTransport func(*http.Request) (*http.Response, error)

func (f runnerReleaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type runnerReleaseReadFunc func([]byte) (int, error)

func (f runnerReleaseReadFunc) Read(p []byte) (int, error) { return f(p) }

type runnerReleaseBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *runnerReleaseBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *runnerReleaseBody) Close() error {
	b.closed = true
	return nil
}

func runnerReleaseClient(t *testing.T, ctx context.Context, transport runnerReleaseTransport) *http.Client {
	t.Helper()
	return &http.Client{Transport: runnerReleaseTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://api.github.com/repos/actions/runner/releases?per_page=100" {
			t.Fatalf("unexpected release request: %s %s", req.Method, req.URL)
		}
		if req.Context() != ctx {
			t.Fatal("release request lost the caller context or deadline")
		}
		if req.Header.Get("Accept") != "application/vnd.github+json" || req.Header.Get("User-Agent") != "runmoor/"+Version || req.Header.Get("Authorization") != "" {
			t.Fatal("release request changed its headers or included credentials")
		}
		return transport(req)
	})}
}

type runnerReleaseFixture struct {
	Tag        string    `json:"tag_name"`
	Published  time.Time `json:"published_at"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Body       *string   `json:"body,omitempty"`
}

func runnerReleaseResponse(t *testing.T, size int) []byte {
	t.Helper()
	releases := make([]runnerReleaseFixture, 100)
	for i := range releases {
		releases[i] = runnerReleaseFixture{
			Tag:       fmt.Sprintf("v2.%d.0", 337-i),
			Published: time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
		}
	}
	releases[0].Published = time.Date(2026, time.August, 26, 14, 33, 29, 0, time.UTC)
	body := ""
	releases[0].Body = &body
	data, err := json.Marshal(releases)
	if err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		return data
	}
	if size < len(data) {
		t.Fatal("requested response size is smaller than the fixture")
	}
	body = strings.Repeat("x", size-len(data))
	data, err = json.Marshal(releases)
	if err != nil || len(data) != size {
		t.Fatalf("could not build exact-size release fixture: size=%d error=%v", len(data), err)
	}
	return data
}

func assertRunnerReleaseBody(t *testing.T, body *runnerReleaseBody, wantRead int) {
	t.Helper()
	if !body.closed || body.read != wantRead {
		t.Fatalf("release body: closed=%t read=%d, want closed=true read=%d", body.closed, body.read, wantRead)
	}
}

func assertRunnerReleaseRetry(t *testing.T, err error, message string) {
	t.Helper()
	requireCode(t, err, ErrRetry)
	p := err.(*Problem)
	if p.Message != message || p.Recovery == "" {
		t.Fatalf("unexpected release diagnostic: %v", err)
	}
	data, marshalErr := json.Marshal(p)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(err.Error(), runnerReleaseFixtureSecret) || bytes.Contains(data, []byte(runnerReleaseFixtureSecret)) {
		t.Fatal("release diagnostic exposed upstream response or error data")
	}
}

func TestRunnerReleaseResponseSizes(t *testing.T) {
	for _, size := range []int{0, 2 << 20, (2 << 20) + 1, 4_144_596, 8 << 20, (8 << 20) + 1, 9 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := runnerReleaseResponse(t, size)
			body := &runnerReleaseBody{Reader: bytes.NewReader(data)}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := runnerReleaseClient(t, ctx, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: -1}, nil
			})
			err := checkRunnerVersion(ctx, "2.337.0", client)
			if size > 8<<20 {
				assertRunnerReleaseRetry(t, err, "Runner release metadata exceeds the 8 MiB response limit.")
				if !strings.Contains(err.(*Problem).Recovery, "manually") || !strings.Contains(err.(*Problem).Recovery, "retry doctor") {
					t.Fatal("oversize diagnostic lacks manual verification and retry guidance")
				}
			} else if err != nil {
				t.Fatalf("valid latest-100 response rejected: %v", err)
			}
			assertRunnerReleaseBody(t, body, min(len(data), (8<<20)+1))
		})
	}
}

func TestRunnerReleaseFreshness(t *testing.T) {
	now := time.Now()
	pinned := runnerReleaseFixture{Tag: "v2.337.0", Published: now.Add(-60 * 24 * time.Hour)}
	recent := runnerReleaseFixture{Tag: "v2.338.0", Published: now.Add(-29 * 24 * time.Hour)}
	expired := runnerReleaseFixture{Tag: "v2.338.0", Published: now.Add(-31 * 24 * time.Hour)}
	draft := runnerReleaseFixture{Tag: "v2.339.0", Published: now.Add(-40 * 24 * time.Hour), Draft: true}
	prerelease := runnerReleaseFixture{Tag: "v2.340.0", Published: now.Add(-40 * 24 * time.Hour), Prerelease: true}
	for _, tc := range []struct {
		name     string
		releases []runnerReleaseFixture
		code     ErrorCode
	}{
		{name: "latest", releases: []runnerReleaseFixture{pinned}},
		{name: "ignore unstable releases", releases: []runnerReleaseFixture{draft, prerelease, pinned}},
		{name: "29 day update", releases: []runnerReleaseFixture{draft, prerelease, recent, pinned}, code: ErrRetry},
		{name: "31 day update", releases: []runnerReleaseFixture{draft, prerelease, expired, pinned}, code: ErrRunnerVersion},
		{name: "oldest newer release controls window", releases: []runnerReleaseFixture{{Tag: "v2.339.0", Published: recent.Published}, expired, pinned}, code: ErrRunnerVersion},
		{name: "missing pin", releases: []runnerReleaseFixture{recent}, code: ErrRunnerVersion},
		{name: "draft pin", releases: []runnerReleaseFixture{{Tag: pinned.Tag, Published: pinned.Published, Draft: true}}, code: ErrRunnerVersion},
		{name: "prerelease pin", releases: []runnerReleaseFixture{{Tag: pinned.Tag, Published: pinned.Published, Prerelease: true}}, code: ErrRunnerVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.releases)
			if err != nil {
				t.Fatal(err)
			}
			body := &runnerReleaseBody{Reader: bytes.NewReader(data)}
			ctx := context.Background()
			client := runnerReleaseClient(t, ctx, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			err = checkRunnerVersion(ctx, "2.337.0", client)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireCode(t, err, tc.code)
				if tc.code == ErrRetry {
					assertRunnerReleaseRetry(t, err, "A newer runner release is available.")
				}
			}
			assertRunnerReleaseBody(t, body, len(data))
		})
	}
}

func TestRunnerReleaseBadResponses(t *testing.T) {
	valid := string(runnerReleaseResponse(t, 0))
	for _, tc := range []struct {
		name    string
		status  int
		payload string
		readErr bool
		message string
	}{
		{name: "malformed", status: 200, payload: runnerReleaseFixtureSecret, message: "Runner release metadata is invalid."},
		{name: "truncated", status: 200, payload: `[{"body":"` + runnerReleaseFixtureSecret, message: "Runner release metadata is invalid."},
		{name: "trailing data", status: 200, payload: valid + runnerReleaseFixtureSecret, message: "Runner release metadata is invalid."},
		{name: "multiple values", status: 200, payload: valid + `[]`, message: "Runner release metadata is invalid."},
		{name: "read failure after valid prefix", status: 200, payload: valid, readErr: true, message: "Runner release metadata could not be read."},
		{name: "oversized secret", status: 200, payload: runnerReleaseFixtureSecret + strings.Repeat("x", 8<<20), message: "Runner release metadata exceeds the 8 MiB response limit."},
		{name: "rate limit", status: 429, payload: runnerReleaseFixtureSecret, message: "Runner release freshness could not be checked."},
		{name: "server unavailable", status: 503, payload: runnerReleaseFixtureSecret, message: "Runner release freshness could not be checked."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.payload)
			if tc.readErr {
				reader = io.MultiReader(reader, runnerReleaseReadFunc(func([]byte) (int, error) {
					return 0, errors.New(runnerReleaseFixtureSecret)
				}))
			}
			body := &runnerReleaseBody{Reader: reader}
			ctx := context.Background()
			client := runnerReleaseClient(t, ctx, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body}, nil
			})
			err := checkRunnerVersion(ctx, "2.337.0", client)
			assertRunnerReleaseRetry(t, err, tc.message)
			wantRead := min(len(tc.payload), (8<<20)+1)
			if tc.status != http.StatusOK {
				wantRead = 0
			}
			assertRunnerReleaseBody(t, body, wantRead)
		})
	}
}

func TestRunnerReleaseTransportFailures(t *testing.T) {
	for _, mode := range []string{"unavailable", "canceled request", "expired deadline", "canceled read"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "canceled request":
				cancel()
			case "expired deadline":
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer deadlineCancel()
			}
			var body *runnerReleaseBody
			client := runnerReleaseClient(t, ctx, func(req *http.Request) (*http.Response, error) {
				if mode == "canceled read" {
					body = &runnerReleaseBody{Reader: runnerReleaseReadFunc(func([]byte) (int, error) {
						cancel()
						return 0, req.Context().Err()
					})}
					return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
				}
				if err := req.Context().Err(); err != nil {
					return nil, err
				}
				return nil, errors.New(runnerReleaseFixtureSecret)
			})
			err := checkRunnerVersion(ctx, "2.337.0", client)
			message := "Runner release freshness is unknown while GitHub is unavailable."
			if mode == "canceled read" {
				message = "Runner release metadata could not be read."
				assertRunnerReleaseBody(t, body, 0)
			}
			assertRunnerReleaseRetry(t, err, message)
		})
	}
}
