package mcp

import (
	"errors"

	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
)

func formatUsageError(reason, hint string) string {
	return formatUsageErrorWithDetails(reason, hint, nil)
}

func formatUsageErrorWithDetails(reason, hint string, details map[string]any) string {
	return errmsg.Usage(reason, hint, details)
}

func requiredFieldError(field, expected string, received any) error {
	return errmsg.Required(field, expected, received)
}

func parseFieldError(field string, err error, details map[string]any) error {
	return errmsg.Parse(field, err, details)
}

func wrapRuntimeError(action string, err error) error {
	return wrapRuntimeErrorWithDetails(action, err, nil)
}

func wrapRuntimeErrorWithDetails(action string, err error, details map[string]any) error {
	return errors.New(errmsg.Runtime(action, err, details))
}
