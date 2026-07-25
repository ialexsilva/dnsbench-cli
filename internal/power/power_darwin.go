//go:build darwin

package power

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// lookPath is a seam so tests can exercise the "mechanism missing" path.
var lookPath = exec.LookPath

// newCaffeinateCmd builds the caffeinate child process.
//
// Setsid puts caffeinate in its own session, detached from our controlling
// terminal. Terminal.app and iTerm2 name a tab after the processes attached to
// its tty, so an inherited caffeinate renames the tab to "caffeinate" for the
// whole run. Detaching also keeps caffeinate out of the foreground process
// group, so the Ctrl+C meant for the benchmark no longer kills the power
// assertion before the report is written; release() and the -w <pid> watchdog
// still guarantee it never outlives us.
func newCaffeinateCmd(path string, pid int) *exec.Cmd {
	cmd := exec.Command(path, caffeinateArgs(pid)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// Acquire shells out to caffeinate, the macOS tool that wraps the IOKit
// power-assertion API. See caffeinateArgs for the flags. Because caffeinate is
// started with -w <pid>, it also exits on its own if this process dies without
// calling release, so the assertion never outlives us.
func Acquire() (func(), error) {
	path, err := lookPath("caffeinate")
	if err != nil {
		return func() {}, fmt.Errorf("%w: caffeinate not found", ErrUnsupported)
	}
	cmd := newCaffeinateCmd(path, os.Getpid())
	if err := cmd.Start(); err != nil {
		return func() {}, fmt.Errorf("start caffeinate: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}, nil
}
