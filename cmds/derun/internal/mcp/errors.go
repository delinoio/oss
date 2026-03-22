package mcp

import "fmt"

func formatUsageError(reason, hint string) string {
	if hint == "" {
		return fmt.Sprintf("invalid arguments: %s", reason)
	}
	return fmt.Sprintf("invalid arguments: %s; hint: %s", reason, hint)
}

func requiredFieldError(field, expected string) error {
	if expected == "" {
		return fmt.Errorf("%s is required", field)
	}
	return fmt.Errorf("%s is required; expected %s", field, expected)
}

func wrapRuntimeError(action string, err error) error {
	if err == nil {
		return fmt.Errorf("failed to %s", action)
	}
	return fmt.Errorf("failed to %s: %w", action, err)
}
