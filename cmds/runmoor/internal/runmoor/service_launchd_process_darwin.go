//go:build darwin

package runmoor

import "golang.org/x/sys/unix"

func launchdProcessCommandLine(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, errInvalidServiceDefinition
	}
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, errInvalidServiceDefinition
	}
	return parseLaunchdProcessArguments(data)
}
