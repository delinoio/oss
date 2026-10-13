//go:build windows

package codex

// Windows ownership is covered by the process package's job-object tests.
func processAlive(int) bool { return false }
