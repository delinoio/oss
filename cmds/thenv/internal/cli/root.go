package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch args[0] {
	case string(contracts.ThenvOperationPush):
		return executePush(args[1:])
	case string(contracts.ThenvOperationPull):
		return executePull(args[1:])
	case string(contracts.ThenvOperationList):
		return executeList(args[1:])
	case string(contracts.ThenvOperationRotate):
		return executeRotate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage()
		return 2
	}
}

type commonFlags struct {
	serverURL     string
	token         string
	workspaceID   string
	projectID     string
	environmentID string
}

func registerCommonFlags(fs *flag.FlagSet, common *commonFlags) {
	fs.StringVar(&common.serverURL, "server", resolveServerURL(), "thenv server base URL")
	fs.StringVar(&common.token, "token", resolveToken(), "bearer token value used as actor subject")
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

func executePush(args []string) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

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
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if strings.TrimSpace(envFile) == "" && strings.TrimSpace(devVarsFile) == "" {
		fmt.Fprintln(os.Stderr, "push requires at least one of --env-file or --dev-vars-file")
		return 2
	}

	logger := logging.New()
	bundleClient := newBundleClient(common.serverURL)

	files := make([]*thenvv1.BundleFile, 0, 2)
	fileTypes := make([]string, 0, 2)
	if strings.TrimSpace(envFile) != "" {
		payload, err := os.ReadFile(filepath.Clean(envFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read --env-file: %v\n", err)
			return 1
		}
		files = append(files, &thenvv1.BundleFile{FileType: thenvv1.FileType_FILE_TYPE_ENV, Plaintext: payload})
		fileTypes = append(fileTypes, thenvv1.FileType_FILE_TYPE_ENV.String())
	}
	if strings.TrimSpace(devVarsFile) != "" {
		payload, err := os.ReadFile(filepath.Clean(devVarsFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read --dev-vars-file: %v\n", err)
			return 1
		}
		files = append(files, &thenvv1.BundleFile{FileType: thenvv1.FileType_FILE_TYPE_DEV_VARS, Plaintext: payload})
		fileTypes = append(fileTypes, thenvv1.FileType_FILE_TYPE_DEV_VARS.String())
	}

	req := connect.NewRequest(&thenvv1.PushBundleVersionRequest{Scope: common.scope(), Files: files})
	applyAuthHeaders(req, common.token)

	res, err := bundleClient.PushBundleVersion(context.Background(), req)
	if err != nil {
		logger.Event(map[string]any{
			"operation":  contracts.ThenvOperationPush,
			"scope":      common.scope(),
			"file_types": fileTypes,
			"result":     "failure",
			"error":      renderError(err),
		})
		fmt.Fprintf(os.Stderr, "push failed: %s\n", renderError(err))
		return 1
	}

	logger.Event(map[string]any{
		"operation":         contracts.ThenvOperationPush,
		"scope":             common.scope(),
		"file_types":        fileTypes,
		"bundle_version_id": res.Msg.GetVersion().GetBundleVersionId(),
		"result":            "success",
	})

	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"bundleVersionId": res.Msg.GetVersion().GetBundleVersionId(),
		"status":          res.Msg.GetVersion().GetStatus().String(),
		"createdAt":       res.Msg.GetVersion().GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	})
	return 0
}

func executePull(args []string) int {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

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
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	logger := logging.New()
	bundleClient := newBundleClient(common.serverURL)

	req := connect.NewRequest(&thenvv1.PullActiveBundleRequest{Scope: common.scope(), BundleVersionId: strings.TrimSpace(bundleVersionID)})
	applyAuthHeaders(req, common.token)

	res, err := bundleClient.PullActiveBundle(context.Background(), req)
	if err != nil {
		logger.Event(map[string]any{
			"operation": contracts.ThenvOperationPull,
			"scope":     common.scope(),
			"result":    "failure",
			"error":     renderError(err),
		})
		fmt.Fprintf(os.Stderr, "pull failed: %s\n", renderError(err))
		return 1
	}

	conflictPolicy := contracts.ThenvConflictPolicyFailClosed
	if force {
		conflictPolicy = contracts.ThenvConflictPolicyForceOverwrite
	}

	writtenFiles := make([]string, 0, len(res.Msg.GetFiles()))
	for _, file := range res.Msg.GetFiles() {
		outputPath, err := resolveOutputPath(file.GetFileType(), outputEnvFile, outputDevVarsFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pull failed: %v\n", err)
			return 1
		}

		conflict, err := hasConflict(outputPath, file.GetPlaintext())
		if err != nil {
			fmt.Fprintf(os.Stderr, "check output conflict: %v\n", err)
			return 1
		}
		if conflict && !force {
			fmt.Fprintf(os.Stderr, "pull conflict on %s (use --force to overwrite)\n", outputPath)
			return 1
		}

		if err := os.WriteFile(outputPath, file.GetPlaintext(), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write output file %s: %v\n", outputPath, err)
			return 1
		}
		writtenFiles = append(writtenFiles, outputPath)
	}

	logger.Event(map[string]any{
		"operation":         contracts.ThenvOperationPull,
		"scope":             common.scope(),
		"bundle_version_id": res.Msg.GetVersion().GetBundleVersionId(),
		"conflict_policy":   conflictPolicy,
		"result":            "success",
	})

	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"bundleVersionId": res.Msg.GetVersion().GetBundleVersionId(),
		"filesWritten":    writtenFiles,
	})
	return 0
}

