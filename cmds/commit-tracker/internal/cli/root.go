package cli

import (
	"context"
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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/delinoio/oss/cmds/commit-tracker/internal/contracts"
	"github.com/delinoio/oss/cmds/commit-tracker/internal/logging"
	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	committrackerv1connect "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1/committrackerv1connect"
)

var (
	readFile = os.ReadFile
	nowUTC   = func() time.Time { return time.Now().UTC() }
)

type commonFlags struct {
	serverURL string
	token     string
	subject   string
}

type ingestMetricInput struct {
	MetricKey               string  `json:"metricKey"`
	DisplayName             string  `json:"displayName"`
	Unit                    string  `json:"unit"`
	ValueKind               string  `json:"valueKind"`
	Direction               string  `json:"direction"`
	WarningThresholdPercent float64 `json:"warningThresholdPercent"`
	FailThresholdPercent    float64 `json:"failThresholdPercent"`
	Value                   float64 `json:"value"`
}

type ingestInput struct {
	Provider    string              `json:"provider"`
	Repository  string              `json:"repository"`
	Branch      string              `json:"branch"`
	CommitSHA   string              `json:"commitSha"`
	RunID       string              `json:"runId"`
	Environment string              `json:"environment"`
	MeasuredAt  string              `json:"measuredAt"`
	Metrics     []ingestMetricInput `json:"metrics"`
}

func Execute(args []string) int {
	return execute(args, os.Stdout, os.Stderr)
}

func execute(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case string(contracts.CommitTrackerOperationIngest):
		return executeIngest(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func registerCommonFlags(fs *flag.FlagSet, common *commonFlags) {
	fs.StringVar(&common.serverURL, "server", resolveServerURL(), "commit-tracker server URL")
	fs.StringVar(&common.token, "token", "", "bearer token (defaults to COMMIT_TRACKER_TOKEN at runtime)")
	fs.StringVar(&common.subject, "subject", "", "subject sent in X-Commit-Tracker-Subject header (defaults to COMMIT_TRACKER_SUBJECT, then token)")
}

func (c *commonFlags) validate() error {
	if strings.TrimSpace(c.serverURL) == "" {
		return errors.New("--server is required")
	}
	if strings.TrimSpace(c.token) == "" {
		return errors.New("--token is required")
	}
	return nil
}

func executeIngest(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.CommitTrackerOperationIngest), flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	var inputPath string
	fs.StringVar(&inputPath, "input", "", "JSON payload path")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(common.token) == "" {
		common.token = resolveToken()
	}
	common.subject = resolvedSubject(common.subject, common.token)

	if err := common.validate(); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	if strings.TrimSpace(inputPath) == "" {
		fmt.Fprintln(stderr, "ingest requires --input")
		return 2
	}

	logger := logging.NewWithWriter(stderr)
	payload, err := readFile(filepath.Clean(inputPath))
	if err != nil {
		fmt.Fprintf(stderr, "read --input: %v\n", err)
		return 2
	}

	var parsed ingestInput
	if err := json.Unmarshal(payload, &parsed); err != nil {
		fmt.Fprintf(stderr, "parse input JSON: %v\n", err)
		return 2
	}

	requestMessage, err := parsed.toUpsertCommitMetricsRequest()
	if err != nil {
		fmt.Fprintf(stderr, "invalid input payload: %v\n", err)
		return 2
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	client := committrackerv1connect.NewMetricIngestionServiceClient(httpClient, normalizeServerURL(common.serverURL))

	request := connect.NewRequest(requestMessage)
	applyAuthHeaders(request, common.token, common.subject)

	response, err := client.UpsertCommitMetrics(context.Background(), request)
	if err != nil {
		logger.Event(map[string]any{
			"operation":  contracts.CommitTrackerOperationIngest,
			"result":     "failure",
			"provider":   requestMessage.GetProvider().String(),
			"repository": requestMessage.GetRepository(),
			"commit":     requestMessage.GetCommitSha(),
			"run_id":     requestMessage.GetRunId(),
			"error":      err.Error(),
		})
		fmt.Fprintf(stderr, "ingest failed: %s\n", renderError(err))
		return 1
	}

	logger.Event(map[string]any{
		"operation":    contracts.CommitTrackerOperationIngest,
		"result":       "success",
		"provider":     requestMessage.GetProvider().String(),
		"repository":   requestMessage.GetRepository(),
		"commit":       requestMessage.GetCommitSha(),
		"run_id":       requestMessage.GetRunId(),
		"metric_count": len(requestMessage.GetMetrics()),
	})

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"provider":      requestMessage.GetProvider().String(),
		"repository":    requestMessage.GetRepository(),
		"commitSha":     requestMessage.GetCommitSha(),
		"runId":         requestMessage.GetRunId(),
		"upsertedCount": response.Msg.GetUpsertedCount(),
	})

	return 0
}

func resolveServerURL() string {
	configured := strings.TrimSpace(os.Getenv("COMMIT_TRACKER_SERVER_URL"))
	if configured == "" {
		return "http://127.0.0.1:8091"
	}
	return normalizeServerURL(configured)
}

func resolveToken() string {
	return strings.TrimSpace(os.Getenv("COMMIT_TRACKER_TOKEN"))
}

func resolveSubject(token string) string {
	configured := strings.TrimSpace(os.Getenv("COMMIT_TRACKER_SUBJECT"))
	if configured != "" {
		return configured
	}
	return strings.TrimSpace(token)
}

func resolvedSubject(subject string, token string) string {
	if strings.TrimSpace(subject) != "" {
		return strings.TrimSpace(subject)
	}
	return resolveSubject(token)
}

func normalizeServerURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}

