package power

import "strconv"

// The idle-sleep policy is deliberately narrow and identical across platforms:
// suppress automatic idle sleep, but let the display turn off and never block
// an explicit user-requested suspend. On Linux that means requesting only the
// "idle" systemd-logind inhibitor category, not "sleep" (which would also fight
// an explicit suspend). These values are defined here, free of any build tag,
// so the policy can be unit-tested on any platform.
const (
	inhibitWhat = "idle"
	inhibitWho  = "dnsbench"
	inhibitWhy  = "DNS benchmark in progress"
)

// caffeinateArgs builds the macOS caffeinate argument list. -i asserts
// PreventUserIdleSystemSleep (system idle only; the display may still sleep)
// and -w ties caffeinate's lifetime to this process, so it exits even if we
// terminate without calling release.
func caffeinateArgs(pid int) []string {
	return []string{"-i", "-w", strconv.Itoa(pid)}
}