func executeList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

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
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	logger := logging.New()
	bundleClient := newBundleClient(common.serverURL)

	req := connect.NewRequest(&thenvv1.ListBundleVersionsRequest{Scope: common.scope(), Limit: int32(limit), Cursor: strings.TrimSpace(cursor)})
	applyAuthHeaders(req, common.token)

	res, err := bundleClient.ListBundleVersions(context.Background(), req)
	if err != nil {
		logger.Event(map[string]any{
			"operation": contracts.ThenvOperationList,
			"scope":     common.scope(),
			"result":    "failure",
			"error":     renderError(err),
		})
		fmt.Fprintf(os.Stderr, "list failed: %s\n", renderError(err))
		return 1
	}

	items := make([]map[string]any, 0, len(res.Msg.GetVersions()))
	for _, version := range res.Msg.GetVersions() {
		fileTypes := make([]string, 0, len(version.GetFileTypes()))
		for _, fileType := range version.GetFileTypes() {
			fileTypes = append(fileTypes, fileType.String())
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

	logger.Event(map[string]any{
		"operation": contracts.ThenvOperationList,
		"scope":     common.scope(),
		"result":    "success",
	})

	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"versions":   items,
		"nextCursor": res.Msg.GetNextCursor(),
	})
	return 0
}

func executeRotate(args []string) int {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	var fromVersionID string
	fs.StringVar(&fromVersionID, "from-version", "", "source bundle version id for rotate")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.validate(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	logger := logging.New()
	bundleClient := newBundleClient(common.serverURL)

	req := connect.NewRequest(&thenvv1.RotateBundleVersionRequest{Scope: common.scope(), FromVersionId: strings.TrimSpace(fromVersionID)})
	applyAuthHeaders(req, common.token)

	res, err := bundleClient.RotateBundleVersion(context.Background(), req)
	if err != nil {
		logger.Event(map[string]any{
			"operation": contracts.ThenvOperationRotate,
			"scope":     common.scope(),
			"result":    "failure",
			"error":     renderError(err),
		})
		fmt.Fprintf(os.Stderr, "rotate failed: %s\n", renderError(err))
		return 1
	}

	logger.Event(map[string]any{
		"operation":                contracts.ThenvOperationRotate,
		"scope":                    common.scope(),
		"bundle_version_id":        res.Msg.GetVersion().GetBundleVersionId(),
		"target_bundle_version_id": res.Msg.GetPreviousActive().GetBundleVersionId(),
		"result":                   "success",
	})

	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"bundleVersionId":       res.Msg.GetVersion().GetBundleVersionId(),
		"status":                res.Msg.GetVersion().GetStatus().String(),
		"previousActiveVersion": res.Msg.GetPreviousActive().GetBundleVersionId(),
	})
	return 0
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

func applyAuthHeaders[T any](req *connect.Request[T], token string) {
	req.Header().Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header().Set("X-Request-Id", newRequestID("req"))
	req.Header().Set("X-Trace-Id", newRequestID("trace"))
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

func renderError(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return fmt.Sprintf("%s (%s)", connectErr.Message(), connectErr.Code().String())
	}
	return err.Error()
}

func printUsage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage:")
	_, _ = fmt.Fprintln(os.Stderr, "  thenv push --workspace <id> --project <id> --env <id> [--env-file <path>] [--dev-vars-file <path>] [--server <url>] [--token <subject>]")
	_, _ = fmt.Fprintln(os.Stderr, "  thenv pull --workspace <id> --project <id> --env <id> [--output-env-file <path>] [--output-dev-vars-file <path>] [--version <id>] [--force] [--server <url>] [--token <subject>]")
	_, _ = fmt.Fprintln(os.Stderr, "  thenv list --workspace <id> --project <id> --env <id> [--limit <n>] [--cursor <token>] [--server <url>] [--token <subject>]")
	_, _ = fmt.Fprintln(os.Stderr, "  thenv rotate --workspace <id> --project <id> --env <id> [--from-version <id>] [--server <url>] [--token <subject>]")
}
