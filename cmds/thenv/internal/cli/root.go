package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/thenv/internal/contracts"
	"github.com/delinoio/oss/cmds/thenv/internal/logging"
	thenvv1 "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1"
	thenvv1connect "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1/thenvv1connect"
)

func Execute(args []string) int {
	return execute(args, os.Stdout, os.Stderr)
}

func execute(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case string(contracts.ThenvOperationPush):
		return executePush(args[1:], stdout, stderr)
	case string(contracts.ThenvOperationPull):
		return executePull(args[1:], stdout, stderr)
	case string(contracts.ThenvOperationList):
		return executeList(args[1:], stdout, stderr)
	case string(contracts.ThenvOperationRotate):
		return executeRotate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

type commonFlags struct {
	serverURL     string
	token         string
	subject       string
	workspaceID   string
	projectID     string
	environmentID string
}

type requestCorrelation struct {
	requestID string
	traceID   string
}

type cliOperationLogContext struct {
	operation          contracts.ThenvOperation
	eventType          thenvv1.AuditEventType
	actor              string
	authIdentitySource contracts.ThenvAuthIdentitySource
	scope              map[string]string
	requestID          string
	traceID            string
}

type cliOperationLogEvent struct {
	roleDecision          contracts.RoleDecision
	result                contracts.OperationResult
	bundleVersionID       string
	targetBundleVersionID string
	fileTypes             []string
	conflictPolicy        contracts.ThenvConflictPolicy
	err                   error
}

func registerCommonFlags(fs *flag.FlagSet, common *commonFlags) {
	fs.StringVar(&common.serverURL, "server", resolveServerURL(), "thenv server base URL")
	fs.StringVar(&common.token, "token", resolveToken(), "bearer token value")
	fs.StringVar(&common.subject, "subject", resolveSubject(), "subject identity sent in X-Thenv-Subject header (must match token; defaults to token)")
	fs.StringVar(&common.workspaceID, "workspace", "", "workspace scope id")
	fs.StringVar(&common.projectID, "project", "", "project scope id")
	fs.StringVar(&common.environmentID, "env", "", "environment scope id")
}

func (f commonFlags) validate() error {
	if strings.TrimSpace(f.serverURL) == "" {
		return errors.New("--server is required")
	}
	if strings.TrimSpace(f.token) == "" {
		return errors.New("--token is required")
	}
	if strings.TrimSpace(f.workspaceID) == "" || strings.TrimSpace(f.projectID) == "" || strings.TrimSpace(f.environmentID) == "" {
		return errors.New("--workspace, --project, and --env are required")
	}
	return nil
}

func (f commonFlags) scope() *thenvv1.Scope {
	return &thenvv1.Scope{
		WorkspaceId:   strings.TrimSpace(f.workspaceID),
		ProjectId:     strings.TrimSpace(f.projectID),
		EnvironmentId: strings.TrimSpace(f.environmentID),
	}
}

func (f commonFlags) resolvedSubject() string {
	if subject := strings.TrimSpace(f.subject); subject != "" {
		return subject
	}
	return strings.TrimSpace(f.token)
}

func executePush(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	var envFile string
	var devVarsFile string
	fs.StringVar(&envFile, "env-file", "", "path to .env input file")
	fs.StringVar(&devVarsFile, "dev-vars-file", "", "path to .dev.vars input file")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.validate(); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	if strings.TrimSpace(envFile) == "" && strings.TrimSpace(devVarsFile) == "" {
		fmt.Fprintln(stderr, "push requires at least one of --env-file or --dev-vars-file")
		return 2
	}

	logger := logging.NewWithWriter(stderr)
	bundleClient := newBundleClient(common.serverURL)
	scope := common.scope()
	subject := common.resolvedSubject()

	files := make([]*thenvv1.BundleFile, 0, 2)
	fileTypes := make([]string, 0, 2)
	if strings.TrimSpace(envFile) != "" {
		payload, err := os.ReadFile(filepath.Clean(envFile))
		if err != nil {
			logContext := newCLIOperationLogContext(contracts.ThenvOperationPush, common.token, subject, scope, requestCorrelation{
				requestID: newRequestID("req"),
				traceID:   newRequestID("trace"),
			})
			emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
				roleDecision: contracts.RoleDecisionAllow,
				result:       contracts.OperationResultFailure,
				err:          err,
			})
			fmt.Fprintf(stderr, "read --env-file: %v\n", err)
			return 1
		}
		files = append(files, &thenvv1.BundleFile{FileType: thenvv1.FileType_FILE_TYPE_ENV, Plaintext: payload})
		fileTypes = append(fileTypes, thenvv1.FileType_FILE_TYPE_ENV.String())
	}
	if strings.TrimSpace(devVarsFile) != "" {
		payload, err := os.ReadFile(filepath.Clean(devVarsFile))
		if err != nil {
			logContext := newCLIOperationLogContext(contracts.ThenvOperationPush, common.token, subject, scope, requestCorrelation{
				requestID: newRequestID("req"),
				traceID:   newRequestID("trace"),
			})
			emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
				roleDecision: contracts.RoleDecisionAllow,
				result:       contracts.OperationResultFailure,
				fileTypes:    fileTypes,
				err:          err,
			})
			fmt.Fprintf(stderr, "read --dev-vars-file: %v\n", err)
			return 1
		}
		files = append(files, &thenvv1.BundleFile{FileType: thenvv1.FileType_FILE_TYPE_DEV_VARS, Plaintext: payload})
		fileTypes = append(fileTypes, thenvv1.FileType_FILE_TYPE_DEV_VARS.String())
	}

	req := connect.NewRequest(&thenvv1.PushBundleVersionRequest{Scope: scope, Files: files})
	correlation := applyAuthHeaders(req, common.token, subject)
	logContext := newCLIOperationLogContext(contracts.ThenvOperationPush, common.token, subject, scope, correlation)

	res, err := bundleClient.PushBundleVersion(context.Background(), req)
	if err != nil {
		roleDecision, result := roleDecisionAndResultFromError(err)
		emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
			roleDecision: roleDecision,
			result:       result,
			fileTypes:    fileTypes,
			err:          err,
		})
		fmt.Fprintf(stderr, "push failed: %s\n", renderError(err))
		return 1
	}

	emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
		roleDecision:    contracts.RoleDecisionAllow,
		result:          contracts.OperationResultSuccess,
		bundleVersionID: res.Msg.GetVersion().GetBundleVersionId(),
		fileTypes:       fileTypes,
	})

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"bundleVersionId": res.Msg.GetVersion().GetBundleVersionId(),
		"status":          res.Msg.GetVersion().GetStatus().String(),
		"createdAt":       res.Msg.GetVersion().GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	})
	return 0
}

