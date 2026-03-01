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
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/commit-tracker/internal/contracts"
	"github.com/delinoio/oss/cmds/commit-tracker/internal/logging"
	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	committrackerv1connect "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1/committrackerv1connect"
)

type failOnPolicy string

const (
	failOnPolicyNever failOnPolicy = "never"
	failOnPolicyWarn  failOnPolicy = "warn"
	failOnPolicyFail  failOnPolicy = "fail"
)

type metricKeyFlag []string

func (f *metricKeyFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *metricKeyFlag) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("--metric-key cannot be empty")
	}
	*f = append(*f, trimmed)
	return nil
}

type reportFlags struct {
	provider     string
	repository   string
	pullRequest  int64
	baseCommit   string
	headCommit   string
	environment  string
	metricKeys   metricKeyFlag
	failOn       string
	githubOutput string
}

func executeReport(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.CommitTrackerOperationReport), flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := commonFlags{}
	registerCommonFlags(fs, &common)

	report := reportFlags{}
	report.register(fs)

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

	policy, err := parseFailOnPolicy(report.failOn)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}

	requestMessage, err := report.toPublishPullRequestReportRequest()
	if err != nil {
		fmt.Fprintf(stderr, "invalid report input: %v\n", err)
		return 2
	}

	logger := logging.NewWithWriter(stderr)
	httpClient := &http.Client{Timeout: 15 * time.Second}
	client := committrackerv1connect.NewProviderReportServiceClient(httpClient, normalizeServerURL(common.serverURL))

	request := connect.NewRequest(requestMessage)
	applyAuthHeaders(request, common.token, common.subject)

	response, err := client.PublishPullRequestReport(context.Background(), request)
	if err != nil {
		logger.Event(map[string]any{
			"operation":        contracts.CommitTrackerOperationReport,
			"result":           "failure",
			"provider":         requestMessage.GetProvider().String(),
			"repository":       requestMessage.GetRepository(),
			"pull_request":     requestMessage.GetPullRequest(),
			"commit":           requestMessage.GetHeadCommitSha(),
			"evaluation_level": "",
			"error":            err.Error(),
		})
		fmt.Fprintf(stderr, "report failed: %s\n", renderError(err))
		return 1
	}

	aggregateEvaluation := response.Msg.GetAggregateEvaluation().String()
	logger.Event(map[string]any{
		"operation":        contracts.CommitTrackerOperationReport,
		"result":           "success",
		"provider":         requestMessage.GetProvider().String(),
		"repository":       requestMessage.GetRepository(),
		"pull_request":     requestMessage.GetPullRequest(),
		"commit":           requestMessage.GetHeadCommitSha(),
		"evaluation_level": aggregateEvaluation,
	})

	if err := json.NewEncoder(stdout).Encode(map[string]any{
		"provider":            requestMessage.GetProvider().String(),
		"repository":          requestMessage.GetRepository(),
		"pullRequest":         requestMessage.GetPullRequest(),
		"baseCommitSha":       requestMessage.GetBaseCommitSha(),
		"headCommitSha":       requestMessage.GetHeadCommitSha(),
		"aggregateEvaluation": aggregateEvaluation,
		"commentUrl":          response.Msg.GetCommentUrl(),
		"statusUrl":           response.Msg.GetStatusUrl(),
	}); err != nil {
		fmt.Fprintf(stderr, "write report output: %v\n", err)
		return 1
	}

	if err := appendGitHubOutput(resolveGitHubOutputPath(report.githubOutput), []githubOutputEntry{
		{Key: "aggregate_evaluation", Value: aggregateEvaluation},
		{Key: "comment_url", Value: response.Msg.GetCommentUrl()},
		{Key: "status_url", Value: response.Msg.GetStatusUrl()},
		{Key: "pull_request", Value: fmt.Sprintf("%d", requestMessage.GetPullRequest())},
		{Key: "base_commit_sha", Value: requestMessage.GetBaseCommitSha()},
		{Key: "head_commit_sha", Value: requestMessage.GetHeadCommitSha()},
	}); err != nil {
		fmt.Fprintf(stderr, "write github output: %v\n", err)
		return 1
	}

	if policy.shouldFail(response.Msg.GetAggregateEvaluation()) {
		fmt.Fprintf(stderr, "report evaluation %s triggered failure threshold fail-on=%s\n", aggregateEvaluation, policy)
		return 1
	}

	return 0
}

