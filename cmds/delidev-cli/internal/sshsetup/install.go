// SPDX-License-Identifier: Apache-2.0
package sshsetup

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

type SetupDocument struct {
	Version        uint32             `json:"version"`
	OperationID    domain.ID          `json:"operation_id"`
	ServerID       domain.ID          `json:"server_id"`
	ReleaseVersion string             `json:"release_version"`
	SourceRevision string             `json:"source_revision"`
	Artifact       updates.Artifact   `json:"artifact"`
	Grant          worker.PairingCode `json:"grant"`
	Name           string             `json:"name"`
}
type SetupResult struct {
	Version       uint32         `json:"version"`
	OperationID   domain.ID      `json:"operation_id"`
	ServerID      domain.ID      `json:"server_id"`
	DeviceID      domain.ID      `json:"device_id"`
	MachineID     domain.ID      `json:"machine_id"`
	Generation    domain.ID      `json:"generation"`
	WorkerVersion string         `json:"worker_version"`
	Target        updates.Target `json:"target"`
	Running       bool           `json:"running"`
	Reused        bool           `json:"reused"`
}

func (d SetupDocument) Validate() error {
	source, e := hex.DecodeString(d.SourceRevision)
	if e != nil || len(source) != 20 || hex.EncodeToString(source) != d.SourceRevision {
		return failure(domain.InvalidArgument)
	}
	if d.Artifact.Size <= 0 || d.Artifact.Size > updates.ArtifactLimit {
		return failure(domain.InvalidArgument)
	}
	if d.Version != 1 || d.OperationID.Validate() != nil || d.ServerID.Validate() != nil || d.Artifact.Component != updates.Worker || !d.Artifact.Target.Valid() || d.Artifact.Name != updates.ArtifactName(updates.Worker, d.Artifact.Target) || updates.ValidateArtifactURL(d.Artifact.URL, d.ReleaseVersion, d.Artifact.Name) != nil || d.Grant.ServerID != d.ServerID || d.Grant.Validate() != nil || domain.Text(d.Name, "Runner Device name", 256, true) != nil {
		return failure(domain.InvalidArgument)
	}
	return nil
}
func (r SetupResult) Validate(d SetupDocument) error {
	if r.Version != 1 || r.OperationID != d.OperationID || r.ServerID != d.ServerID || r.DeviceID.Validate() != nil || r.MachineID.Validate() != nil || r.Generation.Validate() != nil || (!r.Reused && r.WorkerVersion != d.ReleaseVersion) || r.Target != d.Artifact.Target || !r.Running {
		return failure(domain.RecoveryRequired)
	}
	if newer, e := updates.Newer(r.WorkerVersion, d.ReleaseVersion); e != nil || newer {
		return failure(domain.RecoveryRequired)
	}
	return nil
}

