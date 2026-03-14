package worker

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
)

// Client wraps the Connect RPC client to the worker server's WorkerSessionAdapterService.
type Client struct {
	client dexdexv1connect.WorkerSessionAdapterServiceClient
	logger *slog.Logger

	mu               sync.RWMutex
	cachedCaps       []*dexdexv1.AgentCapability
	cachedCapsExpiry time.Time
}

// NewClient creates a new worker client with the given worker server URL.
func NewClient(workerServerURL string, logger *slog.Logger) *Client {
	return &Client{
		client: dexdexv1connect.NewWorkerSessionAdapterServiceClient(http.DefaultClient, workerServerURL),
		logger: logger,
	}
}

// GetAgentCapabilities returns cached agent capabilities (5-min TTL).
func (c *Client) GetAgentCapabilities(ctx context.Context) ([]*dexdexv1.AgentCapability, error) {
	c.mu.RLock()
	if c.cachedCaps != nil && time.Now().Before(c.cachedCapsExpiry) {
		caps := c.cachedCaps
		c.mu.RUnlock()
		return caps, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.cachedCaps != nil && time.Now().Before(c.cachedCapsExpiry) {
		return c.cachedCaps, nil
	}

	resp, err := c.client.GetAgentCapabilities(ctx, connect.NewRequest(&dexdexv1.GetAgentCapabilitiesRequest{}))
	if err != nil {
		c.logger.Error("failed to get agent capabilities from worker", "error", err)
		return nil, err
	}

	c.cachedCaps = resp.Msg.Capabilities
	c.cachedCapsExpiry = time.Now().Add(5 * time.Minute)
	c.logger.Info("cached agent capabilities", "count", len(c.cachedCaps))

	return c.cachedCaps, nil
}

// ForkSession calls the worker's ForkSessionAdapter RPC.
func (c *Client) ForkSession(ctx context.Context, sessionID string, forkIntent dexdexv1.SessionForkIntent, prompt string) (string, error) {
	resp, err := c.client.ForkSessionAdapter(ctx, connect.NewRequest(&dexdexv1.ForkSessionAdapterRequest{
		SessionId:  sessionID,
		ForkIntent: forkIntent,
		Prompt:     prompt,
	}))
	if err != nil {
		return "", err
	}
	return resp.Msg.ForkedSessionId, nil
}

// StartExecution calls the worker's StartExecution streaming RPC and returns a stream of events.
func (c *Client) StartExecution(ctx context.Context, req *dexdexv1.StartExecutionRequest) (*connect.ServerStreamForClient[dexdexv1.ExecutionEvent], error) {
	stream, err := c.client.StartExecution(ctx, connect.NewRequest(req))
	if err != nil {
		c.logger.Error("failed to start execution on worker", "session_id", req.SessionId, "error", err)
		return nil, err
	}
	return stream, nil
}

// SubmitWorkerInput relays user input to a running session on the worker.
func (c *Client) SubmitWorkerInput(ctx context.Context, sessionID, inputText string) error {
	_, err := c.client.SubmitWorkerInput(ctx, connect.NewRequest(&dexdexv1.SubmitWorkerInputRequest{
		SessionId: sessionID,
		InputText: inputText,
	}))
	if err != nil {
		c.logger.Error("failed to submit worker input", "session_id", sessionID, "error", err)
		return err
	}
	return nil
}

// CancelExecution cancels a running session on the worker.
func (c *Client) CancelExecution(ctx context.Context, sessionID string) error {
	_, err := c.client.CancelExecution(ctx, connect.NewRequest(&dexdexv1.CancelExecutionRequest{
		SessionId: sessionID,
	}))
	if err != nil {
		c.logger.Error("failed to cancel execution", "session_id", sessionID, "error", err)
		return err
	}
	return nil
}
