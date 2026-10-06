// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

type desktopControlAction string

const desktopControlStop desktopControlAction = "stop"

// This pipe controls its original process only. EOF is deliberately not Stop:
// a crashed desktop must leave its server available to other clients.
func readDesktopStop(input io.Reader, stop chan<- struct{}) {
	line, err := bufio.NewReader(io.LimitReader(input, 257)).ReadBytes('\n')
	var control struct {
		Version int                  `json:"version"`
		Action  desktopControlAction `json:"action"`
	}
	if err == nil && len(line) <= 256 && domain.Decode(line, &control) == nil && control.Version == 1 && control.Action == desktopControlStop {
		close(stop)
	}
}

func desktopHost(ctx context.Context, o options, args []string, streams IO) (any, error) {
	if o.server != "" || o.tokenStdin {
		return nil, usage()
	}
	fs := flags("server desktop-host")
	modeName := fs.String("mode", "", "launch, retry or ensure")
	listen := fs.String("listen", server.DefaultListen, "explicit listener")
	origins := fs.String("allowed-origins", "", "comma-separated exact origins")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	var mode startupMode
	switch *modeName {
	case "launch":
		mode = startupDesktopLaunch
	case "retry":
		mode = startupDesktopRetry
	case "ensure":
		mode = startupEnsure
	default:
		return nil, usage()
	}
	config := server.Config{DataDir: o.dataDir, Listen: *listen, Logger: slog.New(slog.NewJSONHandler(streams.Err, nil))}
	if *origins != "" {
		config.AllowedOrigins = strings.Split(*origins, ",")
	}
	stop := make(chan struct{})
	go readDesktopStop(streams.In, stop)
	// Inherited stdin may use a synchronous OS read that Close cannot wake.
	// Do not wait on that read after reuse/external Stop: the CLI process exit
	// releases it, and native Child::wait independently joins the whole process.
	// The reader owns no server operation except this process-local Stop signal.
	defer func() {
		if input, ok := streams.In.(io.Closer); ok {
			input.Close()
		}
	}()
	startup, cancelStartup := context.WithCancel(ctx)
	defer cancelStartup()
	finished := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-stop:
			cancelStartup()
		case <-finished:
		}
	}()
	defer func() { close(finished); <-watchDone }()
	return startupWithHost(startup, o, config, streams, mode, func(admission context.Context, config server.Config, intent server.Lifecycle, ready func() error) (any, error) {
		return runDesktopHost(ctx, admission, config, intent, streams.Out, stop, ready)
	})
}

func runDesktopHost(ctx, admission context.Context, config server.Config, intent server.Lifecycle, output io.Writer, stop <-chan struct{}, ready func() error) (any, error) {
	path := filepath.Join(config.DataDir, "server.log")
	if _, err := os.Lstat(path); err == nil {
		if err := security.RegularPrivate(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, domain.SafeError(err)
	}
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	defer log.Close()
	config.Logger = slog.New(slog.NewJSONHandler(log, nil))
	config.StartupID = intent.Generation
	running, cancel := context.WithCancel(ctx)
	defer cancel()
	// The aggregate startup deadline ends at readiness. An explicit Stop has
	// its own suppression-before-cancellation path below.
	abortStartup := context.AfterFunc(admission, func() {
		select {
		case <-stop:
		default:
			cancel()
		}
	})
	defer abortStartup()
	exited := make(chan struct{})
	controlDone := make(chan struct{})
	go func() {
		defer close(controlDone)
		select {
		case <-stop:
		case <-exited:
			select {
			case <-stop:
			default:
				return
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := server.SuppressDesktopRestart(config.DataDir, intent.Generation)
			if err == nil {
				config.Logger.Info("desktop_server_shutdown", "phase", "restart-suppressed")
				break
			}
			if domain.SafeError(err).Code != domain.Conflict || time.Now().After(deadline) {
				config.Logger.Warn("desktop_server_shutdown", "phase", "suppression-unconfirmed", "code", domain.SafeError(err).Code)
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		cancel()
	}()
	var readyErr error
	err = server.Serve(running, config, func(endpoint server.Endpoint) {
		abortStartup()
		if readyErr = ready(); readyErr != nil {
			cancel()
			return
		}
		status := map[string]any{"version": endpoint.Version, "protocol_version": endpoint.ProtocolVersion, "listener": endpoint.URL, "server_id": endpoint.ServerID}
		readyErr = json.NewEncoder(output).Encode(envelope{Version: 1, Result: map[string]any{"started": true, "generation": intent.Generation, "server": map[string]any{"status": status}}})
		if readyErr != nil {
			// Losing the desktop's output pipe is not an explicit Quit. Preserve
			// the admitted server even when readiness publication races a crash.
			config.Logger.Warn("desktop_server_control", "phase", "ready-delivery-lost")
		}
	})
	close(exited)
	<-controlDone
	if readyErr != nil {
		return nil, domain.Fail(domain.Unavailable, "Desktop startup could not be reported.", "Inspect the original server before retrying.")
	}
	if err != nil {
		return nil, err
	}
	config.Logger.Info("desktop_server_shutdown", "phase", "server-joined")
	return map[string]any{"state": "stopped", "generation": intent.Generation}, nil
}