func executePull(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	var outputEnvFile string
	var outputDevVarsFile string
	var force bool
	var bundleVersionID string

	fs.StringVar(&outputEnvFile, "output-env-file", ".env", "output path for ENV payload")
	fs.StringVar(&outputDevVarsFile, "output-dev-vars-file", ".dev.vars", "output path for DEV_VARS payload")
	fs.BoolVar(&force, "force", false, "force overwrite on conflict")
	fs.StringVar(&bundleVersionID, "version", "", "explicit bundle version id to pull")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.validate(); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}

	logger := logging.NewWithWriter(stderr)
	bundleClient := newBundleClient(common.serverURL)
	scope := common.scope()
	subject := common.resolvedSubject()

	conflictPolicy := contracts.ThenvConflictPolicyFailClosed
	if force {
		conflictPolicy = contracts.ThenvConflictPolicyForceOverwrite
	}

	req := connect.NewRequest(&thenvv1.PullActiveBundleRequest{Scope: scope, BundleVersionId: strings.TrimSpace(bundleVersionID)})
	correlation := applyAuthHeaders(req, common.token, subject)
	logContext := newCLIOperationLogContext(contracts.ThenvOperationPull, common.token, subject, scope, correlation)

	res, err := bundleClient.PullActiveBundle(context.Background(), req)
	if err != nil {
		roleDecision, result := roleDecisionAndResultFromError(err)
		emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
			roleDecision:   roleDecision,
			result:         result,
			conflictPolicy: conflictPolicy,
			err:            err,
		})
		fmt.Fprintf(stderr, "pull failed: %s\n", renderError(err))
		return 1
	}

	fileTypes := stringifyFileTypes(collectBundleFileTypes(res.Msg.GetFiles()))
	writtenFiles := make([]string, 0, len(res.Msg.GetFiles()))
	for _, file := range res.Msg.GetFiles() {
		outputPath, err := resolveOutputPath(file.GetFileType(), outputEnvFile, outputDevVarsFile)
		if err != nil {
			emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
				roleDecision:    contracts.RoleDecisionAllow,
				result:          contracts.OperationResultFailure,
				bundleVersionID: res.Msg.GetVersion().GetBundleVersionId(),
				fileTypes:       fileTypes,
				conflictPolicy:  conflictPolicy,
				err:             err,
			})
			fmt.Fprintf(stderr, "pull failed: %v\n", err)
			return 1
		}

		conflict, err := hasConflict(outputPath, file.GetPlaintext())
		if err != nil {
			emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
				roleDecision:    contracts.RoleDecisionAllow,
				result:          contracts.OperationResultFailure,
				bundleVersionID: res.Msg.GetVersion().GetBundleVersionId(),
				fileTypes:       fileTypes,
				conflictPolicy:  conflictPolicy,
				err:             err,
			})
			fmt.Fprintf(stderr, "check output conflict: %v\n", err)
			return 1
		}
		if conflict && !force {
			err = fmt.Errorf("pull conflict on %s", outputPath)
			emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
				roleDecision:    contracts.RoleDecisionAllow,
				result:          contracts.OperationResultFailure,
				bundleVersionID: res.Msg.GetVersion().GetBundleVersionId(),
				fileTypes:       fileTypes,
				conflictPolicy:  conflictPolicy,
				err:             err,
			})
			fmt.Fprintf(stderr, "pull conflict on %s (use --force to overwrite)\n", outputPath)
			return 1
		}

		if err := os.WriteFile(outputPath, file.GetPlaintext(), 0o600); err != nil {
			emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
				roleDecision:    contracts.RoleDecisionAllow,
				result:          contracts.OperationResultFailure,
				bundleVersionID: res.Msg.GetVersion().GetBundleVersionId(),
				fileTypes:       fileTypes,
				conflictPolicy:  conflictPolicy,
				err:             err,
			})
			fmt.Fprintf(stderr, "write output file %s: %v\n", outputPath, err)
			return 1
		}
		writtenFiles = append(writtenFiles, outputPath)
	}

	emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
		roleDecision:    contracts.RoleDecisionAllow,
		result:          contracts.OperationResultSuccess,
		bundleVersionID: res.Msg.GetVersion().GetBundleVersionId(),
		fileTypes:       fileTypes,
		conflictPolicy:  conflictPolicy,
	})

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"bundleVersionId": res.Msg.GetVersion().GetBundleVersionId(),
		"filesWritten":    writtenFiles,
	})
	return 0
}