func (f *reportFlags) register(fs *flag.FlagSet) {
	if f == nil {
		return
	}
	f.provider = "github"
	f.environment = "ci"
	f.failOn = string(failOnPolicyFail)

	fs.StringVar(&f.provider, "provider", f.provider, "git provider (default: github)")
	fs.StringVar(&f.repository, "repository", "", "repository owner/name (defaults to GITHUB_REPOSITORY)")
	fs.Int64Var(&f.pullRequest, "pull-request", 0, "pull request number (defaults to pull_request.number from GITHUB_EVENT_PATH)")
	fs.StringVar(&f.baseCommit, "base-commit", "", "base commit SHA (defaults to pull_request.base.sha from GITHUB_EVENT_PATH)")
	fs.StringVar(&f.headCommit, "head-commit", "", "head commit SHA (defaults to GITHUB_SHA)")
	fs.StringVar(&f.environment, "environment", f.environment, "metric environment label")
	fs.Var(&f.metricKeys, "metric-key", "metric key filter (repeatable)")
	fs.StringVar(&f.failOn, "fail-on", f.failOn, "failure threshold: never|warn|fail")
	fs.StringVar(&f.githubOutput, "github-output", "", "GitHub Actions output file path (defaults to GITHUB_OUTPUT)")
}

func (f reportFlags) toPublishPullRequestReportRequest() (*committrackerv1.PublishPullRequestReportRequest, error) {
	provider, err := parseProviderKind(f.provider)
	if err != nil {
		return nil, err
	}

	repository := strings.TrimSpace(f.repository)
	if repository == "" {
		repository = strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY"))
	}
	if repository == "" {
		return nil, errors.New("repository is required (--repository or GITHUB_REPOSITORY)")
	}

	headCommit := strings.TrimSpace(f.headCommit)
	if headCommit == "" {
		headCommit = strings.TrimSpace(os.Getenv("GITHUB_SHA"))
	}
	if headCommit == "" {
		return nil, errors.New("head commit is required (--head-commit or GITHUB_SHA)")
	}

	pullRequest, baseCommit, err := resolvePullRequestContext(f.pullRequest, f.baseCommit)
	if err != nil {
		return nil, err
	}

	environment := strings.TrimSpace(f.environment)
	if environment == "" {
		return nil, errors.New("environment is required")
	}

	metricKeys, err := sanitizeMetricKeys(f.metricKeys)
	if err != nil {
		return nil, err
	}

	return &committrackerv1.PublishPullRequestReportRequest{
		Provider:      provider,
		Repository:    repository,
		PullRequest:   pullRequest,
		BaseCommitSha: baseCommit,
		HeadCommitSha: headCommit,
		Environment:   environment,
		MetricKeys:    metricKeys,
	}, nil
}

func sanitizeMetricKeys(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(raw))
	keys := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			return nil, errors.New("metric key cannot be empty")
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		keys = append(keys, trimmed)
	}
	return keys, nil
}

func parseFailOnPolicy(raw string) (failOnPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(failOnPolicyNever):
		return failOnPolicyNever, nil
	case string(failOnPolicyWarn):
		return failOnPolicyWarn, nil
	case string(failOnPolicyFail):
		return failOnPolicyFail, nil
	default:
		return "", fmt.Errorf("invalid --fail-on value: %q (allowed: never|warn|fail)", raw)
	}
}

func (p failOnPolicy) shouldFail(level committrackerv1.EvaluationLevel) bool {
	switch p {
	case failOnPolicyNever:
		return false
	case failOnPolicyWarn:
		return level == committrackerv1.EvaluationLevel_EVALUATION_LEVEL_WARN || level == committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL
	case failOnPolicyFail:
		return level == committrackerv1.EvaluationLevel_EVALUATION_LEVEL_FAIL
	default:
		return false
	}
}
