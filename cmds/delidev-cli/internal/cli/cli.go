// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"os"
	"strings"
	"time"
)

type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type options struct {
	dataDir, server string
	requestID       domain.ID
	tokenStdin      bool
}

func Run(ctx context.Context, args []string, streams IO) int {
	if streams.In == nil {
		streams.In = os.Stdin
	}
	if streams.Out == nil {
		streams.Out = os.Stdout
	}
	if streams.Err == nil {
		streams.Err = os.Stderr
	}
	o, remaining, err := globals(args)
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if err != nil {
		return emit(nil, err)
	}
	if len(remaining) == 0 || remaining[0] == "help" || remaining[0] == "--help" {
		fmt.Fprint(streams.Out, help)
		return 0
	}
	if remaining[0] == "version" || remaining[0] == "--version" {
		return emit(map[string]any{"version": rpc.Version, "protocol_version": rpc.ProtocolVersion}, nil)
	}
	if remaining[0] == "presentation" {
		if o.tokenStdin {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Local presentation does not accept server authentication input.", "Omit --token-stdin; no server connection is needed."))
		}
		value, err := githubPresentationCommand(ctx, remaining[1:], streams.In)
		return emit(value, err)
	}
	if o.dataDir == "" {
		o.dataDir, err = DefaultDataDir()
		if err != nil {
			return emit(nil, domain.SafeError(err))
		}
	}
	command := remaining[0]
	rest := remaining[1:]
	if command == "service-run" {
		value, err := runService(ctx, o, rest, streams)
		return emit(value, err)
	}
	if (command == "server" || command == "worker") && len(rest) > 0 && rest[0] == "service" {
		kind := userservice.Server
		if command == "worker" {
			kind = userservice.Worker
		}
		value, err := serviceCommand(ctx, o, kind, rest[1:], streams)
		return emit(value, err)
	}
	if command == "device" && len(rest) > 0 && (rest[0] == "inspect-local" || rest[0] == "recover-local") {
		value, err := desktopRecoveryCommand(ctx, o, rest)
		return emit(value, err)
	}
	if command == "server" && len(rest) > 0 && (rest[0] == "start" || rest[0] == "run" || rest[0] == "ensure" || rest[0] == "desktop-launch" || rest[0] == "desktop-status" || rest[0] == "desktop-retry") {
		value, err := start(ctx, o, rest, streams)
		return emit(value, err)
	}
	if command == "connection" {
		value, err := connectionCommand(ctx, o, rest, streams)
		return emit(value, err)
	}
	if command == "settings" && len(rest) == 1 && rest[0] == "defaults" {
		return emit(domain.DefaultSettings(), nil)
	}
	if command == "worker" || (command == "device" && len(rest) > 0 && (rest[0] == "pair" || rest[0] == "pair-local" || rest[0] == "inspect")) {
		value, err := deviceLocal(ctx, o, command, rest, streams)
		return emit(value, err)
	}
	if command == "account" && len(rest) > 0 && rest[0] == "connect" && o.tokenStdin {
		for _, arg := range rest[1:] {
			name, _, _ := strings.Cut(arg, "=")
			if name == "--key-stdin" {
				return emit(nil, domain.Fail(domain.InvalidArgument, "Server authentication and the API key cannot share stdin.", "Use the owner scope or pair a client before connecting an API key."))
			}
		}
	}
	if command == "integration" && len(rest) > 0 && rest[0] == "replace-token" && o.tokenStdin {
		return emit(nil, domain.Fail(domain.InvalidArgument, "Server authentication and a PAT cannot share stdin.", "Use a paired client or local owner connection before supplying --pat-stdin."))
	}
	c, err := connectClient(o, streams.In)
	if err != nil {
		if command == "server" && len(rest) == 1 && rest[0] == "stop" && o.server == "" && !o.tokenStdin && domain.SafeError(err).Code == domain.ServerUnavailable {
			ensureRequest(&o)
			if suppressErr := server.SuppressLocalRestart(o.dataDir, o.requestID); suppressErr != nil {
				return emit(nil, suppressErr)
			}
			return emit(nil, offlineStop())
		}
		return emit(nil, err)
	}
	defer c.transport.CloseIdleConnections()
	if command != "events" && !(command == "session" && (followsTerminalOutput(rest) || (len(rest) >= 2 && rest[0] == "forward" && rest[1] == "start"))) {
		limit := 30 * time.Second
		// Network credential work and backup inspection/replacement own bounded
		// 30-second server work. Allow its typed outcome to arrive first.
		if command == "network" || command == "backup" && len(rest) > 0 && (rest[0] == "restore" || rest[0] == "inspect") {
			limit = 35 * time.Second
			c.transport.ResponseHeaderTimeout = limit
		}
		if command == "github" {
			limit = 40 * time.Second
			c.transport.ResponseHeaderTimeout = limit
		}
		if command == "integration" {
			c.transport.ResponseHeaderTimeout = 25 * time.Second
			if len(rest) > 0 && rest[0] == "inspect-repository" {
				limit = 40 * time.Second
				c.transport.ResponseHeaderTimeout = limit
			}
		}
		if command == "session" && len(rest) > 0 {
			switch rest[0] {
			case "fork":
				// Fork copies every repository under a two-minute Worker bound.
				// A command timeout retains the accepted job for observation.
				limit = 145 * time.Second
			case "pr":
				// Linking refreshes GitHub identities before committing metadata.
				limit = 45 * time.Second
				c.transport.ResponseHeaderTimeout = limit
			case "files", "diff", "review-context":
				// The Worker observation owns a 15-second deadline. Leave
				// room for its typed result instead of racing its response.
				c.transport.ResponseHeaderTimeout = 20 * time.Second
			case "review":
				// Comment creation and grouped submission have server limits
				// of 30 and 45 seconds. The default 15-second header timeout
				// would abandon valid work before either operation finishes.
				limit = 50 * time.Second
				c.transport.ResponseHeaderTimeout = limit
			}
		}
		bounded, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		ctx = bounded
	}
	switch command {
	case "service-control":
		if code, handled := dispatchServiceControl(ctx, c, o, rest, streams); handled {
			return code
		}
	case "usage":
		if code, handled := dispatchUsage(ctx, c, o, rest, streams); handled {
			return code
		}
	case "activity":
		if code, handled := dispatchActivity(ctx, c, o, rest, streams); handled {
			return code
		}
	case "search":
		if code, handled := dispatchSearch(ctx, c, o, rest, streams); handled {
			return code
		}
	case "schedule":
		if code, handled := dispatchSchedule(ctx, c, o, rest, streams); handled {
			return code
		}
	case "notification":
		if code, handled := dispatchNotification(ctx, c, o, rest, streams); handled {
			return code
		}
	case "inbox":
		if code, handled := dispatchInbox(ctx, c, o, rest, streams); handled {
			return code
		}
	case "interaction":
		if code, handled := dispatchInteraction(ctx, c, o, rest, streams); handled {
			return code
		}
	case "session":
		if code, handled := dispatchSession(ctx, c, o, rest, streams); handled {
			return code
		}
	case "queue":
		if code, handled := dispatchQueue(ctx, c, o, rest, streams); handled {
			return code
		}
	case "provider":
		if code, handled := dispatchProvider(ctx, c, o, rest, streams); handled {
			return code
		}
	case "model":
		if code, handled := dispatchModel(ctx, c, o, rest, streams); handled {
			return code
		}
	case "github":
		if code, handled := dispatchGithub(ctx, c, o, rest, streams); handled {
			return code
		}
	case "network":
		if code, handled := dispatchNetwork(ctx, c, o, rest, streams); handled {
			return code
		}
	case "integration":
		if code, handled := dispatchIntegration(ctx, c, o, rest, streams); handled {
			return code
		}
	case "account":
		if code, handled := dispatchAccount(ctx, c, o, rest, streams); handled {
			return code
		}
	case "machine":
		if code, handled := dispatchMachine(ctx, c, o, rest, streams); handled {
			return code
		}
	case "device":
		if code, handled := dispatchDevice(ctx, c, o, rest, streams); handled {
			return code
		}
	case "repository":
		if code, handled := dispatchRepository(ctx, c, o, rest, streams); handled {
			return code
		}
	case "server":
		if code, handled := dispatchServer(ctx, c, o, rest, streams); handled {
			return code
		}
	case "doctor":
		if code, handled := dispatchDoctor(ctx, c, o, rest, streams); handled {
			return code
		}
	case "configuration":
		if code, handled := dispatchConfiguration(ctx, c, o, rest, streams); handled {
			return code
		}
	case "backup":
		if code, handled := dispatchBackup(ctx, c, o, rest, streams); handled {
			return code
		}
	case "events":
		if code, handled := dispatchEvents(ctx, c, o, rest, streams); handled {
			return code
		}
	}
	kind := domain.Kind(command)
	if !kind.Valid() || rpc.WireKind(kind) == pb.EntityKind_ENTITY_KIND_UNSPECIFIED {
		return emit(nil, usage())
	}
	if len(rest) == 0 {
		return emit(nil, usage())
	}
	action := rest[0]
	rest = rest[1:]
	fs := flags(command + " " + action)
	id := fs.String("id", "", "entity ID")
	revision := fs.Uint64("revision", 0, "expected entity revision")
	wait := fs.Bool("wait", false, "wait for asynchronous configuration validation")
	input := fs.String("input", "-", "configuration JSON file, or - for stdin")
	limit := fs.Uint("limit", 50, "page size")
	page := fs.String("page-token", "", "page token")
	project := fs.String("project-id", "", "project scope")
	session := fs.String("session-id", "", "session scope")
	accountType := fs.String("account-type", "", "account type: api or subscription")
	providerID := fs.String("provider-id", "", "account provider ID")
	if err := parse(fs, rest); err != nil {
		return emit(nil, err)
	}
	switch action {
	case "list", "snapshot":
		if *limit > 200 || *limit == 0 {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use 1 through 200."))
		}
		if (*accountType != "" || *providerID != "") && kind != domain.AccountKind {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Account filters require account resources.", "Select account as the resource kind."))
		}
		if action == "snapshot" && (*accountType != "" || *providerID != "") {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Account filters apply only to paginated account lists.", "Use account list instead of account snapshot."))
		}
		f := &pb.Filter{Kind: rpc.WireKind(kind), PageSize: uint32(*limit), PageToken: *page, ProjectId: *project, SessionId: *session}
		if action == "snapshot" {
			response, err := c.resources.GetSnapshot(ctx, request(c, &pb.GetSnapshotRequest{Filter: f}))
			if err != nil {
				return emit(nil, rpc.ClientError(err))
			}
			return emit(map[string]any{"resources": resourcesJSON(response.Msg.Resources), "cursor": response.Msg.Cursor}, nil)
		}
		var selectedType pb.AccountTypeFilter
		switch *accountType {
		case "":
		case "api":
			selectedType = pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_API
		case "subscription":
			selectedType = pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_SUBSCRIPTION
		default:
			return emit(nil, domain.Fail(domain.InvalidArgument, "Unknown account type filter.", "Select api or subscription."))
		}
		response, err := listWithProviderFilter(ctx, c, f, *providerID, selectedType)
		if err != nil {
			return emit(nil, err)
		}
		return emit(map[string]any{"resources": resourcesJSON(response.Resources), "next_page_token": response.NextPageToken}, nil)
	case "get", "inspect":
		if err := domain.ID(*id).Validate(); err != nil {
			return emit(nil, err)
		}
		response, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: rpc.WireKind(kind), Id: *id}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		return emit(resourceJSON(response.Msg.Resource), nil)
	case "create", "edit", "save":
		if o.tokenStdin && *input == "-" {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Credential stdin and document stdin cannot share one stream.", "Pass the non-secret configuration document with --input PATH."))
		}
		if action == "edit" && (*id == "" || *revision == 0) {
			return emit(nil, domain.Fail(domain.MissingInput, "Editing requires an ID and expected revision.", "Read the entity, then pass --id and --revision."))
		}
		if action == "create" && (*id != "" || *revision != 0) {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Create allocates a new entity identity.", "Omit --id and --revision for create."))
		}
		body, err := readDocument(*input, streams.In)
		if err != nil {
			return emit(nil, err)
		}
		ensureRequest(&o)
		response, err := c.configuration.SaveConfiguration(ctx, request(c, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, Kind: rpc.WireKind(kind), SchemaVersion: 1, DocumentJson: body}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		if response.Msg.Job != nil {
			job := response.Msg.Job
			if *wait {
				job, err = awaitJob(ctx, c, job)
				if err != nil {
					return emit(map[string]any{"job": resourceJSON(job)}, err)
				}
			}
			var state domain.Job
			if err := domain.Decode(job.DocumentJson, &state); err != nil {
				return emit(nil, err)
			}
			if state.State == domain.JobSucceeded {
				var output struct {
					ID       string `json:"id"`
					Revision uint64 `json:"revision"`
				}
				if err := domain.Decode(state.Output, &output); err != nil {
					return emit(nil, err)
				}
				saved, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: rpc.WireKind(kind), Id: output.ID}))
				if err != nil {
					return emit(map[string]any{"job": resourceJSON(job)}, rpc.ClientError(err))
				}
				return emit(map[string]any{"resource": resourceJSON(saved.Msg.Resource), "job": resourceJSON(job), "replayed": response.Msg.Replayed}, nil)
			}
			value := map[string]any{"job": resourceJSON(job), "replayed": response.Msg.Replayed}
			if *wait && state.Problem != nil {
				return emit(value, state.Problem)
			}
			return emit(value, nil)
		}
		return emit(map[string]any{"resource": resourceJSON(response.Msg.Resource), "replayed": response.Msg.Replayed}, nil)
	case "delete":
		if *id == "" || *revision == 0 {
			return emit(nil, domain.Fail(domain.MissingInput, "Deletion requires an ID and expected revision.", "Read the entity and provide both fields."))
		}
		ensureRequest(&o)
		response, err := c.configuration.DeleteConfiguration(ctx, request(c, &pb.DeleteConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, Kind: rpc.WireKind(kind)}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		return emit(response.Msg, nil)
	case "routing":
		if kind != domain.AgentKind {
			return emit(nil, usage())
		}
		response, err := c.configuration.PreviewRouting(ctx, request(c, &pb.PreviewRoutingRequest{AgentId: *id, ProjectId: *project}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		return emit(json.RawMessage(response.Msg.RouteJson), nil)
	default:
		return emit(nil, usage())
	}
}
