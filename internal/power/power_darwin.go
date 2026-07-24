//go:build darwin

package power

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// lookPath is a seam so tests can exercise the "mechanism missing" path.
var lookPath = exec.LookPath

// Acquire shells out to caffeinate, the macOS tool that wraps the IOKit
// power-assertion API. See caffeinateArgs for the flags. Because caffeinate is
// started with -w <pid>, it also exits on its own if this process dies without
// calling release, so the assertion never outlives us.
func Acquire() (func(), error) {
	path, err := lookPath("caffeinate")
	if err != nil {
		return func() {}, fmt.Errorf("%w: caffeinate not found", ErrUnsupported)
	}
	cmd := exec.Command(path, caffeinateArgs(os.Getpid())...)
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
