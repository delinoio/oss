// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"golang.org/x/term"
)

type terminalOutputOptions struct {
	id, epoch string
	after     uint64
	follow    bool
}

func parseTerminalOutput(action string, args []string) (terminalOutputOptions, error) {
	var value terminalOutputOptions
	f := flags("session terminal " + action)
	f.StringVar(&value.id, "id", "", "terminal ID")
	f.StringVar(&value.epoch, "epoch", "", "last output epoch")
	f.Uint64Var(&value.after, "after", 0, "last acknowledged output sequence")
	f.BoolVar(&value.follow, "follow", false, "stream versioned JSON frames until exit/disconnection")
	err := parse(f, args)
	return value, err
}

func followsTerminalOutput(args []string) bool {
	if len(args) < 2 || args[0] != "terminal" || (args[1] != "output" && args[1] != "reattach") {
		return false
	}
	value, err := parseTerminalOutput(args[1], args[2:])
	return err == nil && value.follow
}

func sessionTerminalCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "A terminal operation is required.", "Use create, list, inspect, input, resize, output, reattach or close.")
	}
	action := args[0]
	f := flags("session terminal " + action)
	id := f.String("id", "", "session ID for create/list, terminal ID for other operations")
	switch action {
	case "create", "input", "resize", "close":
		revision := f.Uint64("revision", 0, "current session or terminal revision")
		var shell, input *string
		rows, columns := new(uint), new(uint)
		if action == "create" {
			shell = f.String("shell", "", "absolute Worker shell override; omitted uses its native default")
			rows = f.Uint("rows", 24, "terminal rows")
			columns = f.Uint("columns", 80, "terminal columns")
		}
		if action == "resize" {
			rows = f.Uint("rows", 24, "terminal rows")
			columns = f.Uint("columns", 80, "terminal columns")
		}
		if action == "input" {
			input = f.String("input", "-", "file or stdin containing exact terminal bytes")
		}
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		if *revision == 0 || *rows > 500 || *columns > 1000 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid terminal revision or dimensions.", "Use the current revision and bounded dimensions.")
		}
		meta := &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}
		var change *pb.CreateTerminalResponse
		if action == "create" {
			response, err := c.terminals.CreateTerminal(ctx, request(c, &pb.CreateTerminalRequest{Mutation: meta, ShellOverride: *shell, Rows: uint32(*rows), Columns: uint32(*columns)}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			change = response.Msg
		} else {
			message := &pb.ControlTerminalRequest{Mutation: meta}
			switch action {
			case "close":
				message.Action = pb.TerminalAction_TERMINAL_ACTION_CLOSE
			case "resize":
				message.Action, message.Rows, message.Columns = pb.TerminalAction_TERMINAL_ACTION_RESIZE, uint32(*rows), uint32(*columns)
			case "input":
				message.Action = pb.TerminalAction_TERMINAL_ACTION_INPUT
				if o.tokenStdin && *input == "-" {
					return nil, domain.Fail(domain.MissingInput, "Credentials and terminal input cannot share stdin.", "Use --input FILE for terminal bytes.")
				}
				reader := streams.In
				if file, ok := reader.(*os.File); *input == "-" && ok && term.IsTerminal(int(file.Fd())) {
					return nil, domain.Fail(domain.MissingInput, "Terminal input requires piped bytes or a file.", "Pipe bytes into stdin or select --input FILE.")
				}
				if *input != "-" {
					file, err := os.Open(*input)
					if err != nil {
						return nil, domain.Fail(domain.Unavailable, "Terminal input could not be read.", "Select a readable byte file.")
					}
					defer file.Close()
					reader = file
				}
				raw, err := io.ReadAll(io.LimitReader(reader, domain.MaxTerminalInput+1))
				if err != nil || len(raw) == 0 || len(raw) > domain.MaxTerminalInput {
					return nil, domain.Fail(domain.InvalidArgument, "Terminal input exceeds its bound or is empty.", "Send 1–32768 bytes.")
				}
				message.Input = raw
			}
			response, err := c.terminals.ControlTerminal(ctx, request(c, message))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			change = &pb.CreateTerminalResponse{Terminal: response.Msg.Terminal, Replayed: response.Msg.Replayed}
		}
		return map[string]any{"terminal": resourceJSON(change.Terminal), "replayed": change.Replayed}, nil
	case "list":
		page := f.String("page-token", "", "bounded terminal history cursor")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		response, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_TERMINAL, SessionId: *id, PageToken: *page}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		records := []any{}
		for _, r := range response.Msg.Resources {
			records = append(records, resourceJSON(r))
		}
		return map[string]any{"terminals": records, "next_page_token": response.Msg.NextPageToken}, nil
	case "inspect":
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		response, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_TERMINAL, Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return resourceJSON(response.Msg.Resource), nil
	case "output", "reattach":
		output, err := parseTerminalOutput(action, args[1:])
		if err != nil {
			return nil, err
		}
		stream, err := c.terminals.WatchTerminalOutput(ctx, request(c, &pb.WatchTerminalOutputRequest{TerminalId: output.id, Epoch: output.epoch, AfterSequence: output.after}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		defer stream.Close()
		encoder := json.NewEncoder(streams.Out)
		var latest any
		for stream.Receive() {
			message := stream.Msg()
			latest = map[string]any{"epoch": message.Epoch, "sequence": strconv.FormatUint(message.Sequence, 10), "data": message.Data, "gap": message.Gap, "heartbeat": message.Heartbeat, "terminal": resourceJSON(message.Terminal)}
			if !output.follow {
				return latest, nil
			}
			if err := encoder.Encode(envelope{Version: 1, Result: latest}); err != nil {
				return latest, err
			}
		}
		if err := stream.Err(); err != nil {
			return latest, rpc.ClientError(err)
		}
		return map[string]any{"detached": true, "last_frame": latest}, nil
	default:
		return nil, domain.Fail(domain.InvalidArgument, "Unknown terminal operation.", "Use create, list, inspect, input, resize, output, reattach or close.")
	}
}
