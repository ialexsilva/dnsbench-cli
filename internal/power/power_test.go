package power

import (
	"context"
	"testing"
)

func TestKeepAwakeReturnsUsableRelease(t *testing.T) {
	release, _, detail := KeepAwake(context.Background())
	if release == nil {
		t.Fatal("KeepAwake returned a nil release function")
	}
	// release must be safe to call more than once.
	release()
	release()
	_ = detail
}
