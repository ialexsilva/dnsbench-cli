package power

import (
	"slices"
	"testing"
)

func TestCaffeinateArgs(t *testing.T) {
	got := caffeinateArgs(4321)
	want := []string{"-i", "-w", "4321"}
	if !slices.Equal(got, want) {
		t.Fatalf("caffeinateArgs(4321) = %v; want %v", got, want)
	}
}

// The inhibitor policy must stay idle-only so Linux matches macOS/Windows:
// suppress automatic idle sleep without blocking an explicit user suspend.
func TestInhibitPolicyIsIdleOnly(t *testing.T) {
	if inhibitWhat != "idle" {
		t.Fatalf("inhibitWhat = %q; must be %q so an explicit suspend is not blocked", inhibitWhat, "idle")
	}
}
