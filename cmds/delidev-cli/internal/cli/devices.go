package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type pairingAttempt struct {
	Name  string             `json:"name"`
	Type  domain.DeviceType  `json:"type"`
	Grant worker.PairingCode `json:"grant"`
}

func deviceRemote(ctx context.Context, c client, o options, args []string) (any, error) {
	switch args[0] {
	case "create-pairing":
		fs := flags("device create-pairing")
		name := fs.String("name", "", "device name")
		kind := fs.String("type", "", "client or worker")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.Text(*name, "device name", 256, true); err != nil {
			return nil, err
		}
		deviceType := domain.DeviceType(*kind)
		wire := pb.DeviceType_DEVICE_TYPE_UNSPECIFIED
		switch deviceType {
		case domain.ClientDevice:
			wire = pb.DeviceType_DEVICE_TYPE_CLIENT
		case domain.WorkerDevice:
			wire = pb.DeviceType_DEVICE_TYPE_WORKER
		default:
			return nil, domain.Fail(domain.InvalidArgument, "A pairing type is required.", "Select --type worker or --type client.")
		}
		status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		root := filepath.Join(o.dataDir, "pairing-codes")
		if err := security.PrivateDir(o.dataDir); err != nil {
			return nil, err
		}
		if err := security.PrivateDir(root); err != nil {
			return nil, err
		}
		lock, err := security.TryLock(filepath.Join(root, string(o.requestID)+".lock"))
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		path := filepath.Join(root, string(o.requestID)+".pending.json")
		var attempt pairingAttempt
		raw, err := security.ReadPrivate(path, 32<<10)
		if errors.Is(err, os.ErrNotExist) {
			code, err := worker.RandomToken()
			if err != nil {
				return nil, err
			}
			attempt = pairingAttempt{Name: *name, Type: deviceType, Grant: worker.PairingCode{Version: 1, Code: code, ServerID: domain.ID(status.Msg.ServerId), Endpoint: c.endpoint}}
			raw, err = json.Marshal(attempt)
			if err != nil {
				return nil, err
			}
			if err := security.WriteAtomic(path, raw); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			if err := domain.Decode(raw, &attempt); err != nil {
				return nil, err
			}
			if attempt.Name != *name || attempt.Type != deviceType || attempt.Grant.Endpoint != c.endpoint || string(attempt.Grant.ServerID) != status.Msg.ServerId {
				return nil, domain.Fail(domain.Conflict, "The request ID belongs to a different pairing attempt.", "Retry the exact original parameters or use a new request ID.")
			}
		}
		hash := sha256.Sum256([]byte(attempt.Grant.Code))
		result, err := c.devices.CreatePairing(ctx, request(c, &pb.CreatePairingRequest{RequestId: string(o.requestID), Name: *name, Type: wire, CodeDigest: hash[:]}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		attempt.Grant.PairingID = domain.ID(result.Msg.Pairing.Id)
		codePath := filepath.Join(root, string(o.requestID)+".json")
		raw, err = json.Marshal(attempt.Grant)
		if err != nil {
			return nil, err
		}
		if err := security.WriteAtomic(codePath, raw); err != nil {
			return nil, err
		}
		return map[string]any{"pairing": resourceJSON(result.Msg.Pairing), "code_file": codePath, "replayed": result.Msg.Replayed}, nil
	case "revoke":
		fs := flags("device revoke")
		id := fs.String("id", "", "device ID")
		revision := fs.Uint64("revision", 0, "device revision")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		response, err := c.devices.RevokeDevice(ctx, request(c, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"device": resourceJSON(response.Msg.Device), "replayed": response.Msg.Replayed}, nil
	default:
		return nil, usage()
	}
}
func deviceLocal(ctx context.Context, o options, command string, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	fs := flags(command + " " + args[0])
	rootName := "device-dir"
	defaultRoot := filepath.Join(o.dataDir, "client")
	kind := domain.ClientDevice
	if command == "worker" {
		rootName = "worker-dir"
		defaultRoot = filepath.Join(o.dataDir, "worker")
		kind = domain.WorkerDevice
	}
	root := fs.String(rootName, defaultRoot, "private device scope")
	switch args[0] {
	case "pair":
		input := fs.Bool("code-stdin", false, "read private pairing document from stdin")
		name := fs.String("name", "local worker", "execution machine name")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if !*input || terminalInput(streams.In) {
			return nil, domain.Fail(domain.MissingInput, "A private pairing document is required on stdin.", "Pipe the file returned by device create-pairing; never put its contents in argv.")
		}
		raw, err := io.ReadAll(io.LimitReader(streams.In, (32<<10)+1))
		if err != nil || len(raw) > 32<<10 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid pairing input.", "Provide one bounded pairing JSON document.")
		}
		var grant worker.PairingCode
		if err := domain.Decode(raw, &grant); err != nil {
			return nil, err
		}
		if o.server != "" && o.server != grant.Endpoint {
			return nil, domain.Fail(domain.Conflict, "The selected endpoint differs from the pairing grant.", "Use the endpoint bound to the private pairing document.")
		}
		credential, err := worker.Pair(ctx, *root, grant, kind, *name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"device_id": credential.DeviceID, "machine_id": credential.MachineID, "server_id": credential.ServerID, "device_dir": *root, "paired": true}, nil
	case "start":
		if command != "worker" {
			return nil, usage()
		}
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if o.server != "" {
			return nil, domain.Fail(domain.InvalidArgument, "Worker startup uses its paired endpoint.", "Select the private Worker directory.")
		}
		err := worker.Run(ctx, worker.Config{Root: *root, Logger: slog.New(slog.NewJSONHandler(streams.Err, nil)), Ready: func(id domain.ID) {
			_ = json.NewEncoder(streams.Out).Encode(envelope{Version: 1, Result: map[string]any{"status": "ready", "machine_id": id}})
		}})
		return map[string]any{"status": "stopped"}, err
	default:
		return nil, usage()
	}
}
func repositoryInspect(ctx context.Context, c client, o options, args []string) (any, error) {
	fs := flags("repository inspect")
	machine := fs.String("machine-id", "", "execution machine")
	path := fs.String("path", "", "Worker checkout path")
	preferred := fs.String("preferred-remote", "", "configured remote")
	wait := fs.Bool("wait", false, "wait for completion within the command deadline")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	response, err := c.workers.InspectRepository(ctx, request(c, &pb.InspectRepositoryRequest{RequestId: string(o.requestID), MachineId: *machine, Path: *path, PreferredRemote: *preferred}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	job := response.Msg.Job
	if *wait {
		job, err = awaitJob(ctx, c, job)
		if err != nil {
			return map[string]any{"job": resourceJSON(job)}, err
		}
		var state domain.Job
		if err := domain.Decode(job.DocumentJson, &state); err != nil {
			return nil, err
		}
		if state.Problem != nil {
			return map[string]any{"job": resourceJSON(job)}, state.Problem
		}
	}
	return map[string]any{"job": resourceJSON(job), "replayed": response.Msg.Replayed}, nil
}
func awaitJob(ctx context.Context, c client, job *pb.Resource) (*pb.Resource, error) {
	bounded, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for {
		latest, err := c.resources.GetResource(bounded, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: job.Id}))
		if err != nil {
			if bounded.Err() != nil {
				return job, domain.SafeError(bounded.Err())
			}
			return job, rpc.ClientError(err)
		}
		job = latest.Msg.Resource
		var state domain.Job
		if err := domain.Decode(job.DocumentJson, &state); err != nil {
			return job, err
		}
		if state.State == domain.JobUncertain {
			if state.Problem != nil {
				return job, state.Problem
			}
			return job, domain.Fail(domain.RecoveryRequired, "The accepted job requires reconciliation.", "Inspect the retained job before retrying; waiting does not cancel or repeat it.")
		}
		if state.State.Terminal() {
			return job, nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return job, domain.SafeError(bounded.Err())
		case <-timer.C:
		}
	}
}
