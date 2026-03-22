package cli

import "fmt"

func formatUsageError(reason, hint string) string {
	if hint == "" {
		return fmt.Sprintf("invalid arguments: %s", reason)
	}
	return fmt.Sprintf("invalid arguments: %s; hint: %s", reason, hint)
}

func formatRuntimeError(action string, err error) string {
	if err == nil {
		return fmt.Sprintf("failed to %s", action)
	}
	return fmt.Sprintf("failed to %s: %v", action, err)
}
