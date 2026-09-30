package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"golang.org/x/term"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
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
type envelope struct {
	Version   int           `json:"version"`
	RequestID domain.ID     `json:"request_id,omitempty"`
	Result    any           `json:"result,omitempty"`
	Error     *domain.Error `json:"error,omitempty"`
}
type client struct {
	transport     *http.Transport
	system        delidevv1connect.SystemServiceClient
	resources     delidevv1connect.ResourceServiceClient
	search        delidevv1connect.SearchServiceClient
	activity      delidevv1connect.ActivityServiceClient
	usage         delidevv1connect.UsageServiceClient
	configuration delidevv1connect.ConfigurationServiceClient
	devices       delidevv1connect.DeviceServiceClient
	workers       delidevv1connect.WorkerServiceClient
	accounts      delidevv1connect.AccountServiceClient
	integrations  delidevv1connect.IntegrationServiceClient
	network       delidevv1connect.NetworkServiceClient
	providers     delidevv1connect.ProviderServiceClient
	sessions      delidevv1connect.SessionServiceClient
	interactions  delidevv1connect.InteractionServiceClient
	inbox         delidevv1connect.InboxServiceClient
	schedules     delidevv1connect.ScheduleServiceClient
	endpoint      string
	token         string
}

func request[T any](c client, message *T) *connect.Request[T] {
	r := connect.NewRequest(message)
	r.Header().Set("Authorization", "Bearer "+c.token)
	return r
}
func DefaultDataDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "delidev"), nil
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
	emit := func(value any, err error) int {
		response := envelope{Version: 1, RequestID: o.requestID, Result: value}
		code := 0
		if err != nil {
			response.Error = domain.SafeError(err)
			code = response.Error.ExitCode()
		}
		if e := json.NewEncoder(streams.Out).Encode(response); e != nil {
			return 1
		}
		return code
	}
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
	if command == "device" && len(rest) > 0 && (rest[0] == "inspect-local" || rest[0] == "recover-local") {
		value, err := desktopRecoveryCommand(ctx, o, rest)
		return emit(value, err)
	}
	if command == "server" && len(rest) > 0 && (rest[0] == "start" || rest[0] == "run" || rest[0] == "ensure") {
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
	if command != "events" {
		limit := 30 * time.Second
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
	case "usage":
		if len(rest) > 0 && rest[0] == "pricing" {
			if len(rest) > 1 && rest[1] == "set" {
				ensureRequest(&o)
			}
			value, err := pricingCommand(ctx, c, o, rest[1:], streams.In)
			return emit(value, err)
		}
		if len(rest) > 0 && rest[0] == "summary" {
			value, err := usageCommand(ctx, c, rest)
			return emit(value, err)
		}
	case "activity":
		value, err := activityCommand(ctx, c, rest)
		return emit(value, err)
	case "search":
		value, err := searchCommand(ctx, c, rest)
		return emit(value, err)
	case "schedule":
		if len(rest) > 0 && rest[0] != "snapshot" {
			switch rest[0] {
			case "create", "edit", "delete", "pause", "resume", "run-now":
				ensureRequest(&o)
			}
			value, err := scheduleCommand(ctx, c, o, rest, streams)
			return emit(value, err)
		}
	case "notification":
		if len(rest) > 0 && (rest[0] == "configure" || rest[0] == "claim" || rest[0] == "report") {
			ensureRequest(&o)
		}
		value, err := notificationCommand(ctx, c, o, rest)
		return emit(value, err)
	case "inbox":
		if len(rest) > 0 && rest[0] != "snapshot" {
			if rest[0] == "mark-read" || rest[0] == "mark-unread" {
				ensureRequest(&o)
			}
			value, err := inboxCommand(ctx, c, o, rest)
			return emit(value, err)
		}
	case "interaction":
		if len(rest) > 0 && rest[0] == "approve" {
			ensureRequest(&o)
			value, err := respondApproval(ctx, c, o, rest[1:], streams)
			return emit(value, err)
		}
		if len(rest) > 0 && rest[0] == "respond" {
			ensureRequest(&o)
			value, err := respondQuestion(ctx, c, o, rest[1:], streams)
			return emit(value, err)
		}
	case "session":
		if len(rest) > 0 && rest[0] == "budget" {
			if len(rest) > 1 && rest[1] != "get" {
				ensureRequest(&o)
			}
			value, err := budgetCommand(ctx, c, o, rest[1:])
			return emit(value, err)
		}
		if len(rest) > 0 && rest[0] != "get" && rest[0] != "inspect" && rest[0] != "snapshot" {
			if rest[0] != "list" {
				ensureRequest(&o)
			}
			value, err := sessionCommand(ctx, c, o, rest, streams)
			return emit(value, err)
		}
	case "queue":
		if len(rest) > 0 && rest[0] != "get" && rest[0] != "inspect" && rest[0] != "snapshot" {
			if rest[0] != "list" {
				ensureRequest(&o)
			}
			value, err := queueCommand(ctx, c, o, rest, streams)
			return emit(value, err)
		}
	case "provider":
		presetCreate := false
		if len(rest) > 0 && rest[0] == "create" {
			for _, arg := range rest[1:] {
				name, _, _ := strings.Cut(arg, "=")
				if name == "--preset" {
					presetCreate = true
				}
			}
		}
		if len(rest) > 0 && (rest[0] == "presets" || rest[0] == "inventory" || rest[0] == "discover" || presetCreate) {
			if rest[0] != "presets" && rest[0] != "inventory" {
				ensureRequest(&o)
			}
			value, err := providerCatalog(ctx, c, o, rest)
			return emit(value, err)
		}
	case "model":
		if len(rest) > 0 && (rest[0] == "search" || rest[0] == "resolve") {
			value, err := modelCatalog(ctx, c, rest)
			return emit(value, err)
		}
	case "github":
		if len(rest) >= 2 && rest[0] == "pr" && rest[1] == "remediation" {
			value, err := prRemediationCommand(ctx, c, o, rest[2:])
			return emit(value, err)
		}
		if len(rest) >= 2 && rest[0] == "pr" && rest[1] == "problems" {
			value, err := prProblemsCommand(ctx, c, o, rest[2:])
			return emit(value, err)
		}
		value, err := githubCommand(ctx, c, rest)
		return emit(value, err)
	case "network":
		ensureRequest(&o)
		value, err := networkCommand(ctx, c, o, rest, streams)
		return emit(value, err)
	case "integration":
		if len(rest) > 0 && rest[0] != "list" && rest[0] != "get" && rest[0] != "snapshot" {
			if rest[0] != "inspect-repository" {
				ensureRequest(&o)
			}
			value, err := integrationCommand(ctx, c, o, rest, streams)
			return emit(value, err)
		}
	case "account":
		if len(rest) > 0 && (rest[0] == "connect" || rest[0] == "disconnect" || rest[0] == "status" || rest[0] == "validate") {
			if rest[0] != "status" {
				ensureRequest(&o)
			}
			value, err := accountCommand(ctx, c, o, rest, streams)
			return emit(value, err)
		}
	case "machine":
		if len(rest) > 0 && rest[0] == "discover" {
			ensureRequest(&o)
			value, err := machineDiscover(ctx, c, o, rest[1:], streams)
			return emit(value, err)
		}
	case "device":
		if len(rest) > 0 && (rest[0] == "create-pairing" || rest[0] == "revoke") {
			ensureRequest(&o)
			value, err := deviceRemote(ctx, c, o, rest)
			return emit(value, err)
		}
	case "repository":
		if len(rest) > 0 && rest[0] == "inspect" {
			ensureRequest(&o)
			value, err := repositoryInspect(ctx, c, o, rest[1:])
			return emit(value, err)
		}
	case "server":
		if len(rest) != 1 {
			return emit(nil, usage())
		}
		switch rest[0] {
		case "overview":
			response, err := c.system.GetOverview(ctx, request(c, &pb.GetOverviewRequest{}))
			if err != nil {
				return emit(nil, rpc.ClientError(err))
			}
			return emit(overviewOutput(response.Msg), nil)
		case "status":
			response, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
			if err != nil {
				return emit(nil, rpc.ClientError(err))
			}
			return emit(response.Msg, nil)
		case "stop":
			ensureRequest(&o)
			response, err := c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(o.requestID)}))
			if err != nil {
				if o.server == "" && !o.tokenStdin && (domain.SafeError(rpc.ClientError(err)).Code == domain.ServerUnavailable || domain.SafeError(rpc.ClientError(err)).Code == domain.Unavailable) {
					if suppressErr := server.SuppressLocalRestart(o.dataDir, o.requestID); suppressErr != nil {
						return emit(nil, suppressErr)
					}
					return emit(nil, offlineStop())
				}
				return emit(nil, rpc.ClientError(err))
			}
			return emit(response.Msg, nil)
		default:
			return emit(nil, usage())
		}
	case "doctor":
		if len(rest) > 0 {
			return emit(nil, usage())
		}
		response, err := c.system.GetDoctor(ctx, request(c, &pb.GetDoctorRequest{}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		return emit(json.RawMessage(response.Msg.ReportJson), nil)
	case "configuration":
		if len(rest) > 0 && rest[0] == "apply" {
			ensureRequest(&o)
		}
		value, err := configurationTransfer(ctx, c, o, rest, streams)
		return emit(value, err)
	case "backup":
		if len(rest) > 0 && (rest[0] == "create" || rest[0] == "delete") {
			ensureRequest(&o)
		}
		value, err := backupCommand(ctx, c, o, rest)
		return emit(value, err)
	case "events":
		fs := flags("events")
		cursor := fs.String("cursor", "", "snapshot event cursor")
		session := fs.String("session-id", "", "session scope")
		if err := parse(fs, rest); err != nil {
			return emit(nil, err)
		}
		if *cursor == "" {
			return emit(nil, domain.Fail(domain.MissingInput, "An event cursor is required.", "Fetch a resource snapshot before subscribing."))
		}
		stream, err := c.resources.WatchEvents(ctx, request(c, &pb.WatchEventsRequest{Cursor: *cursor, SessionId: *session}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		defer stream.Close()
		for stream.Receive() {
			if code := emit(stream.Msg(), nil); code != 0 {
				return code
			}
		}
		if err := stream.Err(); err != nil {
			if ctx.Err() != nil {
				return emit(nil, domain.SafeError(ctx.Err()))
			}
			return emit(nil, rpc.ClientError(err))
		}
		return 0
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
func ensureRequest(o *options) {
	if o.requestID == "" {
		o.requestID = domain.NewID()
	}
}
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}
func parse(f *flag.FlagSet, args []string) error {
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return usage()
	}
	return nil
}
func usage() error {
	return domain.Fail(domain.InvalidArgument, "Invalid command or arguments.", "Run `delidev help` for supported command syntax.")
}
func globals(args []string) (options, []string, error) {
	var o options
	rest := []string{}
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		switch name {
		case "--json":
			if inline && value != "true" {
				return o, nil, usage()
			}
		case "--token-stdin":
			if inline {
				return o, nil, usage()
			}
			o.tokenStdin = true
		case "--data-dir", "--server", "--request-id":
			if !inline {
				i++
				if i >= len(args) {
					return o, nil, usage()
				}
				value = args[i]
			}
			if value == "" {
				return o, nil, usage()
			}
			switch name {
			case "--data-dir":
				o.dataDir = value
			case "--server":
				o.server = value
			case "--request-id":
				o.requestID = domain.ID(value)
				if err := o.requestID.Validate(); err != nil {
					return o, nil, err
				}
			}
		default:
			rest = append(rest, args[i])
		}
	}
	return o, rest, nil
}
func connectClient(o options, input io.Reader) (client, error) {
	endpoint := o.server
	token := ""
	if saved, err := worker.LoadCredential(o.dataDir); err == nil {
		if saved.Type != domain.ClientDevice {
			return client{}, domain.Fail(domain.PermissionDenied, "A Worker scope cannot authenticate product commands.", "Use the owner or a paired client scope.")
		}
		if endpoint == "" {
			endpoint = saved.Endpoint
		}
		if endpoint == saved.Endpoint {
			token = saved.Token
		}
	} else if !os.IsNotExist(err) {
		return client{}, err
	}
	if endpoint == "" {
		saved, err := server.LoadEndpoint(o.dataDir)
		if err != nil {
			return client{}, err
		}
		if saved.ProtocolVersion != rpc.ProtocolVersion {
			return client{}, domain.Fail(domain.Unsupported, "The running server protocol is incompatible.", "Use a compatible CLI; explicitly stop or upgrade the server only after reviewing its sessions.")
		}
		identity, err := security.LoadIdentity(o.dataDir)
		if err != nil {
			return client{}, domain.Fail(domain.Unauthenticated, "The private owner credential is unavailable.", "Restore the selected server's owner credential.")
		}
		if identity.ServerID != saved.ServerID {
			return client{}, domain.Fail(domain.RecoveryRequired, "The endpoint and owner identity disagree.", "Inspect the selected data scope without overwriting it.")
		}
		endpoint = saved.URL
		token = identity.Token
	}
	if err := rpc.ValidateEndpoint(endpoint); err != nil {
		return client{}, err
	}
	if o.tokenStdin {
		if terminalInput(input) {
			return client{}, domain.Fail(domain.MissingInput, "A credential must be provided through stdin.", "Pipe the credential from protected storage; do not include it in argv.")
		}
		raw, err := io.ReadAll(io.LimitReader(input, 4097))
		if err != nil || len(raw) > 4096 {
			return client{}, domain.Fail(domain.InvalidArgument, "Invalid authentication input.", "Send only the credential through stdin.")
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" {
		return client{}, domain.Fail(domain.MissingInput, "The selected server requires a credential.", "Provide it through --token-stdin or pair this device; never put secrets in argv.")
	}
	httpClient, transport := rpc.HTTPClient()
	opts := []connect.ClientOption{connect.WithReadMaxBytes(5 << 20), connect.WithSendMaxBytes(2 << 20)}
	return client{
		transport: transport, endpoint: endpoint, token: token,
		inbox:         delidevv1connect.NewInboxServiceClient(httpClient, endpoint, opts...),
		schedules:     delidevv1connect.NewScheduleServiceClient(httpClient, endpoint, opts...),
		interactions:  delidevv1connect.NewInteractionServiceClient(httpClient, endpoint, opts...),
		sessions:      delidevv1connect.NewSessionServiceClient(httpClient, endpoint, opts...),
		accounts:      delidevv1connect.NewAccountServiceClient(httpClient, endpoint, opts...),
		integrations:  delidevv1connect.NewIntegrationServiceClient(httpClient, endpoint, opts...),
		network:       delidevv1connect.NewNetworkServiceClient(httpClient, endpoint, opts...),
		providers:     delidevv1connect.NewProviderServiceClient(httpClient, endpoint, opts...),
		devices:       delidevv1connect.NewDeviceServiceClient(httpClient, endpoint, opts...),
		workers:       delidevv1connect.NewWorkerServiceClient(httpClient, endpoint, opts...),
		system:        delidevv1connect.NewSystemServiceClient(httpClient, endpoint, opts...),
		resources:     delidevv1connect.NewResourceServiceClient(httpClient, endpoint, opts...),
		search:        delidevv1connect.NewSearchServiceClient(httpClient, endpoint, opts...),
		activity:      delidevv1connect.NewActivityServiceClient(httpClient, endpoint, opts...),
		usage:         delidevv1connect.NewUsageServiceClient(httpClient, endpoint, opts...),
		configuration: delidevv1connect.NewConfigurationServiceClient(httpClient, endpoint, opts...),
	}, nil
}
func readDocument(path string, input io.Reader) ([]byte, error) {
	reader := input
	if path == "-" && terminalInput(input) {
		return nil, domain.Fail(domain.MissingInput, "A JSON document is required.", "Pipe the document through stdin or pass --input PATH.")
	}
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, domain.Fail(domain.InvalidArgument, "The input document could not be read.", "Provide a readable JSON file or use stdin.")
		}
		defer file.Close()
		reader = file
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, domain.Fail(domain.InvalidArgument, "The input document exceeds its bound or is unreadable.", "Provide at most 1 MiB of UTF-8 JSON.")
	}
	return raw, nil
}
func resourceJSON(r *pb.Resource) any {
	if r == nil {
		return nil
	}
	kind, _ := rpc.Kind(r.Kind)
	return struct {
		ID        string          `json:"id"`
		Kind      domain.Kind     `json:"kind"`
		Revision  uint64          `json:"revision"`
		SessionID string          `json:"session_id,omitempty"`
		ProjectID string          `json:"project_id,omitempty"`
		Data      json.RawMessage `json:"data"`
		CreatedAt string          `json:"created_at"`
		UpdatedAt string          `json:"updated_at"`
	}{r.Id, kind, r.Revision, r.SessionId, r.ProjectId, json.RawMessage(r.DocumentJson), r.CreatedAt, r.UpdatedAt}
}
func resourcesJSON(records []*pb.Resource) []any {
	result := make([]any, 0, len(records))
	for _, r := range records {
		result = append(result, resourceJSON(r))
	}
	return result
}

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

