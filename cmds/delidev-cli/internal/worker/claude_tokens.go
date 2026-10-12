// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func beginManagedClaudeTokenExecution(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput, lease *managedSubscriptionLease, closeHTTP func()) (*nativeClaudeProfile, func(bool) error, error) {
	token, err := subscription.ParseClaudeToken(lease.response.Bundle, config.execution.Credential.ServerID, input.AccountID, domain.ID(lease.response.GenerationId))
	if err != nil {
		clear(lease.response.Bundle)
		closeHTTP()
		return nil, nil, err
	}
	base := filepath.Join(config.Root, "managed-auth")
	root := filepath.Join(base, lease.response.LeaseId+"-claude")
	p := &nativeClaudeProfile{root: root, managedLease: lease, token: []byte(token.Token), tokenBundle: lease.response.Bundle}
	lease.response.Bundle = nil
	finish := func(joined bool) error {
		defer closeHTTP()
		defer clear(p.token)
		defer clear(p.tokenBundle)
		if p.original != nil {
			if subscription.CleanupRuntime(p.root, p.original) != nil {
				joined = false
			}
		} else {
			if _, e := os.Lstat(p.root); !errors.Is(e, os.ErrNotExist) {
				joined = false
			}
		}
		return lease.finish(p.tokenBundle, joined, false, joined && !p.tokenFailed)
	}
	if _, e := os.Lstat(root); !errors.Is(e, os.ErrNotExist) {
		_ = finish(false)
		return nil, nil, subscription.Invalid()
	}
	env, err := harness.PrivateRuntimeEnvironment(root)
	if err == nil {
		p.original, err = security.StableStat(root)
	}
	if err == nil {
		p.home = filepath.Join(root, "claude")
		p.config = claude.AuthConfig{Version: input.Installation.Version, Home: p.home, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.ID(lease.response.LeaseId), Executable: input.Installation.ResolvedPath, Cwd: root, Env: env, Logger: config.Logger}}
		lease.journal.NativeStarted = true
		err = writeJSON(lease.journalPath, lease.journal)
	}
	if err == nil {
		err = claude.VerifyAuthVersion(ctx, p.config)
	}
	if err != nil {
		_ = finish(false)
		return nil, nil, err
	}
	return p, finish, nil
}

// Retained native history is scanned after joined cleanup, before returning an
// unchanged token generation. Raw or encoded credentials invalidate the proof;
// unexpected files, symlinks and exceeded bounds retain the exclusive lease.
func scanManagedClaudeTokenRuntime(home string, token []byte) error {
	if len(token) == 0 {
		return subscription.Invalid()
	}
	needles := [][]byte{token}
	defer func() {
		for _, n := range needles[1:] {
			clear(n)
		}
	}()
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw := make([]byte, encoding.EncodedLen(len(token)))
		encoding.Encode(raw, token)
		needles = append(needles, raw)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return subscription.Invalid()
	}
	defer root.Close()
	original, err := root.Stat(".")
	if err != nil {
		return subscription.Invalid()
	}
	count, total := 0, int64(0)
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, e error) error {
		if e != nil || entry.Type()&os.ModeSymlink != 0 {
			return subscription.Invalid()
		}
		count++
		if count > 2048 {
			return subscription.Invalid()
		}
		if entry.IsDir() {
			return nil
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return subscription.Invalid()
		}
		total += info.Size()
		if total > 64<<20 {
			return subscription.Invalid()
		}
		file, e := root.Open(path)
		if e != nil {
			return subscription.Invalid()
		}
		raw, e := io.ReadAll(io.LimitReader(file, 16<<20+1))
		closed := file.Close()
		defer clear(raw)
		if e != nil || closed != nil || len(raw) > 16<<20 {
			return subscription.Invalid()
		}
		for _, n := range needles {
			if bytes.Contains(raw, n) {
				return subscription.Invalid()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(original, current) {
		return subscription.Invalid()
	}
	return nil
}
func runManagedClaudeTokenRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, accountID domain.ID, account domain.Account) error {
	st := account.Subscription
	if st == nil || !st.ManagedClaudeToken || !st.RecoveryRequired || st.Lease == nil || st.Lease.MachineID != credential.MachineID {
		return subscription.Invalid()
	}
	l := st.Lease
	path := filepath.Join(config.Root, "managed-auth", string(l.ID)+".json")
	raw, e := security.ReadPrivate(path, 16<<10)
	defer clear(raw)
	var j managedSubscriptionJournal
	if e != nil || domain.Decode(raw, &j) != nil || j.Version != 1 || j.Lease != l.ID || j.Account != accountID || j.Operation != l.OperationID || j.Instance != l.InstanceID || j.Generation != l.Generation && (j.Generation != "" || j.NativeStarted || j.ExecutionStarted) || j.NativeProfileID != "" || j.Finish.Validate() != nil {
		return subscription.Invalid()
	}
	if j.NativeStarted {
		if e := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), l.ID); e != nil {
			return e
		}
	}
	if j.ExecutionStarted {
		if e := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), l.OperationID); e != nil {
			return e
		}
	}
	// Recovery removes the original disposable execution runtime rather than
	// retaining unverifiable history after token bytes have intentionally vanished.
	if j.ExecutionStarted {
		if j.ClaudeTokenExecutionID.Validate() != nil {
			return subscription.Invalid()
		}
		home := filepath.Join(config.Root, "runtimes", string(j.ClaudeTokenExecutionID))
		if info, e := security.StableStat(home); e == nil {
			if subscription.CleanupRuntime(home, info) != nil {
				return subscription.Invalid()
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return subscription.Invalid()
		}
	}
	home := filepath.Join(config.Root, "managed-auth", string(l.ID)+"-claude")
	if info, e := security.StableStat(home); e == nil {
		if subscription.CleanupRuntime(home, info) != nil {
			return subscription.Invalid()
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return subscription.Invalid()
	}
	if j.CleanupFinish == "" {
		j.CleanupFinish = domain.NewID()
		if writeJSON(path, j) != nil {
			return subscription.Invalid()
		}
	}
	lease := &managedSubscriptionLease{client: client, credential: credential, instance: l.InstanceID, account: accountID, finishID: j.CleanupFinish, journalPath: path, journal: j, response: &pb.TakeSubscriptionResponse{LeaseId: string(l.ID), GenerationId: string(l.Generation), LeaseRevision: l.Revision}}
	return lease.finish(nil, true, false, false)
}
