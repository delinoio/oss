// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type desktopUpdatePhase string

const (
	desktopPrepared   desktopUpdatePhase = "prepared"
	desktopInstalling desktopUpdatePhase = "installing"
	desktopInstalled  desktopUpdatePhase = "installed"
	desktopFailed     desktopUpdatePhase = "failed"
	desktopUnknown    desktopUpdatePhase = "uncertain"
)

type desktopUpdateJournal struct {
	Version         uint32             `json:"version"`
	ID              domain.ID          `json:"id"`
	ServerID        domain.ID          `json:"server_id"`
	Revision        uint64             `json:"revision,string"`
	Generation      domain.ID          `json:"generation"`
	SavedConnection domain.ID          `json:"saved_connection,omitempty"`
	Phase           desktopUpdatePhase `json:"phase"`
	Manifest        []byte             `json:"manifest"`
	ManifestSHA256  string             `json:"manifest_sha256"`
	ReleaseVersion  string             `json:"release_version"`
	Target          updates.Target     `json:"target"`
	Artifact        updates.Artifact   `json:"artifact"`
	ArtifactPath    string             `json:"artifact_path"`
}

func nativeDesktopUpdate(ctx context.Context, o options, args []string) (any, error) {
	if len(args) == 0 || o.server != "" || o.tokenStdin {
		return nil, usage()
	}
	fs := flags("native desktop update")
	id := fs.String("id", "", "original candidate")
	revision := fs.Uint64("revision", 0, "original candidate revision")
	saved := fs.String("saved-connection", "", "native selected saved connection")
	server := fs.String("server-id", "", "native selected server identity")
	generation := fs.String("native-generation", "", "native original window generation")
	outcome := fs.String("outcome", "", "native installation outcome")
	if parse(fs, args[1:]) != nil || domain.ID(*id).Validate() != nil || domain.ID(*server).Validate() != nil || domain.ID(*generation).Validate() != nil || *revision == 0 {
		return nil, usage()
	}
	// Local committed/unknown outcomes outlive network availability and server
	// candidate changes. This original native journal cannot grant installation.
	if args[0] == "native-outcome" || args[0] == "native-inspect" {
		directory := filepath.Join(o.dataDir, "desktop-updates")
		if e := security.PrivateDir(directory); e != nil {
			return nil, e
		}
		lock, e := security.TryLock(filepath.Join(directory, "control.lock"))
		if e != nil {
			return nil, e
		}
		defer lock.Close()
		path := filepath.Join(directory, *id+".json")
		raw, e := security.ReadPrivate(path, 256<<10)
		if e != nil {
			return nil, e
		}
		var j desktopUpdateJournal
		if domain.DecodeWithLimit(raw, &j, 256<<10) != nil || j.Version != 1 || j.ID != domain.ID(*id) || j.ServerID != domain.ID(*server) || j.Revision != *revision || j.SavedConnection != domain.ID(*saved) || (args[0] != "native-inspect" && j.Generation != domain.ID(*generation)) {
			return nil, workerUpdateFailure()
		}
		if args[0] == "native-outcome" {
			next := desktopUpdatePhase(*outcome)
			if next != desktopInstalled && next != desktopFailed && next != desktopUnknown {
				return nil, usage()
			}
			if j.Phase != desktopInstalling && j.Phase != next {
				return nil, workerUpdateFailure()
			}
			j.Phase = next
			raw, e = json.Marshal(j)
			if e != nil {
				return nil, e
			}
			if e = security.WriteAtomic(path, raw); e != nil {
				return nil, e
			}
		}
		return desktopUpdateDescriptor(j), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	c, e := nativeUpdateClient(o, *saved)
	if e != nil {
		return nil, e
	}
	defer c.transport.CloseIdleConnections()
	status, e := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if e != nil {
		return nil, rpc.ClientError(e)
	}
	if status.Msg.ServerId != *server || status.Msg.ProtocolVersion != rpc.ProtocolVersion {
		return nil, workerUpdateFailure()
	}
	response, e := c.installation.GetUpdate(ctx, request(c, &pb.GetUpdateRequest{Id: *id}))
	if e != nil {
		return nil, rpc.ClientError(e)
	}
	if response.Msg.Update == nil || response.Msg.Update.Id != *id || response.Msg.Update.Revision != *revision {
		return nil, workerUpdateFailure()
	}
	var operation updates.Operation
	if domain.Decode(response.Msg.Update.DocumentJson, &operation) != nil || operation.ServerID != domain.ID(*server) || operation.Component != updates.Desktop || operation.State != updates.Observed || operation.CurrentVersion != rpc.Version {
		return nil, workerUpdateFailure()
	}
	release, e := updates.NewClient()
	if e != nil {
		return nil, e
	}
	defer release.Close()
	verified, e := release.VerifyManifest(operation.Manifest, rpc.Version, time.Now().UTC())
	target, te := updates.SelectTarget(runtime.GOOS, runtime.GOARCH)
	if e != nil || te != nil || operation.Target != target || verified.ManifestSHA256 != operation.ManifestSHA256 || verified.Payload.Version != operation.Version {
		return nil, workerUpdateFailure()
	}
	artifact, e := verified.Artifact(updates.Desktop, target)
	if e != nil {
		return nil, e
	}
	directory := filepath.Join(o.dataDir, "desktop-updates")
	if e = security.PrivateDir(directory); e != nil {
		return nil, e
	}
	lock, e := security.TryLock(filepath.Join(directory, "control.lock"))
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	path := filepath.Join(directory, *id+".json")
	var j desktopUpdateJournal
	raw, readError := security.ReadPrivate(path, 256<<10)
	if readError == nil {
		if domain.DecodeWithLimit(raw, &j, 256<<10) != nil || j.ID != domain.ID(*id) || j.ServerID != domain.ID(*server) || j.Revision != *revision || j.SavedConnection != domain.ID(*saved) || j.ManifestSHA256 != verified.ManifestSHA256 {
			return nil, workerUpdateFailure()
		}
	} else if !errors.Is(readError, os.ErrNotExist) {
		return nil, workerUpdateFailure()
	}
	if args[0] == "native-prepare" {
		if readError == nil && j.Phase != desktopPrepared {
			return nil, workerUpdateFailure()
		}
		source, e := release.Download(ctx, verified, updates.Desktop, target, filepath.Join(directory, "downloads"))
		if e != nil {
			return nil, e
		}
		j = desktopUpdateJournal{Version: 1, ID: domain.ID(*id), ServerID: domain.ID(*server), Revision: *revision, Generation: domain.ID(*generation), SavedConnection: domain.ID(*saved), Phase: desktopPrepared, Manifest: verified.Canonical, ManifestSHA256: verified.ManifestSHA256, ReleaseVersion: verified.Payload.Version, Target: target, Artifact: artifact, ArtifactPath: source}
	} else {
		if readError != nil || j.Generation != domain.ID(*generation) || j.Version != 1 || updates.VerifyFile(j.ArtifactPath, artifact) != nil {
			return nil, workerUpdateFailure()
		}
		switch args[0] {
		case "native-begin":
			if j.Phase != desktopPrepared {
				return nil, workerUpdateFailure()
			}
			j.Phase = desktopInstalling
		case "native-outcome":
			if j.Phase != desktopInstalling {
				return nil, workerUpdateFailure()
			}
			switch *outcome {
			case "installed":
				j.Phase = desktopInstalled
			case "failed":
				j.Phase = desktopFailed
			case "uncertain":
				j.Phase = desktopUnknown
			default:
				return nil, usage()
			}
		case "native-verify":
			if j.Phase != desktopPrepared {
				return nil, workerUpdateFailure()
			}
		default:
			return nil, usage()
		}
	}
	raw, e = json.Marshal(j)
	if e != nil {
		return nil, e
	}
	if e = security.WriteAtomic(path, raw); e != nil {
		return nil, e
	}
	// Only the closed native host consumes this path; renderer projections omit it.
	return desktopUpdateDescriptor(j), nil
}

func desktopUpdateDescriptor(j desktopUpdateJournal) any {
	return map[string]any{"version": uint32(1), "operation_id": j.ID, "server_id": j.ServerID, "generation": j.Generation, "release_version": j.ReleaseVersion, "target": j.Target, "phase": j.Phase, "artifact_path": j.ArtifactPath, "artifact_sha256": j.Artifact.SHA256, "artifact_size": j.Artifact.Size, "manifest_sha256": j.ManifestSHA256}
}
