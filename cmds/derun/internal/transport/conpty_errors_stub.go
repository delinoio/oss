//go:build !windows

package transport

func IsConPTYUnavailableError(_ error) bool {
	return false
}
