package process

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func preciseBirth(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// comm is parenthesized and may itself contain spaces or closing parentheses.
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) <= 19 {
		return "", fmt.Errorf("incomplete process stat")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19], nil
}
