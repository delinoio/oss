// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type prGitLocalCommand struct {
	grant     string
	done      chan struct{}
	completed bool
}
type prGitLocalCompletion struct {
	Version uint32 `json:"version"`
	Grant   string `json:"grant"`
}

func isPRLocalCommand(args []string) bool {
	return args[0] != "push" && args[0] != "fetch" && args[0] != "delidev-target"
}

func (b *prGitBridge) waitLocal(ctx context.Context, w http.ResponseWriter) error {
	grant, err := security.RandomToken()
	if err != nil {
		b.mu.Lock()
		b.uncertain = true
		b.mu.Unlock()
		http.Error(w, "Uncertain PR Git capability.", http.StatusServiceUnavailable)
		return toolFailure()
	}
	command := &prGitLocalCommand{grant: grant, done: make(chan struct{})}
	b.mu.Lock()
	b.local = command
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if !command.completed {
			b.uncertain = true
		}
		b.local = nil
		b.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/json")
	if json.NewEncoder(w).Encode(prGitBridgeResponse{Grant: grant}) != nil || http.NewResponseController(w).Flush() != nil {
		return toolFailure()
	}
	select {
	case <-command.done:
		return nil
	case <-ctx.Done():
		return toolFailure()
	}
}

func (b *prGitBridge) completeLocal(w http.ResponseWriter, r *http.Request) {
	// Completion has its own lane so waiting parallel commands cannot consume
	// the request capacity needed to release their original owner.
	select {
	case b.completions <- struct{}{}:
		defer func() { <-b.completions }()
	default:
		http.Error(w, "PR Git completion is busy.", http.StatusTooManyRequests)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	var completion prGitLocalCompletion
	if err != nil || len(raw) > 4096 || domain.Decode(raw, &completion) != nil || completion.Version != 1 || len(completion.Grant) < 32 || len(completion.Grant) > 256 {
		http.Error(w, "Invalid PR Git completion.", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	command := b.local
	valid := !b.uncertain && command != nil && !command.completed && subtle.ConstantTimeCompare([]byte(command.grant), []byte(completion.Grant)) == 1
	if valid {
		command.completed = true
		close(command.done)
	}
	b.mu.Unlock()
	if !valid {
		http.Error(w, "Unconfirmed PR Git command ownership.", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(prGitBridgeResponse{})
}

func finishPRLocalCommand(ctx context.Context, client *http.Client, endpoint, token, grant string) error {
	raw, _ := json.Marshal(prGitLocalCompletion{Version: 1, Grant: grant})
	// A cancelled local command is still reaped before this bounded release.
	// Lost completion never grants another command or a second push.
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, endpoint+"/complete", bytes.NewReader(raw))
	if err != nil {
		return toolFailure()
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return toolFailure()
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, 4097))
	var result prGitBridgeResponse
	if err != nil || len(raw) > 4096 || response.StatusCode != http.StatusOK || domain.Decode(raw, &result) != nil || result.Problem != nil || result.Grant != "" || len(result.Output) != 0 {
		return toolFailure()
	}
	return nil
}
