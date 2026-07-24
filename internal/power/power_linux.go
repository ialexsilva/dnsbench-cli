//go:build linux

package power

import (
	"fmt"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Acquire takes a systemd-logind idle inhibitor lock over the system bus and
// keeps the returned file descriptor open. logind holds the lock for exactly as
// long as that descriptor stays open, so any process exit — a clean release, a
// hard kill, or an os.Exit that skips deferred cleanup — releases it
// automatically once the kernel closes the fd. Unlike shelling out to
// systemd-inhibit, the D-Bus call also fails synchronously here, so we never
// report success without actually holding the lock.
func Acquire() (func(), error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return func() {}, fmt.Errorf("%w: connect system bus: %v", ErrUnsupported, err)
	}
	obj := conn.Object("org.freedesktop.login1", dbus.ObjectPath("/org/freedesktop/login1"))
	var fd dbus.UnixFD
	err = obj.Call("org.freedesktop.login1.Manager.Inhibit", 0,
		inhibitWhat, inhibitWho, inhibitWhy, "block").Store(&fd)
	if err != nil {
		_ = conn.Close()
		return func() {}, fmt.Errorf("logind inhibit: %w", err)
	}
	lock := os.NewFile(uintptr(fd), "logind-inhibitor")
	var once sync.Once
	return func() {
		once.Do(func() {
			if lock != nil {
				_ = lock.Close() // closing the fd releases the lock
			}
			_ = conn.Close()
		})
	}, nil
}
