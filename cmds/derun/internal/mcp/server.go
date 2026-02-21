package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/retention"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

type Server struct {
	store        *state.Store
	logger       *logging.Logger
	gcInterval   time.Duration
	retentionTTL time.Duration
	writeMu      sync.Mutex
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallParams struct {
	Name      contracts.DerunMCPTool `json:"name"`
	Arguments map[string]any         `json:"arguments"`
}

func NewServer(store *state.Store, logger *logging.Logger, gcInterval time.Duration, retentionTTL time.Duration) *Server {
	return &Server{store: store, logger: logger, gcInterval: gcInterval, retentionTTL: retentionTTL}
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.gcInterval > 0 {
		ticker := time.NewTicker(s.gcInterval)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					result, err := retention.Sweep(s.store, s.retentionTTL, s.logger)
					if err != nil {
						s.logger.Event("cleanup_result", map[string]any{"cleanup_result": "error", "error": err.Error()})
						continue
					}
					s.logger.Event("cleanup_result", map[string]any{"cleanup_result": "ok", "checked": result.Checked, "removed": result.Removed})
				}
			}
		}()
	}

	reader := bufio.NewReader(in)
	for {
		body, err := readFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			if err := s.writeResponse(out, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "invalid json"}}); err != nil {
				return err
			}
			continue
		}

		if req.JSONRPC == "" {
			req.JSONRPC = "2.0"
		}
		if req.Method == "" {
			if req.ID != nil {
				if err := s.writeResponse(out, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32600, Message: "missing method"}}); err != nil {
					return err
				}
			}
			continue
		}

		resp := s.handleRequest(req)
		if req.ID == nil {
			continue
		}
		if err := s.writeResponse(out, resp); err != nil {
			return err
		}
	}
}

func (s *Server) handleRequest(req rpcRequest) rpcResponse {
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		response.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "derun",
				"version": "0.1.0",
			},
		}
		return response
	case "notifications/initialized":
		response.Result = map[string]any{}
		return response
	case "ping":
		response.Result = map[string]any{"ok": true}
		return response
	case "tools/list":
		response.Result = map[string]any{"tools": toolDefinitions()}
		return response
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			response.Error = &rpcError{Code: -32602, Message: "invalid tools/call params"}
			return response
		}
		payload, err := s.callTool(params.Name, params.Arguments)
		if err != nil {
			response.Error = &rpcError{Code: -32000, Message: err.Error()}
			return response
		}
		payloadJSON, _ := json.Marshal(payload)
		response.Result = map[string]any{
			"isError": false,
			"content": []map[string]any{{
				"type": "text",
				"text": string(payloadJSON),
			}},
			"structuredContent": payload,
		}
		return response
	default:
		response.Error = &rpcError{Code: -32601, Message: "method not found"}
		return response
	}
}

func (s *Server) callTool(name contracts.DerunMCPTool, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case contracts.DerunMCPToolListSessions:
		return handleListSessions(s.store, args)
	case contracts.DerunMCPToolGetSession:
		return handleGetSession(s.store, args)
	case contracts.DerunMCPToolReadOutput:
		return handleReadOutput(s.store, args)
	case contracts.DerunMCPToolWaitOutput:
		return handleWaitOutput(s.store, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) writeResponse(out io.Writer, resp rpcResponse) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := io.WriteString(out, frame); err != nil {
		return fmt.Errorf("write response header: %w", err)
	}
	if _, err := out.Write(body); err != nil {
		return fmt.Errorf("write response body: %w", err)
	}
	return nil
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.EqualFold(key, "Content-Length") {
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid content length: %w", err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return body, nil
}

func anyToInt(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int32:
		return int(typed), nil
	case int64:
		return int(typed), nil
	case float64:
		return int(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, err
		}
		return int(parsed), nil
	default:
		return 0, fmt.Errorf("unsupported number type %T", value)
	}
}
