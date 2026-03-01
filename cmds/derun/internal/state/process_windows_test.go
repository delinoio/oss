//go:build windows

package state

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcessAliveCurrentProcess(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatalf("expected current process to be alive")
	}
}

func TestProcessAliveInvalidPID(t *testing.T) {
	if processAlive(0) {
		t.Fatalf("expected pid=0 to be treated as not alive")
	}
	if processAlive(-1) {
		t.Fatalf("expected negative pid to be treated as not alive")
	}
}

func TestProcessAliveTreatsAccessDeniedAsAlive(t *testing.T) {
	previousOpenProcess := openProcess
	previousWaitForSingleObject := waitForSingleObject
	t.Cleanup(func() {
		openProcess = previousOpenProcess
		waitForSingleObject = previousWaitForSingleObject
	})

	waitCalled := false
	openProcess = func(desiredAccess uint32, inheritHandle bool, processID uint32) (windows.Handle, error) {
		return 0, windows.ERROR_ACCESS_DENIED
	}
	waitForSingleObject = func(handle windows.Handle, waitMilliseconds uint32) (uint32, error) {
		waitCalled = true
		return uint32(windows.WAIT_OBJECT_0), nil
	}

	if !processAlive(1234) {
		t.Fatalf("expected access denied on OpenProcess to be treated as alive")
	}
	if waitCalled {
		t.Fatalf("wait should not run when OpenProcess fails")
	}
}

func TestProcessAliveReturnsFalseForOtherOpenProcessErrors(t *testing.T) {
	previousOpenProcess := openProcess
	previousWaitForSingleObject := waitForSingleObject
	t.Cleanup(func() {
		openProcess = previousOpenProcess
		waitForSingleObject = previousWaitForSingleObject
	})

	waitCalled := false
	openProcess = func(desiredAccess uint32, inheritHandle bool, processID uint32) (windows.Handle, error) {
		return 0, windows.ERROR_INVALID_PARAMETER
	}
	waitForSingleObject = func(handle windows.Handle, waitMilliseconds uint32) (uint32, error) {
		waitCalled = true
		return uint32(windows.WAIT_OBJECT_0), nil
	}

	if processAlive(5678) {
		t.Fatalf("expected non-access-denied OpenProcess errors to be treated as not alive")
	}
	if waitCalled {
		t.Fatalf("wait should not run when OpenProcess fails")
	}
}
