package cli

import "github.com/delinoio/oss/cmds/derun/internal/errmsg"

func formatUsageError(reason, hint string) string {
	return formatUsageErrorWithDetails(reason, hint, nil)
}

func formatUsageErrorWithDetails(reason, hint string, details map[string]any) string {
	return errmsg.Usage(reason, hint, details)
}

func formatRuntimeError(action string, err error) string {
	return formatRuntimeErrorWithDetails(action, err, nil)
}

func formatRuntimeErrorWithDetails(action string, err error, details map[string]any) string {
	return errmsg.Runtime(action, err, details)
}
