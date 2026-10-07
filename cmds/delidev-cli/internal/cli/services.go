package cli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func serviceScope(root string, kind userservice.Kind) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", domain.SafeError(err)
	}
	// Canonicalize existing private scopes only; installation never initializes,
	// pairs, migrates or repairs an unrelated scope as a side effect.
	if security.CheckPrivateDir(root) != nil {
		return "", domain.Fail(domain.PermissionDenied, "The service requires an existing private scope.", "Explicitly initialize the server or pair the Worker first.")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", domain.SafeError(err)
	}
	if kind == userservice.Server {
		if _, e := worker.LoadCredential(root); e == nil {
			domain.ObserveOwnership(domain.OwnershipActor, "")
		} else if !os.IsNotExist(e) {
			return "", e
		}
		_, err = security.LoadIdentity(root)
	} else {
		var c worker.Credential
		c, err = worker.LoadCredential(root)
		if err == nil && domain.OwnershipBlocks(domain.OwnershipActor, c.DeviceID, c.Type != domain.WorkerDevice) {
			err = domain.Fail(domain.PermissionDenied, "This is not a Worker scope.", "Choose the original paired Worker directory.")
		}
	}
	return root, err
}
func serviceCommand(ctx context.Context, o options, kind userservice.Kind, args []string, streams IO) (any, error) {
	if len(args) == 0 || o.server != "" || o.tokenStdin {
		return nil, domain.Fail(domain.InvalidArgument, "Service registration is local current-user infrastructure.", "Use server service or worker service on the owning computer; use service-control for authenticated server-host operations.")
	}
	fs := flags("service")
	root := fs.String("worker-dir", filepath.Join(o.dataDir, "worker"), "existing paired Worker scope")
	revision := fs.Uint64("revision", 0, "expected service revision")
	listen := fs.String("listen", server.DefaultListen, "server listener")
	cert := fs.String("tls-cert", "", "server certificate reference")
	key := fs.String("tls-key", "", "server key reference")
	origins := fs.String("allowed-origins", "", "exact server origins")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	scope := o.dataDir
	if kind == userservice.Worker {
		scope = *root
	}
	scope, err := serviceScope(scope, kind)
	if err != nil {
		return nil, err
	}
	manager := userservice.New(scope, kind, slog.New(slog.NewJSONHandler(streams.Err, nil)))
	if args[0] == "status" {
		return manager.Status(ctx)
	}
	if kind == userservice.Server && args[0] == "install" {
		config := server.Config{DataDir: scope, Listen: *listen, TLSCertificate: *cert, TLSKey: *key}
		if *origins != "" {
			config.AllowedOrigins = strings.Split(*origins, ",")
		}
		if err := server.ValidateConfig(config); err != nil {
			return nil, err
		}
		manager.Options = userservice.ServerOptions{Listen: config.Listen, TLSCertificate: *cert, TLSKey: *key, AllowedOrigins: config.AllowedOrigins}
	}
	ensureRequest(&o)
	result, err := manager.Control(ctx, userservice.Action(args[0]), o.requestID, *revision, "local-user")
	if err == nil && args[0] == "stop" {
		result.Status, err = manager.WaitStopped(ctx)
	}
	return result, err
}
func runService(ctx context.Context, o options, args []string, streams IO) (any, error) {
	if o.server != "" || o.tokenStdin {
		return nil, usage()
	}
	fs := flags("service-run")
	kind := fs.String("kind", "", "registered process kind")
	id := fs.String("id", "", "installed service identity")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	k := userservice.Kind(*kind)
	if !k.Valid() || domain.ID(*id).Validate() != nil {
		return nil, usage()
	}
	root, err := serviceScope(o.dataDir, k)
	if err != nil {
		return nil, err
	}
	logFile, err := serviceLog(root, k)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	logger := slog.New(slog.NewJSONHandler(logFile, nil))
	manager := userservice.New(root, k, logger)
	suppress := func(ctx context.Context) error {
		status, err := manager.Status(ctx)
		if err != nil {
			return err
		}
		_, err = manager.Control(ctx, userservice.Stop, domain.NewID(), status.Revision, "owned-controller")
		return err
	}
	err = manager.Run(ctx, domain.ID(*id), func(child context.Context, spec userservice.Spec, explicit bool) error {
		if k == userservice.Server {
			opts := spec.Options
			if opts.Listen == "" {
				opts.Listen = server.DefaultListen
			}
			config := server.Config{DataDir: root, Listen: opts.Listen, TLSCertificate: opts.TLSCertificate, TLSKey: opts.TLSKey, AllowedOrigins: opts.AllowedOrigins, Logger: logger}
			if !explicit {
				intent, err := server.ReadLifecycle(root)
				if err != nil {
					return err
				}
				if intent.State == server.DesiredStopped {
					return suppress(child)
				}
				if intent.Version == 0 || !intent.Matches(config) {
					return domain.Fail(domain.RecoveryRequired, "The original service startup configuration changed.", "Preserve the scope and explicitly start with its installed configuration.")
				}
				config.StartupID = intent.Generation
			}
			var original server.Lifecycle
			var readyErr error
			if err := server.Serve(child, config, func(server.Endpoint) {
				original, readyErr = server.ReadLifecycle(root)
			}); err != nil {
				return err
			}
			if readyErr != nil {
				return readyErr
			}
			_, stopped, err := userservice.ManagedIntent(root, k)
			if err != nil {
				return err
			}
			if stopped && original.State == server.DesiredRunning {
				if err := server.SuppressCompletedServiceRestart(root, original.Generation); err != nil {
					return err
				}
			}
			intent, err := server.ReadLifecycle(root)
			if err != nil {
				return err
			}
			if intent.State == server.DesiredStopped && child.Err() == nil {
				return suppress(child)
			}
			return nil
		}
		if !explicit {
			status, err := worker.Status(root)
			if err != nil {
				return err
			}
			if status.Lifecycle.Desired == worker.WorkerStopped {
				return suppress(child)
			}
		}
		if err := worker.Run(child, worker.Config{Root: root, Logger: logger}); err != nil {
			return err
		}
		status, err := worker.Status(root)
		if err != nil {
			return err
		}
		if status.Lifecycle.Desired == worker.WorkerStopped && child.Err() == nil {
			return suppress(child)
		}
		return nil
	})
	return map[string]any{"status": "exited"}, err
}

func serviceLog(root string, kind userservice.Kind) (*os.File, error) {
	path := filepath.Join(root, "user-service-"+string(kind)+".log")
	before, err := os.Lstat(path)
	if err == nil {
		if e := security.RegularPrivate(path); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(err) {
		return nil, domain.SafeError(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	info, err := f.Stat()
	current, e := os.Lstat(path)
	if err != nil || e != nil || !os.SameFile(info, current) || (before != nil && !os.SameFile(before, info)) || security.RegularPrivate(path) != nil {
		f.Close()
		return nil, domain.Fail(domain.PermissionDenied, "The private service log changed ownership.", "Preserve the scope and restore its owner-only log file.")
	}
	return f, nil
}
