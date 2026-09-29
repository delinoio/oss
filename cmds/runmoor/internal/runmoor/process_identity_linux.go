//go:build linux

package runmoor

import (
	"fmt"
	"os"
)

func tartRunProcessStartIdentity(pid int) (string, error) {
	if pid <= 0 {
		return "", os.ErrNotExist
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return parseLinuxProcessStartIdentity(string(stat), string(bootID))
}