const help = `DeliDev 0.1.0 (unreleased)

Usage: delidev [--data-dir PATH] [--server URL --token-stdin] COMMAND

  server start [--foreground] [--listen IP:PORT] [--tls-cert FILE --tls-key FILE]
               [--allowed-origins ORIGIN,ORIGIN]
  server status | overview | stop
  server ensure [--listen IP:PORT] [--allowed-origins ORIGIN,ORIGIN]
  doctor
  connection list
  connection worker-register|worker-inspect|worker-status|worker-start --id UUID
  connection worker-stop --id UUID --generation UUID
  connection pair --id UUID --name NAME --code-stdin
  connection inspect|verify|retry --id UUID
  device create-pairing --type worker|client --name NAME
  device pair --device-dir PATH --code-stdin
  device pair-local --device-dir PATH
  device inspect-local
  --request-id UUID device recover-local --id UUID --revision N
  device inspect --device-dir PATH
  device revoke --id ID --revision N
  worker pair --worker-dir PATH --name NAME --code-stdin
  worker pair-local --worker-dir PATH
  worker inspect --worker-dir PATH
  worker status --worker-dir PATH
  worker stop --worker-dir PATH --generation UUID-V7
  worker start --worker-dir PATH [--detach]
  repository inspect --machine-id ID --path PATH [--preferred-remote NAME] [--wait]
  machine discover --id ID --revision N [--input FILE|-] [--protocol] [--wait]
  account connect --id ID --revision N (--key-stdin | --keyless)
  account disconnect --id ID --revision N
  account validate --id ID --revision N
  account status --id ID
  account list [--provider-id ID] [--account-type api|subscription] [--limit N] [--page-token TOKEN]
  integration create --input FILE|-
  integration edit --id ID --revision N --input FILE|-
  integration replace-token --id ID --revision N --pat-stdin
  integration validate|delete --id ID --revision N
  network profile save --input FILE|- [--id ID --revision N] [--credential-stdin | --clear-credential]
  network profile list|get|delete [--id ID --revision N] [--limit N --page-token TOKEN]
  network select [--id ROUTE_ID --revision N] [--machine-id ID] [--profile-id ID --profile-revision N]
  network status [--machine-id ID]
  network export-metadata --machine-id ID --revision DESIRED_GENERATION
  integration list|get|snapshot [--id ID]
  integration token-form --id ID --revision N --access selected-repositories|public-repositories|private-repositories [--open]
  integration inspect-repository --repository-id ID
  github pr|issue list --repository-id ID [--state open|closed|all] [--page N --page-size N]
  github pr|issue search --repository-id ID --text TERMS [--state open|closed|all] [--page N]
  github pr|issue get|open --repository-id ID --number N
  github pr diff|rules|ci|feedback|reviewers --repository-id ID --number N
  github pr checks|statuses --repository-id ID --number N [--page N --page-size N]
  github pr problems refresh --repository-id ID --number N [--kind feedback|ci|conflict]
  github pr problems list --remote-repository-id N --pull-request-id N [--limit N --page-token TOKEN]
  github pr problems dismiss --id ID --revision N --content-version SHA256
  github pr remediation list --remote-repository-id N --pull-request-id N [--limit N --page-token TOKEN]
  github pr remediation resume --id SET_ID --revision N
  provider presets
  provider inventory [--query TEXT] [--enabled-only] [--limit N] [--page-token TOKEN]
  provider create --preset PRESET [--name NAME]
    --name creates an independent custom copy; --preset alone creates the managed preset
  provider discover --account-id ID --revision N
  model search [--query TEXT] [--provider-id ID] [--include-hidden] [--enabled-providers-only] [--limit N] [--page-token TOKEN]
  model resolve --selector ID|ALIAS|NATIVE_ID [--provider-id ID]
  session files roots|list|read --id ID [--repository-id ID] [--path RELATIVE] [--page-token TOKEN]
  session diff --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]
  session review-context --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]
  session review create|edit|delete|submit|list|get --id SESSION [--review-id ID] [--revision N] [--input PATH]
  session pr link --id SESSION --repository-id ID --number N
  session pr list|get|unlink --id SESSION [--association-id ID] [--revision N]
  session create --input FILE|- [--wait]
  session prepare --id ID --revision N [--wait]
  session recover-workspace --id ID --revision N [--cleanup] [--wait]
  session recover-execution --id ID --revision N --execution-id ID [--wait]
  session list [--project-id ID] [--include-archived] [--limit N] [--page-token TOKEN]
  session enqueue --id ID --input FILE|-
  session steer --id SESSION --input-id INPUT --revision N --execution-id EXECUTION --turn-id TURN
  session stop|archive|restore|resume --id ID --revision N
  session rename --id ID --revision N --name NAME
  schedule create --input FILE|- [--local-worker-dir PATH]
  schedule edit --id ID --revision N --input FILE|- [--local-worker-dir PATH]
  schedule list [--project-id ID] [--enabled all|true|false] [--limit N] [--page-token TOKEN]
  schedule get|inspect|next-run --id ID
  schedule pause|resume|delete|run-now --id ID --revision N
  schedule history --id ID [--limit N] [--page-token TOKEN]
  schedule occurrence --id SCHEDULE --occurrence-id OCCURRENCE
  interaction respond --id ID --revision N --input FILE|-
  interaction approve --id ID --revision N --input FILE|-
  session budget get --id ID
  session budget set --id ID --revision N --currency USD --threshold DECIMAL
  session budget remove --id ID --revision N
  usage pricing get --model-id ID
  usage pricing version --id ID
  usage pricing set --model-id ID --model-revision M --revision N --input PATH [--request-id ID]
  usage summary [--from RFC3339] [--until RFC3339] [--granularity day --timezone IANA] [--session-id ID] [--project-id ID | --general-chat] [--account-id ID] [--provider-id ID] [--model-id ID]
  activity list [--session-id ID] [--project-id ID] [--limit N] [--page-token TOKEN]
  search --query TEXT [--session-id ID] [--project-id ID] [--agent-id ID] [--account-id ID]
    [--outcome all|not-started|running|succeeded|failed|stopped] [--archive all|active|archiving|archived]
    [--limit N] [--page-token TOKEN]
  notification preferences | configure --revision N --interactions on|off --terminals on|off
  notification list [--limit N] | inspect|claim --id INBOX_ID
  notification report --id INBOX_ID --claim-id CLAIM_ID --state submitted|denied|failed|uncertain
  inbox list [--session-id ID] [--project-id ID] [--read-state all|read|unread]
    [--source all|interaction|execution-terminal] [--limit N] [--page-token TOKEN]
  inbox get|inspect --id ID
  inbox mark-read|mark-unread --id ID --revision N
  queue list --session-id ID [--limit N] [--page-token TOKEN]
  queue edit --session-id ID --id ID --revision N --input FILE|-
  queue remove --session-id ID --id ID --revision N
  configuration export [--output PATH]
  configuration preview --input PATH|- [--output PATH]
  configuration apply --input PATH|- [--request-id ID]
  backup create [--wait]
  backup creation --id JOB-ID
  backup creations [--limit N] [--page-token TOKEN]
  backup list [--limit N] [--page-token TOKEN]
  backup inspect --id ID
  backup delete --id ID --expected-revision REV --size-bytes BYTES --modified-at TIME --sha256 SHA256 --confirm
  backup deletion --id JOB-ID
  backup deletions [--limit N] [--page-token TOKEN]
  settings defaults
  KIND list [--limit 50] [--page-token TOKEN] [--project-id ID] [--session-id ID]
  KIND get --id ID
  KIND snapshot [--session-id ID] [--project-id ID]
  KIND create --input FILE|- [--request-id UUID-V7]
  KIND edit --id ID --revision N --input FILE|- [--request-id UUID-V7]
  KIND delete --id ID --revision N [--request-id UUID-V7]
  agent routing --id ID [--project-id ID]
  events --cursor TOKEN [--session-id ID]
  version

Configuration kinds: project, repository, agent, account, provider, model,
                     template, settings.
Product output is versioned JSON; --json is accepted explicitly.
Progress and structured diagnostics go to stderr. Ordinary commands never start
servers. Retain each mutation's request ID and reuse it only for an exact retry.
Secrets are accepted through stdin, never command arguments.
`

func terminalInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
