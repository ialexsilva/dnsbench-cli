//go:build windows

package power

import (
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// Acquire calls SetThreadExecutionState with ES_CONTINUOUS|ES_SYSTEM_REQUIRED,
// the canonical Windows API for signalling that the machine is in use. The
// execution state is per-thread, so the request and its later clear both run on
// a single OS-locked thread that stays alive until release. release blocks until
// the request has actually been cleared, matching the synchronous behaviour of
// the other platforms.
func Acquire() (func(), error) {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	if err := proc.Find(); err != nil {
		return func() {}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	release := make(chan struct{})
	cleared := make(chan struct{})
	requested := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		proc.Call(uintptr(esContinuous | esSystemRequired))
		close(requested)
		<-release
		proc.Call(uintptr(esContinuous)) // clear the request, letting the OS sleep again
		close(cleared)
	}()
	<-requested
	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
			<-cleared // block until the request is actually cleared
		})
	}, nil
}
