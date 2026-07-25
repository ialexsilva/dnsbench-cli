//go:build darwin

package power

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestCaffeinateRunsInItsOwnSession(t *testing.T) {
	path, err := exec.LookPath("caffeinate")
	if err != nil {
		t.Skip("caffeinate is not available on this machine")
	}
	cmd := newCaffeinateCmd(path, os.Getpid())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start caffeinate: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	child, err := syscall.Getsid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("getsid(caffeinate): %v", err)
	}
	self, err := syscall.Getsid(os.Getpid())
	if err != nil {
		t.Fatalf("getsid(self): %v", err)
	}
	if child == self {
		t.Fatalf("caffeinate shares our session (%d), so it stays attached to the "+
			"controlling terminal and the terminal titles its tab after it", child)
	}
}

func TestAcquireUnsupportedWhenCaffeinateMissing(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	release, err := Acquire()
	if release == nil {
		t.Fatal("release must be non-nil even on failure")
	}
	release() // must not panic
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v; want ErrUnsupported", err)
	}
}
