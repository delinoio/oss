// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func start(ctx context.Context, o options, args []string, streams IO) (any, error) {
	if o.server != "" || o.tokenStdin {
		return nil, domain.Fail(domain.InvalidArgument, "Server startup is a local infrastructure command.", "Run it on the server machine with its data directory.")
	}
	fs := flags("server start")
	listen := fs.String("listen", server.DefaultListen, "explicit listener")
	cert := fs.String("tls-cert", "", "TLS certificate")
	key := fs.String("tls-key", "", "TLS key")
	origins := fs.String("allowed-origins", "", "comma-separated exact origins")
	foreground := fs.Bool("foreground", false, "remain attached")
	startupID := fs.String("startup-id", "", "exact detached startup generation")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if _, err := worker.LoadCredential(o.dataDir); err == nil {
		return nil, domain.Fail(domain.PermissionDenied, "Server startup requires an owner scope, not a paired device scope.", "Select the server's original local data directory.")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	configuration := server.Config{DataDir: o.dataDir, Listen: *listen, TLSCertificate: *cert, TLSKey: *key, Logger: slog.New(slog.NewJSONHandler(streams.Err, nil))}
	if *startupID != "" {
		if args[0] != "run" || domain.ID(*startupID).Validate() != nil {
			return nil, usage()
		}
		configuration.StartupID = domain.ID(*startupID)
	}
	if *origins != "" {
		configuration.AllowedOrigins = strings.Split(*origins, ",")
	}
	if args[0] == "ensure" {
		if *foreground {
			return nil, usage()
		}
		return ensureDetached(ctx, o, configuration, streams, true)
	}
	if args[0] == "run" || *foreground {
		err := server.Serve(ctx, configuration, func(e server.Endpoint) {
			_ = json.NewEncoder(streams.Out).Encode(envelope{Version: 1, Result: map[string]any{"status": "ready", "endpoint": e}})
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "stopped"}, nil
	}
	return startDetached(ctx, o, configuration, streams)
}

func offlineStop() error {
	return domain.Fail(domain.RecoveryRequired, "Automatic local restart is suppressed, but server shutdown is unconfirmed.", "Inspect the existing server before starting again; no session or process cleanup is claimed.")
}