func applyAuthHeaders[T any](request *connect.Request[T], token string, subject string) {
	request.Header().Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header().Set("X-Commit-Tracker-Subject", strings.TrimSpace(subject))
	request.Header().Set("X-Request-Id", fmt.Sprintf("commit-tracker-cli-%d", nowUTC().UnixNano()))
	request.Header().Set("X-Trace-Id", fmt.Sprintf("commit-tracker-trace-%d", nowUTC().UnixNano()))
}

func parseProviderKind(raw string) (committrackerv1.GitProviderKind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "github", "git_provider_kind_github":
		return committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITHUB, nil
	case "gitlab", "git_provider_kind_gitlab":
		return committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_GITLAB, nil
	case "bitbucket", "git_provider_kind_bitbucket":
		return committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_BITBUCKET, nil
	default:
		return committrackerv1.GitProviderKind_GIT_PROVIDER_KIND_UNSPECIFIED, fmt.Errorf("unsupported provider: %s", raw)
	}
}

func parseMetricValueKind(raw string) (committrackerv1.MetricValueKind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "unit-number", "metric_value_kind_unit_number":
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNIT_NUMBER, nil
	case "ratio", "metric_value_kind_ratio":
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_RATIO, nil
	case "delta-only", "metric_value_kind_delta_only":
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_DELTA_ONLY, nil
	case "boolean-gate", "metric_value_kind_boolean_gate":
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_BOOLEAN_GATE, nil
	case "histogram", "metric_value_kind_histogram":
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_HISTOGRAM, nil
	case "percentiles", "metric_value_kind_percentiles":
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_PERCENTILES, nil
	default:
		return committrackerv1.MetricValueKind_METRIC_VALUE_KIND_UNSPECIFIED, fmt.Errorf("unsupported value kind: %s", raw)
	}
}

func parseMetricDirection(raw string) (committrackerv1.MetricDirection, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "increase-is-better", "metric_direction_increase_is_better":
		return committrackerv1.MetricDirection_METRIC_DIRECTION_INCREASE_IS_BETTER, nil
	case "decrease-is-better", "metric_direction_decrease_is_better":
		return committrackerv1.MetricDirection_METRIC_DIRECTION_DECREASE_IS_BETTER, nil
	default:
		return committrackerv1.MetricDirection_METRIC_DIRECTION_UNSPECIFIED, fmt.Errorf("unsupported direction: %s", raw)
	}
}

func (input ingestInput) toUpsertCommitMetricsRequest() (*committrackerv1.UpsertCommitMetricsRequest, error) {
	provider, err := parseProviderKind(input.Provider)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Repository) == "" {
		return nil, errors.New("repository is required")
	}
	if strings.TrimSpace(input.Branch) == "" {
		return nil, errors.New("branch is required")
	}
	if strings.TrimSpace(input.CommitSHA) == "" {
		return nil, errors.New("commitSha is required")
	}
	if strings.TrimSpace(input.RunID) == "" {
		return nil, errors.New("runId is required")
	}
	if strings.TrimSpace(input.Environment) == "" {
		return nil, errors.New("environment is required")
	}
	if len(input.Metrics) == 0 {
		return nil, errors.New("metrics are required")
	}

	metrics := make([]*committrackerv1.MetricDatum, 0, len(input.Metrics))
	for _, metric := range input.Metrics {
		if strings.TrimSpace(metric.MetricKey) == "" {
			return nil, errors.New("metricKey is required")
		}
		if strings.TrimSpace(metric.DisplayName) == "" {
			return nil, fmt.Errorf("displayName is required for metric %s", metric.MetricKey)
		}
		if strings.TrimSpace(metric.Unit) == "" {
			return nil, fmt.Errorf("unit is required for metric %s", metric.MetricKey)
		}

		valueKind, err := parseMetricValueKind(metric.ValueKind)
		if err != nil {
			return nil, fmt.Errorf("metric %s: %w", metric.MetricKey, err)
		}
		direction, err := parseMetricDirection(metric.Direction)
		if err != nil {
			return nil, fmt.Errorf("metric %s: %w", metric.MetricKey, err)
		}
		metrics = append(metrics, &committrackerv1.MetricDatum{
			MetricKey:               strings.TrimSpace(metric.MetricKey),
			DisplayName:             strings.TrimSpace(metric.DisplayName),
			Unit:                    strings.TrimSpace(metric.Unit),
			ValueKind:               valueKind,
			Direction:               direction,
			WarningThresholdPercent: metric.WarningThresholdPercent,
			FailThresholdPercent:    metric.FailThresholdPercent,
			Value:                   metric.Value,
		})
	}

	request := &committrackerv1.UpsertCommitMetricsRequest{
		Provider:    provider,
		Repository:  strings.TrimSpace(input.Repository),
		Branch:      strings.TrimSpace(input.Branch),
		CommitSha:   strings.TrimSpace(input.CommitSHA),
		RunId:       strings.TrimSpace(input.RunID),
		Environment: strings.TrimSpace(input.Environment),
		Metrics:     metrics,
	}

	if strings.TrimSpace(input.MeasuredAt) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.MeasuredAt))
		if err != nil {
			return nil, fmt.Errorf("measuredAt must be RFC3339/RFC3339Nano: %w", err)
		}
		request.MeasuredAt = timestamppb.New(parsed.UTC())
	}

	return request, nil
}

func renderError(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return fmt.Sprintf("%s: %s", connectErr.Code(), connectErr.Message())
	}
	return err.Error()
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: commit-tracker <command> [options]")
	fmt.Fprintln(stderr, "commands:")
	fmt.Fprintln(stderr, "  ingest --input <path> --server <url> --token <token>")
}
