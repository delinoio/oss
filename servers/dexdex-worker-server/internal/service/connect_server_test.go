package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	connect "connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
)

func TestNormalizeSessionOutputFixturePresetCodexReturnsFailedStatus(t *testing.T) {
	client := newWorkerSessionAdapterClient(t)

	response, err := client.NormalizeSessionOutputFixture(
		context.Background(),
		connect.NewRequest(&dexdexv1.NormalizeSessionOutputFixtureRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-codex",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
			Input: &dexdexv1.NormalizeSessionOutputFixtureRequest_FixturePreset{
				FixturePreset: dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_CODEX_CLI_FAILURE,
			},
		}),
	)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputFixture returned error: %v", err)
	}
	if response.Msg.GetSessionStatus() != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED {
		t.Fatalf(
			"unexpected session status: got=%v want=%v",
			response.Msg.GetSessionStatus(),
			dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED,
		)
	}
	if len(response.Msg.GetEvents()) == 0 {
		t.Fatal("expected normalized events")
	}
}

func TestNormalizeSessionOutputFixtureRawJSONLReturnsCompletedStatus(t *testing.T) {
	client := newWorkerSessionAdapterClient(t)

	response, err := client.NormalizeSessionOutputFixture(
		context.Background(),
		connect.NewRequest(&dexdexv1.NormalizeSessionOutputFixtureRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-open-code",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE,
			Input: &dexdexv1.NormalizeSessionOutputFixtureRequest_RawJsonl{
				RawJsonl: `{"type":"step_start","part":{"type":"step-start"}}
{"type":"text","part":{"text":"HELLO"}}
{"type":"step_finish","part":{"reason":"stop"}}`,
			},
		}),
	)
	if err != nil {
		t.Fatalf("NormalizeSessionOutputFixture returned error: %v", err)
	}
	if response.Msg.GetSessionStatus() != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED {
		t.Fatalf(
			"unexpected session status: got=%v want=%v",
			response.Msg.GetSessionStatus(),
			dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
		)
	}
	if len(response.Msg.GetEvents()) != 3 {
		t.Fatalf("unexpected event count: got=%d want=3", len(response.Msg.GetEvents()))
	}
}

func TestNormalizeSessionOutputFixtureRejectsMissingInput(t *testing.T) {
	client := newWorkerSessionAdapterClient(t)

	_, err := client.NormalizeSessionOutputFixture(
		context.Background(),
		connect.NewRequest(&dexdexv1.NormalizeSessionOutputFixtureRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
		}),
	)
	requireWorkerConnectErrorCode(t, err, connect.CodeInvalidArgument)
}

func TestNormalizeSessionOutputFixtureRejectsUnsupportedCliType(t *testing.T) {
	client := newWorkerSessionAdapterClient(t)

	_, err := client.NormalizeSessionOutputFixture(
		context.Background(),
		connect.NewRequest(&dexdexv1.NormalizeSessionOutputFixtureRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_UNSPECIFIED,
			Input: &dexdexv1.NormalizeSessionOutputFixtureRequest_RawJsonl{
				RawJsonl: `{"type":"step_start","part":{"type":"step-start"}}`,
			},
		}),
	)
	requireWorkerConnectErrorCode(t, err, connect.CodeInvalidArgument)
}

func TestNormalizeSessionOutputFixtureRejectsEmptyRawJSONL(t *testing.T) {
	client := newWorkerSessionAdapterClient(t)

	_, err := client.NormalizeSessionOutputFixture(
		context.Background(),
		connect.NewRequest(&dexdexv1.NormalizeSessionOutputFixtureRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
			Input: &dexdexv1.NormalizeSessionOutputFixtureRequest_RawJsonl{
				RawJsonl: "   ",
			},
		}),
	)
	requireWorkerConnectErrorCode(t, err, connect.CodeInvalidArgument)
}

func newWorkerSessionAdapterClient(t *testing.T) dexdexv1connect.WorkerSessionAdapterServiceClient {
	t.Helper()

	service := NewSessionAdapterConnectServer(SessionAdapterConnectServerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewWorkerSessionAdapterServiceHandler(service)
	mux.Handle(path, handler)

	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		httpServer.Close()
	})

	return dexdexv1connect.NewWorkerSessionAdapterServiceClient(httpServer.Client(), httpServer.URL)
}

func requireWorkerConnectErrorCode(t *testing.T, err error, wantCode connect.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected connect error code=%v but got nil", wantCode)
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got=%T err=%v", err, err)
	}
	if connectErr.Code() != wantCode {
		t.Fatalf("unexpected connect error code: got=%v want=%v err=%v", connectErr.Code(), wantCode, err)
	}
}
