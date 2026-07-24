// Package power provides best-effort control over the operating system's
// automatic idle sleep while a long-running benchmark is in progress.
//
// The problem it solves: on a laptop left unattended the OS enters idle sleep
// shortly after the display turns off, which suspends the process mid-run and
// poisons every timing measurement taken around the gap. Acquire asks the OS,
// through its native mechanism on each platform, to hold off idle sleep until
// the run finishes:
//
//   - macOS   — caffeinate (wraps the IOKit power-assertion API)
//   - Linux   — a systemd-logind idle inhibitor lock held over D-Bus
//   - Windows — SetThreadExecutionState (ES_SYSTEM_REQUIRED)
//
// The request covers system idle sleep only; the display is allowed to turn
// off, and an explicit user suspend is never blocked. This matches macOS and
// Windows semantics and, on Linux, is why only the "idle" inhibitor category
// is requested rather than "sleep".
//
// Acquire is best-effort and never fails the caller: on failure it returns a
// no-op release and a non-nil error, which callers surface as a notice and
// otherwise ignore. The returned release is always non-nil and safe to call
// any number of times; it blocks until the inhibition has actually been lifted.
package power

import "errors"

// ErrUnsupported reports that the current platform or system configuration
// offers no mechanism to inhibit idle sleep. It is not a failure of the
// request: the run should continue. Callers can test for it with errors.Is to
// distinguish "nothing to do here" from a genuine failure of an available
// mechanism.
var ErrUnsupported = errors.New("no idle-sleep inhibition mechanism available")
