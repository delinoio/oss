// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchGithub(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) >= 2 && rest[0] == "pr" && rest[1] == "remediation" {
		value, err := prRemediationCommand(ctx, c, o, rest[2:])
		return emit(value, err), true
	}
	if len(rest) >= 2 && rest[0] == "pr" && rest[1] == "problems" {
		value, err := prProblemsCommand(ctx, c, o, rest[2:])
		return emit(value, err), true
	}
	value, err := githubCommand(ctx, c, rest)
	return emit(value, err), true

}
