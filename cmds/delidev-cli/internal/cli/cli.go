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
	configuration delidevv1connect.ConfigurationServiceClient
	devices       delidevv1connect.DeviceServiceClient
	workers       delidevv1connect.WorkerServiceClient
	accounts      delidevv1connect.AccountServiceClient
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
	if o.dataDir == "" {
		o.dataDir, err = DefaultDataDir()
		if err != nil {
			return emit(nil, domain.SafeError(err))
		}
	}
	command := remaining[0]
	rest := remaining[1:]
	if command == "server" && len(rest) > 0 && (rest[0] == "start" || rest[0] == "run") {
		value, err := start(ctx, o, rest, streams)
		return emit(value, err)
	}
	if command == "settings" && len(rest) == 1 && rest[0] == "defaults" {
		return emit(domain.DefaultSettings(), nil)
	}
	if command == "worker" || (command == "device" && len(rest) > 0 && rest[0] == "pair") {
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
	c, err := connectClient(o, streams.In)
	if err != nil {
		return emit(nil, err)
	}
	defer c.transport.CloseIdleConnections()
	if command != "events" {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		ctx = bounded
	}
	switch command {
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
	case "backup":
		if len(rest) != 1 || rest[0] != "create" {
			return emit(nil, usage())
		}
		ensureRequest(&o)
		response, err := c.system.CreateBackup(ctx, request(c, &pb.CreateBackupRequest{RequestId: string(o.requestID)}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		return emit(response.Msg, nil)
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
	if err := parse(fs, rest); err != nil {
		return emit(nil, err)
	}
	switch action {
	case "list", "snapshot":
		if *limit > 200 || *limit == 0 {
			return emit(nil, domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use 1 through 200."))
		}
		f := &pb.Filter{Kind: rpc.WireKind(kind), PageSize: uint32(*limit), PageToken: *page, ProjectId: *project, SessionId: *session}
		if action == "snapshot" {
			response, err := c.resources.GetSnapshot(ctx, request(c, &pb.GetSnapshotRequest{Filter: f}))
			if err != nil {
				return emit(nil, rpc.ClientError(err))
			}
			return emit(map[string]any{"resources": resourcesJSON(response.Msg.Resources), "cursor": response.Msg.Cursor}, nil)
		}
		response, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: f}))
		if err != nil {
			return emit(nil, rpc.ClientError(err))
		}
		return emit(map[string]any{"resources": resourcesJSON(response.Msg.Resources), "next_page_token": response.Msg.NextPageToken}, nil)
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
	return client{transport: transport, endpoint: endpoint, accounts: delidevv1connect.NewAccountServiceClient(httpClient, endpoint, opts...), devices: delidevv1connect.NewDeviceServiceClient(httpClient, endpoint, opts...), workers: delidevv1connect.NewWorkerServiceClient(httpClient, endpoint, opts...), system: delidevv1connect.NewSystemServiceClient(httpClient, endpoint, opts...), resources: delidevv1connect.NewResourceServiceClient(httpClient, endpoint, opts...), configuration: delidevv1connect.NewConfigurationServiceClient(httpClient, endpoint, opts...), token: token}, nil
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
	if o.server != "" {
		return nil, domain.Fail(domain.InvalidArgument, "Server startup is a local infrastructure command.", "Run it on the server machine with its data directory.")
	}
	fs := flags("server start")
	listen := fs.String("listen", server.DefaultListen, "explicit listener")
	cert := fs.String("tls-cert", "", "TLS certificate")
	key := fs.String("tls-key", "", "TLS key")
	origins := fs.String("allowed-origins", "", "comma-separated exact origins")
	foreground := fs.Bool("foreground", false, "remain attached")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	configuration := server.Config{DataDir: o.dataDir, Listen: *listen, TLSCertificate: *cert, TLSKey: *key, Logger: slog.New(slog.NewJSONHandler(streams.Err, nil))}
	if *origins != "" {
		configuration.AllowedOrigins = strings.Split(*origins, ",")
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

const help = `DeliDev 0.1.0 (unreleased)

Usage: delidev [--data-dir PATH] [--server URL --token-stdin] COMMAND

  server start [--foreground] [--listen IP:PORT] [--tls-cert FILE --tls-key FILE]
               [--allowed-origins ORIGIN,ORIGIN]
  server status | stop
  doctor
  device create-pairing --type worker|client --name NAME
  device pair --device-dir PATH --code-stdin
  device revoke --id ID --revision N
  worker pair --worker-dir PATH --name NAME --code-stdin
  worker start --worker-dir PATH
  repository inspect --machine-id ID --path PATH [--preferred-remote NAME] [--wait]
  machine discover --id ID --revision N [--input FILE|-] [--protocol] [--wait]
  account connect --id ID --revision N (--key-stdin | --keyless)
  account disconnect --id ID --revision N
  account validate --id ID --revision N
  account status --id ID
  backup create
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
