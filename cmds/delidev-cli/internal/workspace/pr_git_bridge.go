// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type prGitBridge struct {
	endpoint string
	token    string
	server   *http.Server
	cancel   context.CancelFunc
	done     chan error
	commands chan struct{}
	requests chan struct{}
	once     sync.Once
	closeErr error
}
type prGitBridgeRequest struct {
	Version uint32   `json:"version"`
	Args    []string `json:"args"`
}
type prGitBridgeResponse struct {
	Output  []byte        `json:"output,omitempty"`
	Problem *domain.Error `json:"problem,omitempty"`
}

// The Worker owns authentication and the command's original process scope.
// The harness receives only a disposable capability for this closed argv API.
// It never receives native lookup environment or the private push-proof key.
func startPRGitBridge(parent context.Context, tool *PRGitTool) (*prGitBridge, error) {
	if err := security.PrivateDir(filepath.Join(filepath.Dir(tool.path), "local-home")); err != nil {
		return nil, err
	}
	token, err := security.RandomToken()
	if err != nil {
		return nil, toolFailure()
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, toolFailure()
	}
	ctx, cancel := context.WithCancel(parent)
	bridge := &prGitBridge{endpoint: "http://" + listener.Addr().String() + "/git", token: token, cancel: cancel, done: make(chan error, 1), commands: make(chan struct{}, 1), requests: make(chan struct{}, 32)}
	bridge.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	bridge.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/git" || r.URL.RawQuery != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "Unavailable PR Git capability.", http.StatusForbidden)
			return
		}
		select {
		case bridge.requests <- struct{}{}:
			defer func() { <-bridge.requests }()
		default:
			http.Error(w, "PR Git capability is busy.", http.StatusTooManyRequests)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		var request prGitBridgeRequest
		if err != nil || len(raw) > 1<<20 || domain.Decode(raw, &request) != nil || request.Version != 1 || tool.scope.validateArgs(request.Args) != nil {
			http.Error(w, "Invalid PR Git request.", http.StatusBadRequest)
			return
		}
		bounded, stop := context.WithTimeout(r.Context(), 2*time.Minute)
		defer stop()
		var output bytes.Buffer
		select {
		case bridge.commands <- struct{}{}:
			err = tool.runOwned(bounded, request.Args, &output)
			<-bridge.commands
		case <-bounded.Done():
			err = domain.SafeError(bounded.Err())
		}
		if err != nil {
			tool.manager.Logger.WarnContext(bounded, "manual_pr_git_command_failed", "attempt_id", tool.scope.Selection.AttemptID, "execution_id", tool.scope.Claim.ExecutionID, "action", request.Args[0], "code", domain.SafeError(err).Code)
		}
		response := prGitBridgeResponse{Output: output.Bytes()}
		if err != nil {
			response.Output = nil
			response.Problem = domain.SafeError(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	go func() { bridge.done <- bridge.server.Serve(listener) }()
	return bridge, nil
}
func (b *prGitBridge) close() error {
	if b == nil {
		return nil
	}
	b.once.Do(func() {
		b.cancel()
		bounded, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := b.server.Shutdown(bounded); err != nil {
			b.closeErr = toolFailure()
			_ = b.server.Close()
		}
		select {
		case err := <-b.done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				b.closeErr = toolFailure()
			}
		case <-bounded.Done():
			b.closeErr = toolFailure()
		}
	})
	return b.closeErr
}

// RunPRGit is the copied launcher's client. No original Git executable or
// authentication context is restored in this process, even for direct calls.
func RunPRGit(ctx context.Context, path string, args []string, stdout io.Writer) error {
	scope, _, err := loadPRGitScope(path)
	if err != nil {
		return err
	}
	if err := scope.validateArgs(args); err != nil {
		return err
	}
	endpoint, token := os.Getenv(prGitEnvironmentPrefix+"ENDPOINT"), os.Getenv(prGitEnvironmentPrefix+"TOKEN")
	address, err := url.Parse(endpoint)
	if err != nil || address.Scheme != "http" || address.User != nil || address.Hostname() != "127.0.0.1" || address.Path != "/git" || address.RawPath != "" || address.RawQuery != "" || address.Fragment != "" || len(token) < 32 || len(token) > 256 {
		return toolFailure()
	}
	port, err := strconv.Atoi(address.Port())
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != address.Port() {
		return toolFailure()
	}
	raw, err := json.Marshal(prGitBridgeRequest{Version: 1, Args: args})
	if err != nil || len(raw) > 1<<20 {
		return toolFailure()
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return toolFailure()
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return domain.Fail(domain.Unavailable, "The original PR Git bridge did not respond.", "Preserve its attempt; no native push can be replayed.")
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, (MaxGitOutput*2)+(64<<10)+1))
	var result prGitBridgeResponse
	if err != nil || len(raw) > MaxGitOutput*2+64<<10 || response.StatusCode != http.StatusOK || domain.Decode(raw, &result) != nil || len(result.Output) > MaxGitOutput || result.Problem != nil && len(result.Output) != 0 {
		return toolFailure()
	}
	if result.Problem != nil {
		return domain.SafeError(result.Problem)
	}
	_, err = stdout.Write(result.Output)
	return err
}
