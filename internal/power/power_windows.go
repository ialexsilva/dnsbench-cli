//go:build windows

package power

import (
	"context"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// KeepAwake calls SetThreadExecutionState with ES_CONTINUOUS|ES_SYSTEM_REQUIRED,
// the canonical Windows API for signalling that the machine is in use. The
// execution state is per-thread, so both the request and its later clear run on
// a single OS-locked thread that stays alive until release.
func KeepAwake(_ context.Context) (release func(), active bool, detail string) {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	if err := proc.Find(); err != nil {
		return func() {}, false, "SetThreadExecutionState unavailable; the system may sleep during the run"
	}
	done := make(chan struct{})
	ready := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, _, _ := proc.Call(uintptr(esContinuous | esSystemRequired))
		ready <- r != 0
		<-done
		proc.Call(uintptr(esContinuous)) // clear the request, letting the OS sleep again
	}()
	if !<-ready {
		close(done)
		return func() {}, false, "SetThreadExecutionState failed; the system may sleep during the run"
	}
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
	}, true, ""
}
