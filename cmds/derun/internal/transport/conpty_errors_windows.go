//go:build windows

package transport

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

func IsConPTYUnavailableError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, windows.ERROR_PROC_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) ||
		errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return true
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "create pseudo console") &&
		(strings.Contains(message, "procedure could not be found") ||
			strings.Contains(message, "not implemented") ||
			strings.Contains(message, "not supported"))
}
