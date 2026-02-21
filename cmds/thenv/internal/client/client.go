package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/pkg/thenv/api"
)

type Client struct {
	logger *slog.Logger
	token  string

	pushClient     *connect.Client[api.PushBundleVersionRequest, api.PushBundleVersionResponse]
	pullClient     *connect.Client[api.PullActiveBundleRequest, api.PullActiveBundleResponse]
	listClient     *connect.Client[api.ListBundleVersionsRequest, api.ListBundleVersionsResponse]
	rotateClient   *connect.Client[api.RotateBundleVersionRequest, api.RotateBundleVersionResponse]
	activateClient *connect.Client[api.ActivateBundleVersionRequest, api.ActivateBundleVersionResponse]
	setPolicy      *connect.Client[api.SetPolicyRequest, api.SetPolicyResponse]
	getPolicy      *connect.Client[api.GetPolicyRequest, api.GetPolicyResponse]
	auditClient    *connect.Client[api.ListAuditEventsRequest, api.ListAuditEventsResponse]
}

func New(baseURL string, token string, logger *slog.Logger) (*Client, error) {
	trimmedURL := strings.TrimSpace(baseURL)
	if trimmedURL == "" {
		return nil, errors.New("server URL is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("token is required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	codec := api.JSONCodec{}
	httpClient := &http.Client{Timeout: 20 * time.Second}

	return &Client{
		logger: logger,
		token:  token,
		pushClient: connect.NewClient[api.PushBundleVersionRequest, api.PushBundleVersionResponse](
			httpClient,
			trimmedURL+api.ProcedurePushBundleVersion,
			connect.WithCodec(codec),
		),
		pullClient: connect.NewClient[api.PullActiveBundleRequest, api.PullActiveBundleResponse](
			httpClient,
			trimmedURL+api.ProcedurePullActiveBundle,
			connect.WithCodec(codec),
		),
		listClient: connect.NewClient[api.ListBundleVersionsRequest, api.ListBundleVersionsResponse](
			httpClient,
			trimmedURL+api.ProcedureListBundleVersions,
			connect.WithCodec(codec),
		),
		rotateClient: connect.NewClient[api.RotateBundleVersionRequest, api.RotateBundleVersionResponse](
			httpClient,
			trimmedURL+api.ProcedureRotateBundleVersion,
			connect.WithCodec(codec),
		),
		activateClient: connect.NewClient[api.ActivateBundleVersionRequest, api.ActivateBundleVersionResponse](
			httpClient,
			trimmedURL+api.ProcedureActivateBundle,
			connect.WithCodec(codec),
		),
		setPolicy: connect.NewClient[api.SetPolicyRequest, api.SetPolicyResponse](
			httpClient,
			trimmedURL+api.ProcedureSetPolicy,
			connect.WithCodec(codec),
		),
		getPolicy: connect.NewClient[api.GetPolicyRequest, api.GetPolicyResponse](
			httpClient,
			trimmedURL+api.ProcedureGetPolicy,
			connect.WithCodec(codec),
		),
		auditClient: connect.NewClient[api.ListAuditEventsRequest, api.ListAuditEventsResponse](
			httpClient,
			trimmedURL+api.ProcedureListAuditEvents,
			connect.WithCodec(codec),
		),
	}, nil
}

func (c *Client) PushBundleVersion(ctx context.Context, request api.PushBundleVersionRequest) (*api.PushBundleVersionResponse, error) {
	response, err := c.pushClient.CallUnary(ctx, newRequest(c.token, &request))
	if err != nil {
		return nil, fmt.Errorf("push bundle version: %w", err)
	}
	return response.Msg, nil
}

func (c *Client) PullActiveBundle(ctx context.Context, request api.PullActiveBundleRequest) (*api.PullActiveBundleResponse, error) {
	response, err := c.pullClient.CallUnary(ctx, newRequest(c.token, &request))
	if err != nil {
		return nil, fmt.Errorf("pull active bundle: %w", err)
	}
	return response.Msg, nil
}

func (c *Client) ListBundleVersions(ctx context.Context, request api.ListBundleVersionsRequest) (*api.ListBundleVersionsResponse, error) {
	response, err := c.listClient.CallUnary(ctx, newRequest(c.token, &request))
	if err != nil {
		return nil, fmt.Errorf("list bundle versions: %w", err)
	}
	return response.Msg, nil
}

func (c *Client) RotateBundleVersion(ctx context.Context, request api.RotateBundleVersionRequest) (*api.RotateBundleVersionResponse, error) {
	response, err := c.rotateClient.CallUnary(ctx, newRequest(c.token, &request))
	if err != nil {
		return nil, fmt.Errorf("rotate bundle version: %w", err)
	}
	return response.Msg, nil
}

func newRequest[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	request.Header().Set("X-Request-Id", requestID())
	return request
}

func requestID() string {
	return fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
}