// Only validated UUIDs/digests become fixed installation path components.
func remotePaths(server, operation domain.ID, a updates.Artifact) (string, string, error) {
	if server.Validate() != nil || operation.Validate() != nil || !a.Target.Valid() || a.Component != updates.Worker || len(a.SHA256) != 64 {
		return "", "", failure(domain.InvalidArgument)
	}
	for _, r := range a.SHA256 {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", "", failure(domain.InvalidArgument)
		}
	}
	if strings.HasPrefix(string(a.Target), "windows-") {
		return `$env:LOCALAPPDATA+'\\DeliDev\\ssh-workers\\` + string(server) + `'`, a.SHA256 + ".exe", nil
	}
	return `"$HOME/.local/share/delidev/ssh-workers/` + string(server) + `"`, a.SHA256, nil
}
func (c *Connection) Stage(ctx context.Context, server, operation domain.ID, a updates.Artifact, source string) error {
	root, name, e := remotePaths(server, operation, a)
	if e != nil {
		return e
	}
	file, e := updates.OpenArtifact(source, a)
	if e != nil {
		return failure(domain.Unavailable)
	}
	defer file.Close()
	var command string
	if strings.HasPrefix(string(a.Target), "windows-") {
		script := `$ErrorActionPreference='Stop'; $root=` + root + `; New-Item -ItemType Directory -Force ($root+'\\bin') | Out-Null; foreach($p in @($root,($root+'\\bin'))){if((Get-Item -LiteralPath $p).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'invalid'}}; $tmp=$root+'\\bin\\.pending-` + string(operation) + `'; $dst=$root+'\\bin\\` + name + `'; if(Test-Path -LiteralPath $tmp){throw 'pending'}; $f=[IO.File]::Open($tmp,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None); try{[Console]::OpenStandardInput().CopyTo($f);$f.Flush($true)}finally{$f.Close()}; try{if((Get-Item -LiteralPath $tmp).Length -ne ` + fmt.Sprint(a.Size) + ` -or (Get-FileHash -LiteralPath $tmp -Algorithm SHA256).Hash.ToLowerInvariant() -ne '` + a.SHA256 + `'){throw 'digest'}; if(Test-Path -LiteralPath $dst){if((Get-Item -LiteralPath $dst).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'invalid'};if((Get-FileHash -LiteralPath $dst -Algorithm SHA256).Hash.ToLowerInvariant() -ne '` + a.SHA256 + `'){throw 'digest'}}else{[IO.File]::Move($tmp,$dst)};Write-Output 'DELIDEV_STAGE_V1 ` + a.SHA256 + `'}finally{if(Test-Path -LiteralPath $tmp){Remove-Item -LiteralPath $tmp}}`
		command = `powershell.exe -NoProfile -NonInteractive -Command "` + script + `"`
	} else {
		hashCommand := "sha256sum"
		if strings.HasPrefix(string(a.Target), "darwin-") {
			hashCommand = "shasum -a 256"
		}
		ownerCommand, modeCommand := "stat -c %u", "stat -c %a"
		if strings.HasPrefix(string(a.Target), "darwin-") {
			ownerCommand, modeCommand = "stat -f %u", "stat -f %Lp"
		}
		command = `umask 077; set -eu; root=` + root + `; base="$HOME"; uid=$(id -u); for part in .local share delidev ssh-workers ` + string(server) + ` bin; do test ! -L "$base"; test "$(` + ownerCommand + ` "$base")" = "$uid"; mode=$(` + modeCommand + ` "$base"); test "$((0$mode & 022))" = 0; next="$base/$part"; test ! -L "$next"; if test -e "$next"; then test -d "$next"; else mkdir "$next"; fi; base="$next"; done; test "$(` + ownerCommand + ` "$base")" = "$uid"; mode=$(` + modeCommand + ` "$base"); test "$((0$mode & 077))" = 0; tmp="$root/bin/.pending-` + string(operation) + `"; dst="$root/bin/` + name + `"; test ! -e "$tmp"; test ! -L "$tmp"; set -C; cat > "$tmp"; trap 'rm -f "$tmp"' EXIT; test "$(wc -c < "$tmp" | tr -d ' ')" = '` + fmt.Sprint(a.Size) + `'; sum=$(` + hashCommand + ` "$tmp"); test "${sum%% *}" = '` + a.SHA256 + `'; chmod 700 "$tmp"; if test -e "$dst"; then test ! -L "$dst"; sum=$(` + hashCommand + ` "$dst"); test "${sum%% *}" = '` + a.SHA256 + `'; else ln "$tmp" "$dst"; fi; sync; printf 'DELIDEV_STAGE_V1 ` + a.SHA256 + `\n'`
	}
	raw, e := c.command(ctx, command, file)
	if e != nil || strings.TrimSpace(string(raw)) != "DELIDEV_STAGE_V1 "+a.SHA256 {
		return failure(domain.RecoveryRequired)
	}
	return nil
}
func (c *Connection) Setup(ctx context.Context, d SetupDocument, inspectOnly bool) (SetupResult, error) {
	var result SetupResult
	if d.Validate() != nil {
		return result, failure(domain.InvalidArgument)
	}
	root, name, e := remotePaths(d.ServerID, d.OperationID, d.Artifact)
	if e != nil {
		return result, e
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return result, e
	}
	defer clear(raw)
	var command string
	if strings.HasPrefix(string(d.Artifact.Target), "windows-") {
		command = `powershell.exe -NoProfile -NonInteractive -Command "$root=` + root + `; & ($root+'\\bin\\` + name + `') --json --data-dir $root worker ssh-setup --worker-dir $root --operation-id '` + string(d.OperationID) + `'"`
	} else {
		command = `root=` + root + `; exec "$root/bin/` + name + `" --json --data-dir "$root" worker ssh-setup --worker-dir "$root" --operation-id '` + string(d.OperationID) + `'`
	}
	if inspectOnly {
		if strings.HasPrefix(string(d.Artifact.Target), "windows-") {
			command = strings.TrimSuffix(command, "\"") + " --inspect-only\""
		} else {
			command += " --inspect-only"
		}
	}
	output, runError := c.command(ctx, command, strings.NewReader(string(raw)))
	var envelope struct {
		Version       uint32          `json:"version"`
		RequestID     string          `json:"request_id,omitempty"`
		CorrelationID string          `json:"correlation_id,omitempty"`
		Result        json.RawMessage `json:"result,omitempty"`
		Error         json.RawMessage `json:"error,omitempty"`
	}
	if domain.DecodeWithLimit(output, &envelope, OutputLimit) == nil && len(envelope.Error) > 0 {
		var problem domain.Error
		if domain.Decode(envelope.Error, &problem) == nil {
			error := domain.Fail(domain.RecoveryRequired, "The original remote Worker setup is unconfirmed.", "Inspect the original SSH setup and private Worker status before taking another action.")
			switch problem.Cause {
			case "input-ownership", "binary-identity", "private-root", "private-journal", "existing-credential", "pairing", "startup":
				error.Cause = problem.Cause
			}
			return result, error
		}
	}
	if runError != nil || domain.DecodeWithLimit(output, &envelope, OutputLimit) != nil || envelope.Version != 1 || len(envelope.Error) != 0 || domain.Decode(envelope.Result, &result) != nil || result.Validate(d) != nil {
		return result, failure(domain.RecoveryRequired)
	}
	return result, nil
}
