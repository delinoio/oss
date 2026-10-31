package process

func Contains(scope Scope, pid int) bool {
	if pid <= 0 || pid == scope.OwnerPID || scope.NativeID == 0 {
		return false
	}
	id, e := coalition(pid)
	return e == nil && id == scope.NativeID
}
