package cli

import (
	"context"
	"io"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func connectionCommand(ctx context.Context, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	if o.server != "" || o.tokenStdin {
		return nil, domain.Fail(domain.InvalidArgument, "Saved connections use their own exact paired authority.", "Omit --server and --token-stdin; supply pairing material only through --code-stdin.")
	}
	fs := flags("connection " + args[0])
	if args[0] == "removed" {
		after := fs.String("after", "", "last removed connection ID from the previous page")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		return connections.ListRemoved(o.dataDir, domain.ID(*after))
	}
	if args[0] == "list" {
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		values, err := connections.List(o.dataDir)
		return map[string]any{"connections": values}, err
	}
	id := fs.String("id", "", "saved connection UUID-v7")
	var name *string
	var input *bool
	var generation *string
	var revision *uint64
	var networkDigest *string
	if args[0] == "worker-network-import" {
		networkDigest = fs.String("expected-ciphertext-digest", "", "separately authenticated ciphertext digest")
	}
	if args[0] == "worker-stop" {
		generation = fs.String("generation", "", "original Worker lifecycle generation")
	}
	if args[0] == "rename" {
		name = fs.String("name", "", "new connection display name")
		revision = fs.Uint64("revision", 0, "original saved connection revision")
	}
	if args[0] == "remove" {
		revision = fs.Uint64("revision", 0, "original saved connection revision")
	}
	if args[0] == "pair" {
		name = fs.String("name", "", "connection display name")
		input = fs.Bool("code-stdin", false, "read the original private pairing document from stdin")
	}
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	var value any
	var err error
	switch args[0] {
	case "remove":
		value, err = connections.Remove(ctx, o.dataDir, domain.ID(*id), o.requestID, *revision)
	case "rename":
		value, err = connections.Rename(ctx, o.dataDir, domain.ID(*id), o.requestID, *revision, *name)
	case "worker-register":
		credential, registerErr := connections.RegisterWorker(ctx, o.dataDir, domain.ID(*id))
		err = registerErr
		if err == nil {
			value = credentialMetadata(credential)
		}
	case "worker-inspect", "worker-status", "worker-start", "worker-stop", "worker-network-prepare", "worker-network-import", "worker-network-status":
		credential, credentialErr := connections.WorkerCredential(o.dataDir, domain.ID(*id))
		if credentialErr != nil {
			err = credentialErr
			break
		}
		root, rootErr := connections.WorkerRoot(o.dataDir, domain.ID(*id))
		if rootErr != nil {
			err = rootErr
			break
		}
		switch args[0] {
		case "worker-inspect":
			value = credentialMetadata(credential)
		case "worker-status":
			value, err = worker.Status(root)
		case "worker-start":
			value, err = startDetachedWorker(ctx, o, root)
		case "worker-stop":
			value, err = stopLocalWorker(ctx, root, domain.ID(*generation))
		case "worker-network-prepare", "worker-network-status", "worker-network-import":
			networkArgs := []string{"status", "--worker-dir", root}
			if args[0] == "worker-network-prepare" {
				networkArgs[0] = "prepare"
			}
			if args[0] == "worker-network-import" {
				networkArgs = []string{"import", "--worker-dir", root, "--input", "-", "--expected-ciphertext-digest", *networkDigest}
			}
			value, err = workerNetworkCommand(ctx, o, networkArgs, streams)
		}
	case "inspect":
		value, err = connections.Inspect(o.dataDir, domain.ID(*id))
	case "verify":
		value, err = connections.Verify(ctx, o.dataDir, domain.ID(*id))
	case "retry":
		value, err = connections.Retry(ctx, o.dataDir, domain.ID(*id))
	case "pair":
		if !*input || terminalInput(streams.In) {
			return nil, domain.Fail(domain.MissingInput, "A private server pairing document is required on stdin.", "Provide one original short-lived client grant through --code-stdin; never place it in arguments.")
		}
		raw, readErr := io.ReadAll(io.LimitReader(streams.In, (32<<10)+1))
		defer clear(raw)
		if readErr != nil || len(raw) > 32<<10 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid server pairing input.", "Provide one bounded private JSON pairing document.")
		}
		var grant worker.PairingCode
		if err := domain.Decode(raw, &grant); err != nil {
			return nil, err
		}
		value, err = connections.Pair(ctx, o.dataDir, domain.ID(*id), *name, grant)
	default:
		return nil, usage()
	}
	logger := slog.New(slog.NewJSONHandler(streams.Err, nil))
	if err != nil {
		logger.Warn("client_connection", "operation", args[0], "connection_id", *id, "code", domain.SafeError(err).Code)
	} else {
		logger.Info("client_connection", "operation", args[0], "connection_id", *id, "state", "complete")
	}
	return value, err
}
