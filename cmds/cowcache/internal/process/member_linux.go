package process

// Contains verifies live native ancestry under this supervisor. The caller must
// obtain pid from kernel-authenticated IPC, not from the request body.
func Contains(scope Scope, pid int) bool {
	if scope.OwnerPID <= 0 || pid == scope.OwnerPID {
		return false
	}
	first, e := linuxRow(pid)
	if e != nil {
		return false
	}
	seen := map[int]bool{}
	for current := pid; current > 1 && !seen[current]; {
		seen[current] = true
		r, e := linuxRow(current)
		if e != nil {
			return false
		}
		if r.ppid == scope.OwnerPID {
			again, e := linuxRow(pid)
			return e == nil && again.birth == first.birth
		}
		current = r.ppid
	}
	return false
}
