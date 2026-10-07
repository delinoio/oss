// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"path/filepath"
	"strings"
)

func desktopReadOnly(op desktopOperation) bool {
	switch op {
	case "server.desktop-status", "device.inspect", "worker.inspect", "worker.status", "connection.list", "connection.removed", "connection.inspect", "connection.worker-inspect", "connection.worker-status", "browser-profile.status", "browser-profile.list":
		return true
	}
	return false
}

// This closed registry is the native infrastructure API, not a generic CLI
// dispatcher. Business requests retain the existing authenticated Connect API.
var desktopCommands = map[desktopOperation]string{
	"server.desktop-status": "server desktop-status",
	"device.pair-local":     "device pair-local", "device.inspect": "device inspect", "device.inspect-local": "device inspect-local", "device.recover-local": "device recover-local",
	"worker.pair-local": "worker pair-local", "worker.inspect": "worker inspect", "worker.start": "worker start", "worker.stop": "worker stop", "worker.status": "worker status",
	"worker.network.prepare": "worker network prepare", "worker.network.import": "worker network import", "worker.network.status": "worker network status",
	"connection.list": "connection list", "connection.removed": "connection removed", "connection.inspect": "connection inspect", "connection.pair": "connection pair", "connection.retry": "connection retry", "connection.rename": "connection rename", "connection.remove": "connection remove", "connection.verify": "connection verify",
	"connection.worker-inspect": "connection worker-inspect", "connection.worker-register": "connection worker-register", "connection.worker-start": "connection worker-start", "connection.worker-stop": "connection worker-stop", "connection.worker-status": "connection worker-status",
	"connection.worker-network-prepare": "connection worker-network-prepare", "connection.worker-network-import": "connection worker-network-import", "connection.worker-network-status": "connection worker-network-status",
	"browser-storage.prepare": "browser-storage prepare", "browser-profile.status": "browser-profile status", "browser-profile.list": "browser-profile list", "browser-profile.confirm-removal": "browser-profile confirm-removal",
	"presentation.open-github": "presentation open-github",
	"update.native-prepare":    "update native-prepare", "update.native-verify": "update native-verify", "update.native-begin": "update native-begin", "update.native-outcome": "update native-outcome", "update.native-inspect": "update native-inspect",
}

func (h *desktopHostState) execute(ctx context.Context, r desktopRequest) (any, error) {
	if r.CancelID != "" || r.TimeoutMS > 660000 {
		return nil, usage()
	}
	switch r.Operation {
	case desktopLaunch, desktopRetry, desktopEnsure:
		if len(r.Arguments) != 0 || len(r.Input) != 0 || r.Scope != "" || r.RequestID != "" {
			return nil, usage()
		}
		return h.start(ctx, r.Operation)
	}
	command, ok := desktopCommands[r.Operation]
	if !ok {
		return nil, usage()
	}
	h.mu.Lock()
	target, closed := h.target, h.closed
	h.mu.Unlock()
	if closed || ctx.Err() != nil {
		return nil, context.Canceled
	}
	root := h.options.dataDir
	if r.Scope != "" {
		if !strings.HasPrefix(command, "browser-profile ") {
			return nil, usage()
		}
		if r.Scope == "local" {
			root = filepath.Join(root, "desktop-client")
		} else if strings.HasPrefix(r.Scope, "saved:") && domain.ID(strings.TrimPrefix(r.Scope, "saved:")).Validate() == nil {
			root = filepath.Join(root, "connections", strings.TrimPrefix(r.Scope, "saved:"), "client")
		} else {
			return nil, usage()
		}
	}
	if r.Operation == "worker.start" && (len(r.Arguments) != 1 || r.Arguments[0] != "--detach") {
		return nil, usage()
	}
	for i, arg := range r.Arguments {
		name, _, _ := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") && i == 0 {
			return nil, usage()
		}
		switch name {
		case "--data-dir", "--server", "--token-stdin", "--request-id", "--device-dir", "--worker-dir", "--listen", "--allowed-origins", "--tls-cert", "--tls-key":
			return nil, usage()
		}
		if name == "--input" && (i+1 >= len(r.Arguments) || r.Arguments[i+1] != "-") {
			return nil, usage()
		}
	}
	args := []string{"--data-dir", root}
	if r.RequestID != "" {
		if r.RequestID.Validate() != nil {
			return nil, usage()
		}
		args = append(args, "--request-id", string(r.RequestID))
	}
	args = append(args, strings.Fields(command)...)
	args = append(args, r.Arguments...)
	if r.Operation == "device.inspect" || r.Operation == "device.pair-local" {
		args = append(args, "--device-dir", filepath.Join(h.options.dataDir, "desktop-client"), "--join-existing")
	}
	if r.Operation == "server.desktop-status" {
		args = append(args, "--listen", h.config.Listen)
		if len(h.config.AllowedOrigins) > 0 {
			args = append(args, "--allowed-origins", strings.Join(h.config.AllowedOrigins, ","))
		}
	}
	var output boundedDesktopBuffer
	Run(desktopruntime.WithTarget(ctx, &target), args, IO{In: bytes.NewReader(r.Input), Out: &output, Err: h.diagnostic})
	var result envelope
	if output.exceeded || domain.Decode(output.Bytes(), &result) != nil || result.Version != 1 {
		return nil, domain.Fail(domain.ResourceExhausted, "The desktop command reply is unavailable.", "Inspect the original operation before retrying.")
	}
	if result.Error != nil {
		return nil, result.Error
	}
	if r.Operation == "worker.pair-local" {
		if err := desktopruntime.MarkLocal(target); err != nil {
			return nil, err
		}
	}
	return result.Result, nil
}

type boundedDesktopBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedDesktopBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > desktopReplyLimit {
		b.exceeded = true
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