func executeList(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	var limit int
	var cursor string
	fs.IntVar(&limit, "limit", 20, "page size")
	fs.StringVar(&cursor, "cursor", "", "opaque pagination cursor")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.validate(); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}

	logger := logging.NewWithWriter(stderr)
	bundleClient := newBundleClient(common.serverURL)
	scope := common.scope()
	subject := common.resolvedSubject()

	req := connect.NewRequest(&thenvv1.ListBundleVersionsRequest{Scope: scope, Limit: int32(limit), Cursor: strings.TrimSpace(cursor)})
	correlation := applyAuthHeaders(req, common.token, subject)
	logContext := newCLIOperationLogContext(contracts.ThenvOperationList, common.token, subject, scope, correlation)

	res, err := bundleClient.ListBundleVersions(context.Background(), req)
	if err != nil {
		roleDecision, result := roleDecisionAndResultFromError(err)
		emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
			roleDecision: roleDecision,
			result:       result,
			err:          err,
		})
		fmt.Fprintf(stderr, "list failed: %s\n", renderError(err))
		return 1
	}

	items := make([]map[string]any, 0, len(res.Msg.GetVersions()))
	allFileTypes := make([]thenvv1.FileType, 0, len(res.Msg.GetVersions())*2)
	for _, version := range res.Msg.GetVersions() {
		fileTypes := make([]string, 0, len(version.GetFileTypes()))
		for _, fileType := range version.GetFileTypes() {
			fileTypes = append(fileTypes, fileType.String())
			allFileTypes = append(allFileTypes, fileType)
		}
		items = append(items, map[string]any{
			"bundleVersionId": version.GetBundleVersionId(),
			"status":          version.GetStatus().String(),
			"createdBy":       version.GetCreatedBy(),
			"createdAt":       version.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
			"fileTypes":       fileTypes,
			"sourceVersionId": version.GetSourceVersionId(),
		})
	}

	emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
		roleDecision: contracts.RoleDecisionAllow,
		result:       contracts.OperationResultSuccess,
		fileTypes:    stringifyFileTypes(deduplicateFileTypes(allFileTypes)),
	})

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"versions":   items,
		"nextCursor": res.Msg.GetNextCursor(),
	})
	return 0
}

