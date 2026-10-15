package runmoor

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsPower struct {
	mu     sync.Mutex
	handle windows.Handle
}

func (p *windowsPower) Set(active bool) *Problem {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !active {
		p.release()
		return nil
	}
	if p.handle != 0 {
		return nil
	}
	reason, _ := windows.UTF16PtrFromString("Active Runmoor ephemeral runners")
	// REASON_CONTEXT, version 0, POWER_REQUEST_CONTEXT_SIMPLE_STRING.
	context := struct {
		Version, Flags uint32
		Reason         *uint16
	}{0, 1, reason}
	k := windows.NewLazySystemDLL("kernel32.dll")
	h, _, _ := k.NewProc("PowerCreateRequest").Call(uintptr(unsafe.Pointer(&context)))
	if h != 0 && h != uintptr(windows.InvalidHandle) {
		ok, _, _ := k.NewProc("PowerSetRequest").Call(h, 1)
		if ok != 0 {
			p.handle = windows.Handle(h)
			return nil
		}
		windows.CloseHandle(windows.Handle(h))
	}
	return problem(ErrPower, "Cannot inhibit Windows sleep.", "Check power policy; jobs continue without guaranteed sleep inhibition.")
}
func (p *windowsPower) release() {
	if p.handle != 0 {
		windows.NewLazySystemDLL("kernel32.dll").NewProc("PowerClearRequest").Call(uintptr(p.handle), 1)
		windows.CloseHandle(p.handle)
		p.handle = 0
	}
}
func (p *windowsPower) Release() { p.mu.Lock(); defer p.mu.Unlock(); p.release() }
func init()                      { newPowerController = func() PowerController { return &windowsPower{} } }
