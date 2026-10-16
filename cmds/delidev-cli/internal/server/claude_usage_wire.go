// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func wireClaudeUsageState(state domain.ClaudeLimitLoginState) pb.ClaudeUsageLoginState {
	switch state {
	case domain.ClaudeLimitLoginPreparing:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_PREPARING
	case domain.ClaudeLimitLoginWaiting:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING
	case domain.ClaudeLimitLoginConfirming:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CONFIRMING
	case domain.ClaudeLimitLoginLinked:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_LINKED
	case domain.ClaudeLimitLoginCanceled:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CANCELED
	case domain.ClaudeLimitLoginExpired:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_EXPIRED
	case domain.ClaudeLimitLoginUnsupported:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_UNSUPPORTED
	case domain.ClaudeLimitLoginFailed:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_FAILED
	case domain.ClaudeLimitLoginRecovery:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_RECOVERY_REQUIRED
	}
	return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_UNSPECIFIED
}

var claudeUsageFailureWire = map[domain.ClaudeLimitFailure]pb.ClaudeUsageFailure{
	domain.ClaudeLimitFailureTimeout:             pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_TIMEOUT,
	domain.ClaudeLimitFailureNetwork:             pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_NETWORK,
	domain.ClaudeLimitFailureRoute:               pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_ROUTE_UNSUPPORTED,
	domain.ClaudeLimitFailureRedirect:            pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_REDIRECT,
	domain.ClaudeLimitFailureOversized:           pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_OVERSIZED,
	domain.ClaudeLimitFailureMalformed:           pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_MALFORMED,
	domain.ClaudeLimitFailureEmpty:               pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_EMPTY,
	domain.ClaudeLimitFailureUnauthorized:        pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_UNAUTHORIZED,
	domain.ClaudeLimitFailureForbidden:           pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_FORBIDDEN,
	domain.ClaudeLimitFailureRateLimited:         pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_RATE_LIMITED,
	domain.ClaudeLimitFailureServer:              pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_SERVER_ERROR,
	domain.ClaudeLimitFailureCredentials:         pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_CREDENTIALS_UNAVAILABLE,
	domain.ClaudeLimitFailureStorage:             pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_STORAGE_UNSUPPORTED,
	domain.ClaudeLimitFailureRefreshRejected:     pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_REFRESH_REJECTED,
	domain.ClaudeLimitFailureRefreshUnsupported:  pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_REFRESH_UNSUPPORTED,
	domain.ClaudeLimitFailureRefreshUncertain:    pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_REFRESH_UNCERTAIN,
	domain.ClaudeLimitFailureIdentityChanged:     pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_IDENTITY_CHANGED,
	domain.ClaudeLimitFailureLoginRejected:       pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_LOGIN_REJECTED,
	domain.ClaudeLimitFailureLoginUnsupported:    pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_LOGIN_UNSUPPORTED,
	domain.ClaudeLimitFailureProfileUnsafe:       pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_PROFILE_UNSAFE,
	domain.ClaudeLimitFailureCleanupUncertain:    pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_CLEANUP_UNCERTAIN,
	domain.ClaudeLimitFailureInstallation:        pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_INSTALLATION_UNSUPPORTED,
	domain.ClaudeLimitFailureInterrupted:         pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_INTERRUPTED,
	domain.ClaudeLimitFailureAuthorityChanged:    pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_AUTHORITY_CHANGED,
	domain.ClaudeLimitFailureLoginNotInteractive: pb.ClaudeUsageFailure_CLAUDE_USAGE_FAILURE_LOGIN_MANUAL_CODE_REQUIRED,
}

func wireClaudeUsageFailure(failure domain.ClaudeLimitFailure) pb.ClaudeUsageFailure {
	return claudeUsageFailureWire[failure]
}
