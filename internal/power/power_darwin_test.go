//go:build darwin

package power

import (
	"errors"
	"os/exec"
	"testing"
)

func TestAcquireUnsupportedWhenCaffeinateMissing(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	release, err := Acquire()
	if release == nil {
		t.Fatal("release must be non-nil even on failure")
	}
	release() // must not panic
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v; want ErrUnsupported", err)
	}
}
