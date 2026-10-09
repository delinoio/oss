// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) GetStatus(_ context.Context, req *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	response := connect.NewResponse(&pb.GetStatusResponse{Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, SchemaVersion: store.SchemaVersion, ServerId: string(s.Identity.ServerID), Listener: s.Endpoint.URL, StartedAt: s.Endpoint.StartedAt.Format(time.RFC3339Nano), Stopping: s.stopping.Load(), Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SIDECHAT_QUESTION_RETRY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_CODEX_FORK_V1, pb.SystemCapability_SYSTEM_CAPABILITY_OPENCODE_GO_SUBSCRIPTIONS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_APPROVAL_REVIEW_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_DEFAULTS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V2, pb.SystemCapability_SYSTEM_CAPABILITY_PROJECT_BEHAVIOR_SETTINGS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_REPOSITORY_BRANCH_DISCOVERY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_RESET_CREDITS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_PROJECT_PROMPT_HISTORY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_CODEX_SIDECHAT_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V1, pb.SystemCapability_SYSTEM_CAPABILITY_IMAGE_INPUTS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_SKILLS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SUBSCRIPTIONS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_EXECUTION_STARTUP_V1, pb.SystemCapability_SYSTEM_CAPABILITY_FAILED_SUBSCRIPTION_CLEANUP_V1, pb.SystemCapability_SYSTEM_CAPABILITY_AGENT_WORKER_SOURCE_ROUTES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_REMOTE_REPOSITORIES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_REPOSITORY_CLONE_V1, pb.SystemCapability_SYSTEM_CAPABILITY_GITHUB_REPOSITORY_PICKER_V1, pb.SystemCapability_SYSTEM_CAPABILITY_GITHUB_TOKEN_ONBOARDING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_AGENT_WORKER_WIZARD_V1, pb.SystemCapability_SYSTEM_CAPABILITY_KNOWN_SUBSCRIPTION_MODELS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_LOGIN_V1, pb.SystemCapability_SYSTEM_CAPABILITY_WORKSPACE_STORAGE_V1, pb.SystemCapability_SYSTEM_CAPABILITY_WORKER_NETWORK_BOOTSTRAP_V1, pb.SystemCapability_SYSTEM_CAPABILITY_WORKER_CODEX_PROXY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SUBSCRIPTION_QUOTA_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SUBSCRIPTION_RESET_CREDITS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SUBSCRIPTION_SERVICE_ACCOUNTS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_AUTOMATIC_TITLES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_PERMANENT_SESSION_DELETION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_OUTBOUND_PROXY_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_TERMINALS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_ACCOUNTING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_INPUT_ACCOUNTING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_REQUEST_DIAGNOSTICS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SUBAGENT_OBSERVATION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_SESSION_FORK_V1, pb.SystemCapability_SYSTEM_CAPABILITY_STOPPED_CODEX_ACCOUNT_SWITCH_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_SUBAGENT_CONFIGURATION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_OPENCODE_FOREGROUND_SUBAGENTS_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_SESSION_COMPACTION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_SESSION_COMPACTION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_OPENCODE_SESSION_COMPACTION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_OPENCODE_GENERAL_CHAT_FORK_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_SIDECHAT_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SSH_WORKER_SETUP_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1}})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) StopServer(ctx context.Context, req *connect.Request[pb.StopServerRequest]) (*connect.Response[pb.StopServerResponse], error) {
	id := domain.ID(req.Msg.RequestId)
	lock, err := LockLifecycle(s.Store.Root())
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	defer lock.Close()
	_, err = s.Store.Mutate(ctx, id, "server.stop", struct {
		StartedAt time.Time `json:"started_at"`
	}{s.Endpoint.StartedAt}, func(*store.Tx) (any, error) {
		if err := writeStopped(s.Store.Root(), id, configurationDigest(Config{})); err != nil {
			return nil, err
		}
		return struct {
			Accepted bool `json:"accepted"`
		}{true}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	s.stopping.Store(true)
	// Let the accepted response flush before canceling connection contexts. The
	// durable receipt already prevents an ambiguous client retry from duplicating.
	time.AfterFunc(100*time.Millisecond, s.stop)
	response := connect.NewResponse(&pb.StopServerResponse{RequestId: string(id)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
