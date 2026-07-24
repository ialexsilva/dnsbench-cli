// Package power provides best-effort control over the operating system's
// automatic idle sleep while a long-running benchmark is in progress.
//
// The problem it solves: on a laptop left unattended the OS enters idle sleep
// shortly after the display turns off, which suspends the process mid-run and
// poisons every timing measurement taken around the gap. KeepAwake asks the OS,
// through its native mechanism on each platform, to hold off idle sleep until
// the run finishes:
//
//   - macOS   — caffeinate (wraps the IOKit power-assertion API)
//   - Linux   — systemd-inhibit (a systemd-logind idle inhibitor lock)
//   - Windows — SetThreadExecutionState (ES_SYSTEM_REQUIRED)
//
// The request covers system idle sleep only; the display is allowed to turn
// off as usual. KeepAwake is best-effort and never fails the caller: when no
// mechanism is available it returns a no-op release, active=false and a
// human-readable reason in detail. The returned release is always safe to call,
// exactly once or more than once.
package power
