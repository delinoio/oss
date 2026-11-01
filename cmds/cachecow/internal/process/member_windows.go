package process

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

var activeJob windows.Handle

func Contains(scope Scope, pid int) bool {
	if pid <= 0 || pid == scope.OwnerPID || activeJob == 0 {
		return false
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var yes int32
	r, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob").Call(uintptr(h), uintptr(activeJob), uintptr(unsafe.Pointer(&yes)))
	return r != 0 && yes != 0
}
