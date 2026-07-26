package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestSpinnerNonTTYPrintsStaticLines(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, false, "Characterizing 3 servers", 3)
	s.Start()
	s.Inc()
	s.Inc()
	s.Stop("done")
	out := buf.String()
	if !strings.Contains(out, "Characterizing 3 servers...") {
		t.Fatalf("non-TTY spinner missing start line:\n%q", out)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("non-TTY spinner missing final message:\n%q", out)
	}
	if strings.Contains(out, "\r") || strings.Contains(out, "\x1b[K") {
		t.Fatalf("non-TTY spinner should not emit cursor control:\n%q", out)
	}
}

func TestSpinnerRenderShowsOnlyCount(t *testing.T) {
	disableColors(t)

	var buf bytes.Buffer
	s := NewSpinner(&buf, true, "Characterizing 2 servers", 2)
	s.Inc()
	s.render("⠋")

	const want = "\r\x1b[K⠋ Characterizing 2 servers  1/2"
	if got := buf.String(); got != want {
		t.Fatalf("rendered spinner = %q, want %q", got, want)
	}
}

func TestSpinnerTTYRedrawsInPlaceAndClears(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, true, "Working", 2)
	s.Start()
	s.Inc()
	s.Stop(Green("✓") + " finished")
	out := buf.String()
	if !strings.Contains(out, "\r\x1b[K") {
		t.Fatalf("TTY spinner should redraw in place with a carriage return + clear:\n%q", out)
	}
	if !strings.Contains(out, "/2") {
		t.Fatalf("TTY spinner should show the completed/total count:\n%q", out)
	}
	if !strings.Contains(out, "finished") {
		t.Fatalf("TTY spinner missing final message:\n%q", out)
	}
	if !strings.HasSuffix(out, "finished\n") {
		t.Fatalf("final message should end the output on its own line:\n%q", out)
	}
}
