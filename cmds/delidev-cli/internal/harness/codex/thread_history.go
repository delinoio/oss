// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ThreadHistoryProof binds one native thread to one private rollout file. It
// contains no native content and is kept only in Worker-private checkpoints.
type ThreadHistoryProof struct {
	ThreadID domain.ID `json:"thread_id"`
	Path     string    `json:"path"`
	Digest   string    `json:"digest"`
}

// RetainThreadHistory reads the exact bound thread's native path and hashes
// the private file before an account owner can admit a replacement execution.
func (c *Client) RetainThreadHistory(ctx context.Context) (ThreadHistoryProof, error) {
	if c.mode != ThreadProtocol || c.thread == "" || c.execution == nil {
		return ThreadHistoryProof{}, continuationUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return ThreadHistoryProof{}, err
	}
	defer func() { <-c.control }()
	thread, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	var path string
	if err != nil || thread.ID != c.thread || json.Unmarshal(thread.Path, &path) != nil {
		return ThreadHistoryProof{}, continuationUncertain()
	}
	digest, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return ThreadHistoryProof{}, continuationUncertain()
	}
	return ThreadHistoryProof{ThreadID: c.thread, Path: path, Digest: hex.EncodeToString(digest[:])}, nil
}

func VerifyThreadHistory(ctx context.Context, home string, proof ThreadHistoryProof) error {
	if proof.ThreadID.Validate() != nil || !contextDigest(proof.Digest) {
		return continuationUncertain()
	}
	digest, err := forkRolloutDigest(ctx, home, proof.Path)
	if err != nil || hex.EncodeToString(digest[:]) != proof.Digest {
		return continuationUncertain()
	}
	return nil
}