func executeRotate(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	var fromVersionID string
	fs.StringVar(&fromVersionID, "from-version", "", "source bundle version id for rotate")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.validate(); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}

	logger := logging.NewWithWriter(stderr)
	bundleClient := newBundleClient(common.serverURL)
	scope := common.scope()
	subject := common.resolvedSubject()

	req := connect.NewRequest(&thenvv1.RotateBundleVersionRequest{Scope: scope, FromVersionId: strings.TrimSpace(fromVersionID)})
	correlation := applyAuthHeaders(req, common.token, subject)
	logContext := newCLIOperationLogContext(contracts.ThenvOperationRotate, common.token, subject, scope, correlation)

	res, err := bundleClient.RotateBundleVersion(context.Background(), req)
	if err != nil {
		roleDecision, result := roleDecisionAndResultFromError(err)
		emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
			roleDecision: roleDecision,
			result:       result,
			err:          err,
		})
		fmt.Fprintf(stderr, "rotate failed: %s\n", renderError(err))
		return 1
	}

	emitCLIOperationLog(logger, logContext, cliOperationLogEvent{
		roleDecision:          contracts.RoleDecisionAllow,
		result:                contracts.OperationResultSuccess,
		bundleVersionID:       res.Msg.GetVersion().GetBundleVersionId(),
		targetBundleVersionID: res.Msg.GetPreviousActive().GetBundleVersionId(),
		fileTypes:             stringifyFileTypes(res.Msg.GetVersion().GetFileTypes()),
	})

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"bundleVersionId":       res.Msg.GetVersion().GetBundleVersionId(),
		"status":                res.Msg.GetVersion().GetStatus().String(),
		"previousActiveVersion": res.Msg.GetPreviousActive().GetBundleVersionId(),
	})
	return 0
}

func newCLIOperationLogContext(
	operation contracts.ThenvOperation,
	token string,
	subject string,
	scope *thenvv1.Scope,
	correlation requestCorrelation,
) cliOperationLogContext {
	actor, authIdentitySource := resolveActorAndAuthIdentitySource(token, subject)
	return cliOperationLogContext{
		operation:          operation,
		eventType:          operationAuditEventType(operation),
		actor:              actor,
		authIdentitySource: authIdentitySource,
		scope: map[string]string{
			"workspaceId":   scope.GetWorkspaceId(),
			"projectId":     scope.GetProjectId(),
			"environmentId": scope.GetEnvironmentId(),
		},
		requestID: correlation.requestID,
		traceID:   correlation.traceID,
	}
}

func emitCLIOperationLog(logger *logging.Logger, context cliOperationLogContext, event cliOperationLogEvent) {
	if logger == nil {
		return
	}

	fields := map[string]any{
		"operation":            context.operation,
		"event_type":           context.eventType.String(),
		"actor":                context.actor,
		"auth_identity_source": context.authIdentitySource,
		"scope":                context.scope,
		"role_decision":        event.roleDecision,
		"file_types":           []string{},
		"result":               event.result,
		"request_id":           context.requestID,
		"trace_id":             context.traceID,
	}
	if event.bundleVersionID != "" {
		fields["bundle_version_id"] = event.bundleVersionID
	}
	if event.targetBundleVersionID != "" {
		fields["target_bundle_version_id"] = event.targetBundleVersionID
	}
	if event.fileTypes != nil {
		fields["file_types"] = event.fileTypes
	}
	if event.conflictPolicy != "" {
		fields["conflict_policy"] = event.conflictPolicy
	}
	if event.err != nil {
		fields["error"] = renderError(event.err)
	}

	logger.Event(fields)
}

func operationAuditEventType(operation contracts.ThenvOperation) thenvv1.AuditEventType {
	switch operation {
	case contracts.ThenvOperationPush:
		return thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH
	case contracts.ThenvOperationPull:
		return thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL
	case contracts.ThenvOperationList:
		return thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST
	case contracts.ThenvOperationRotate:
		return thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE
	default:
		return thenvv1.AuditEventType_AUDIT_EVENT_TYPE_UNSPECIFIED
	}
}

func roleDecisionAndResultFromError(err error) (contracts.RoleDecision, contracts.OperationResult) {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) && connectErr.Code() == connect.CodePermissionDenied {
		return contracts.RoleDecisionDeny, contracts.OperationResultDenied
	}
	return contracts.RoleDecisionAllow, contracts.OperationResultFailure
}

func resolveActorAndAuthIdentitySource(token string, subject string) (string, contracts.ThenvAuthIdentitySource) {
	trimmedToken := strings.TrimSpace(token)
	trimmedSubject := strings.TrimSpace(subject)
	if trimmedSubject == "" {
		return "", contracts.ThenvAuthIdentitySourceUnspecified
	}
	if trimmedToken != "" && trimmedSubject == trimmedToken {
		return hashLegacyTokenActor(trimmedSubject), contracts.ThenvAuthIdentitySourceHashedLegacy
	}
	return trimmedSubject, contracts.ThenvAuthIdentitySourceHeader
}

