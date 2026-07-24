//go:build darwin

package power

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// KeepAwake shells out to caffeinate, the macOS tool that wraps the IOKit
// power-assertion API. The -i flag asserts PreventUserIdleSystemSleep and -w
// ties the helper's lifetime to this process, so caffeinate also exits if we
// crash without calling release.
func KeepAwake(_ context.Context) (release func(), active bool, detail string) {
	path, err := exec.LookPath("caffeinate")
	if err != nil {
		return func() {}, false, "caffeinate not found; the system may sleep during the run"
	}
	cmd := exec.Command(path, "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return func() {}, false, "could not start caffeinate: " + err.Error()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}, true, ""
}
