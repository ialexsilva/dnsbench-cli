//go:build !darwin && !linux && !windows

package power

import "context"

// KeepAwake is a no-op on platforms without a supported idle-sleep mechanism.
func KeepAwake(_ context.Context) (release func(), active bool, detail string) {
	return func() {}, false, ""
}
