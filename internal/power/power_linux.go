//go:build linux

package power

import (
	"context"
	"os/exec"
	"sync"
)

// KeepAwake takes a systemd-logind idle inhibitor lock by running
// systemd-inhibit, which holds the lock for as long as its child process lives.
// We keep a trivial child (sleep) alive and kill it on release. Non-systemd
// systems have no comparable standard mechanism, so there we degrade to a
// no-op.
func KeepAwake(_ context.Context) (release func(), active bool, detail string) {
	path, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return func() {}, false, "systemd-inhibit not found; the system may sleep during the run"
	}
	cmd := exec.Command(path,
		"--what=idle:sleep",
		"--who=dnsbench",
		"--why=DNS benchmark in progress",
		"--mode=block",
		"sleep", "infinity",
	)
	if err := cmd.Start(); err != nil {
		return func() {}, false, "could not start systemd-inhibit: " + err.Error()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}, true, ""
}
