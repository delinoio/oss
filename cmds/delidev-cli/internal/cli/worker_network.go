// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func workerNetworkCommand(ctx context.Context, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("worker network " + args[0])
	root := f.String("worker-dir", filepath.Join(o.dataDir, "worker"), "")
	codeStdin := f.Bool("code-stdin", false, "")
	name := f.String("name", "local worker", "")
	input := f.String("input", "", "")
	digest := f.String("expected-ciphertext-digest", "", "")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	switch args[0] {
	case "prepare":
		if *input != "" || *digest != "" {
			return nil, usage()
		}
		var grant *worker.PairingCode
		if *codeStdin {
			raw, err := io.ReadAll(io.LimitReader(streams.In, 16<<10+1))
			defer clear(raw)
			if err != nil || len(raw) > 16<<10 {
				return nil, usage()
			}
			var value worker.PairingCode
			if domain.Decode(raw, &value) != nil || value.Validate() != nil {
				return nil, usage()
			}
			grant = &value
		}
		return worker.PrepareNetwork(ctx, *root, grant, *name, slog.New(slog.NewJSONHandler(streams.Err, nil)))
	case "import":
		if *codeStdin || *input == "" || *input == "-" || *digest == "" {
			return nil, domain.Fail(domain.InvalidArgument, "A separate expected ciphertext digest is required.", "Use --input with the encrypted file and --expected-ciphertext-digest from the authenticated export response.")
		}
		file, err := os.Open(*input)
		if err != nil {
			return nil, domain.SafeError(err)
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, workernetwork.MaxCiphertext+1))
		if err != nil || len(raw) > workernetwork.MaxCiphertext {
			return nil, usage()
		}
		return worker.ImportNetwork(ctx, *root, raw, *digest, slog.New(slog.NewJSONHandler(streams.Err, nil)))
	case "status":
		if *codeStdin || *input != "" || *digest != "" {
			return nil, usage()
		}
		return workernetwork.LoadRecipient(*root)
	default:
		return nil, usage()
	}
}
func exportWorkerNetworkCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	f := flags("network export-bundle")
	input := f.String("recipient-input", "", "")
	output := f.String("output", "", "")
	id := f.String("id", "", "")
	revision := f.Uint64("revision", 0, "")
	profile := f.String("profile-id", "", "")
	profileRevision := f.Uint64("profile-revision", 0, "")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if *input == "" || *output == "" {
		return nil, usage()
	}
	file, err := os.Open(*input)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 16<<10+1))
	if err != nil || len(raw) > 16<<10 {
		return nil, usage()
	}
	var recipient workernetwork.Recipient
	if domain.Decode(raw, &recipient) != nil || recipient.Version != 1 || recipient.Authority.Validate() != nil || recipient.Authority.Endpoint != c.endpoint {
		return nil, domain.Fail(domain.Conflict, "The recipient belongs to another selected server or endpoint.", "Select its original server before exporting an encrypted Worker route.")
	}
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if status.Msg.ServerId != string(recipient.Authority.ServerID) {
		return nil, domain.Fail(domain.Conflict, "The recipient server identity changed.", "Use the original pairing authority and protected key.")
	}
	response, err := c.network.ExportWorkerNetworkBundle(ctx, request(c, &pb.ExportWorkerNetworkBundleRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, MachineId: string(recipient.Authority.MachineID), DeviceId: string(recipient.Authority.DeviceID), PairingId: string(recipient.Authority.PairingID), Endpoint: c.endpoint, Recipient: recipient.PublicKey, KeyId: string(recipient.KeyID), ProfileId: *profile, ProfileRevision: *profileRevision, DesiredGeneration: *revision}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if len(response.Msg.Ciphertext) > workernetwork.MaxCiphertext || response.Msg.CiphertextDigest != workernetwork.Digest(response.Msg.Ciphertext) {
		return nil, domain.Fail(domain.RecoveryRequired, "The encrypted export digest is inconsistent.", "Request a current authenticated export; do not import a guessed digest.")
	}
	if err := security.WriteAtomic(*output, response.Msg.Ciphertext); err != nil {
		return nil, err
	}
	return map[string]any{"ciphertext_digest": response.Msg.CiphertextDigest, "route": resourceJSON(response.Msg.Route), "replayed": response.Msg.Replayed}, nil
}
func workerNetworkStatusCommand(ctx context.Context, c client, args []string) (any, error) {
	f := flags("network worker-status")
	machine := f.String("machine-id", "", "")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	response, err := c.network.GetWorkerNetworkStatus(ctx, request(c, &pb.GetWorkerNetworkStatusRequest{MachineId: *machine}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var value json.RawMessage
	if domain.Decode(response.Msg.StatusJson, &value) != nil {
		return nil, domain.Fail(domain.RecoveryRequired, "Unsupported Worker route status.", "Update the client and selected server.")
	}
	return value, nil
}
