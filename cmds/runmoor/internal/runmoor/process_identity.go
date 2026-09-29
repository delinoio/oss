package runmoor

import (
	"fmt"
	"strconv"
	"strings"
)

func parseLinuxProcessStartIdentity(stat, bootID string) (string, error) {
	closeName := strings.LastIndex(stat, ")")
	if closeName < 0 {
		return "", fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(stat[closeName+1:])
	// /proc/<pid>/stat starts this suffix at field 3 (state); starttime is 22.
	const starttimeOffset = 22 - 3
	if len(fields) <= starttimeOffset {
		return "", fmt.Errorf("process stat lacks starttime")
	}
	bootID = strings.TrimSpace(bootID)
	if bootID == "" {
		return "", fmt.Errorf("boot identity is empty")
	}
	startTicks, err := strconv.ParseUint(fields[starttimeOffset], 10, 64)
	if err != nil || startTicks == 0 {
		return "", fmt.Errorf("process starttime is invalid")
	}
	return fmt.Sprintf("linux:%s:%d", bootID, startTicks), nil
}