func hashLegacyTokenActor(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "token_sha256:" + hex.EncodeToString(sum[:8])
}

func collectBundleFileTypes(files []*thenvv1.BundleFile) []thenvv1.FileType {
	fileTypes := make([]thenvv1.FileType, 0, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		fileTypes = append(fileTypes, file.GetFileType())
	}
	return fileTypes
}

func deduplicateFileTypes(fileTypes []thenvv1.FileType) []thenvv1.FileType {
	if len(fileTypes) == 0 {
		return nil
	}
	seen := make(map[thenvv1.FileType]struct{}, len(fileTypes))
	result := make([]thenvv1.FileType, 0, len(fileTypes))
	for _, fileType := range fileTypes {
		if _, ok := seen[fileType]; ok {
			continue
		}
		seen[fileType] = struct{}{}
		result = append(result, fileType)
	}
	return result
}

func stringifyFileTypes(fileTypes []thenvv1.FileType) []string {
	if len(fileTypes) == 0 {
		return nil
	}
	result := make([]string, 0, len(fileTypes))
	for _, fileType := range fileTypes {
		result = append(result, fileType.String())
	}
	return result
}

func newBundleClient(serverURL string) thenvv1connect.BundleServiceClient {
	normalized := strings.TrimSpace(serverURL)
	if !strings.Contains(normalized, "://") {
		normalized = "http://" + normalized
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	return thenvv1connect.NewBundleServiceClient(httpClient, normalized)
}

func resolveOutputPath(fileType thenvv1.FileType, outputEnvFile string, outputDevVarsFile string) (string, error) {
	switch fileType {
	case thenvv1.FileType_FILE_TYPE_ENV:
		return filepath.Clean(outputEnvFile), nil
	case thenvv1.FileType_FILE_TYPE_DEV_VARS:
		return filepath.Clean(outputDevVarsFile), nil
	default:
		return "", fmt.Errorf("unsupported file type: %s", fileType.String())
	}
}

func hasConflict(path string, content []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if bytes.Equal(existing, content) {
		return false, nil
	}
	return true, nil
}

func applyAuthHeaders[T any](req *connect.Request[T], token string, subject string) requestCorrelation {
	requestID := newRequestID("req")
	traceID := newRequestID("trace")

	req.Header().Set("Authorization", "Bearer "+strings.TrimSpace(token))
	trimmedSubject := strings.TrimSpace(subject)
	if trimmedSubject != "" {
		req.Header().Set("X-Thenv-Subject", trimmedSubject)
	}
	req.Header().Set("X-Request-Id", requestID)
	req.Header().Set("X-Trace-Id", traceID)

	return requestCorrelation{requestID: requestID, traceID: traceID}
}

func newRequestID(prefix string) string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(raw))
}

func resolveServerURL() string {
	if fromEnv := strings.TrimSpace(os.Getenv("THENV_SERVER_URL")); fromEnv != "" {
		return fromEnv
	}
	return "http://127.0.0.1:8087"
}

func resolveToken() string {
	if fromEnv := strings.TrimSpace(os.Getenv("THENV_TOKEN")); fromEnv != "" {
		return fromEnv
	}
	return "admin"
}

func resolveSubject() string {
	return strings.TrimSpace(os.Getenv("THENV_SUBJECT"))
}

func renderError(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return fmt.Sprintf("%s (%s)", connectErr.Message(), connectErr.Code().String())
	}
	return err.Error()
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage:")
	_, _ = fmt.Fprintln(stderr, "  thenv push --workspace <id> --project <id> --env <id> [--env-file <path>] [--dev-vars-file <path>] [--server <url>] [--token <token>] [--subject <subject>]")
	_, _ = fmt.Fprintln(stderr, "  thenv pull --workspace <id> --project <id> --env <id> [--output-env-file <path>] [--output-dev-vars-file <path>] [--version <id>] [--force] [--server <url>] [--token <token>] [--subject <subject>]")
	_, _ = fmt.Fprintln(stderr, "  thenv list --workspace <id> --project <id> --env <id> [--limit <n>] [--cursor <token>] [--server <url>] [--token <token>] [--subject <subject>]")
	_, _ = fmt.Fprintln(stderr, "  thenv rotate --workspace <id> --project <id> --env <id> [--from-version <id>] [--server <url>] [--token <token>] [--subject <subject>]")
}
