package runmoor

import (
	"golang.org/x/sys/unix"
	"runtime"
)

func hostCapacity() (Resources, error) {
	var info unix.Sysinfo_t
	if unix.Sysinfo(&info) != nil {
		return Resources{}, problem(ErrCapacity, "Cannot detect host memory.", "Specify explicit host CPU and memory budgets.")
	}
	return Resources{runtime.NumCPU(), int64(info.Totalram) * int64(info.Unit) / (1024 * 1024)}, nil
}
