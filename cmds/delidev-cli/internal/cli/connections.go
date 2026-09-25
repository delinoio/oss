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
