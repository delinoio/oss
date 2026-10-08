// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
	"log/slog"
)

func ordinaryExecutionTools(logger *slog.Logger) executionenv.Ordinary {
	context := executionenv.Current()
	if logger != nil {
		logger.Debug("ordinary_execution_tool_context", "gh_selector_available", context.Available())
	}
	return context
}
