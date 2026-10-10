package worker

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Cleanup uncertainty takes precedence over inference errors. Only confirmed
// removal and parent synchronization permit an ordinary terminal title result.
func finishTitleRuntimeRemoval(home string, output json.RawMessage, returned error, remove, syncParent func(string) error) (json.RawMessage, error) {
	if err := remove(home); err != nil {
		return nil, domain.Fail(domain.RecoveryRequired, "The private automatic title runtime could not be removed.", "Retain its native ownership and reconcile cleanup before continuing.")
	}
	if err := syncParent(home); err != nil {
		return nil, domain.Fail(domain.RecoveryRequired, "The automatic title runtime removal could not be synchronized.", "Retain its cleanup evidence before reporting the completed title.")
	}
	return output, returned
}
