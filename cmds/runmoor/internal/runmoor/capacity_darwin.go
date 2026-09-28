package runmoor

import (
	"golang.org/x/sys/unix"
	"runtime"
)

func hostCapacity() (Resources, error) {
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Resources{}, problem(ErrCapacity, "Cannot detect host memory.", "Specify explicit host CPU and memory budgets.")
	}
	return Resources{runtime.NumCPU(), int64(memory / (1024 * 1024))}, nil
}
