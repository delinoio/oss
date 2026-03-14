package worker

import (
	"context"
	"log/slog"
	"net/http"
	"os"
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

// NewClient creates a new worker client. URL defaults to DEXDEX_WORKER_SERVER_URL env or http://127.0.0.1:7879.
func NewClient(logger *slog.Logger) *Client {
	url := os.Getenv("DEXDEX_WORKER_SERVER_URL")
	if url == "" {
		url = "http://127.0.0.1:7879"
	}

	return &Client{
		client: dexdexv1connect.NewWorkerSessionAdapterServiceClient(http.DefaultClient, url),
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
