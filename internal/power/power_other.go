//go:build !darwin && !linux && !windows

package power

// Acquire is a no-op on platforms without a supported idle-sleep mechanism.
func Acquire() (func(), error) {
	return func() {}, ErrUnsupported
}
