package power

import "testing"

func TestAcquireReturnsUsableRelease(t *testing.T) {
	release, _ := Acquire()
	if release == nil {
		t.Fatal("Acquire returned a nil release function")
	}
	release()
	release() // must be safe to call more than once
}
