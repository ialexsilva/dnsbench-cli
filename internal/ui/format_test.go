package ui

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func disableColors(t *testing.T) {
	t.Helper()
	prev := ColorEnabled()
	SetColorEnabled(false)
	t.Cleanup(func() { SetColorEnabled(prev) })
}

func enableColors(t *testing.T) {
	t.Helper()
	orig, had := os.LookupEnv("NO_COLOR")
	prev := ColorEnabled()
	os.Unsetenv("NO_COLOR")
	SetColorEnabled(true)
	t.Cleanup(func() {
		if had {
			os.Setenv("NO_COLOR", orig)
		} else {
			os.Unsetenv("NO_COLOR")
		}
		SetColorEnabled(prev)
	})
}

func TestFormatMs(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.0 ms"},
		{12.34, "12.3 ms"},
		{999.94, "999.9 ms"},
		{1000, "1.0 s"},
		{1234, "1.2 s"},
		{2500, "2.5 s"},
	}
	for _, c := range cases {
		if got := FormatMs(c.in); got != c.want {
			t.Errorf("FormatMs(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatPct(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.0%"},
		{12.34, "12.3%"},
		{100, "100.0%"},
	}
	for _, c := range cases {
		if got := FormatPct(c.in); got != c.want {
			t.Errorf("FormatPct(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBarFractionalWidths(t *testing.T) {
	cases := []struct {
		value float64
		max   float64
		width int
		want  string
	}{
		{1, 2, 4, "██  "},
		{1, 8, 1, "▏"},
		{3, 8, 1, "▍"},
		{5, 8, 1, "▋"},
		{7, 8, 1, "▉"},
		{7, 16, 2, "▉ "},
		{0, 10, 3, "   "},
		{10, 10, 3, "███"},
		{5, 10, 4, "██  "},
		{15, 10, 4, "████"},
		{0.01, 100, 4, "▏   "},
	}
	for _, c := range cases {
		if got := Bar(c.value, c.max, c.width); got != c.want {
			t.Errorf("Bar(%v, %v, %d) = %q, want %q", c.value, c.max, c.width, got, c.want)
		}
	}
}

func TestBarWidthConsistency(t *testing.T) {
	for v := 0.0; v <= 20; v += 0.7 {
		got := Bar(v, 20, 12)
		if n := utf8.RuneCountInString(got); n != 12 {
			t.Errorf("Bar(%v, 20, 12) has %d runes, want 12", v, n)
		}
	}
}

func TestTruncatePadAccentedRunes(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"café", 6, "café  "},
		{"café", 4, "café"},
		{"ação múltipla", 8, "ação mú…"},
		{"abc", 2, "a…"},
		{"x", 1, "x"},
		{"xy", 1, "…"},
		{"", 3, "   "},
	}
	for _, c := range cases {
		got := TruncatePad(c.in, c.w)
		if got != c.want {
			t.Errorf("TruncatePad(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
		if n := utf8.RuneCountInString(got); n != c.w {
			t.Errorf("TruncatePad(%q, %d) has %d runes, want %d", c.in, c.w, n, c.w)
		}
	}
}

func TestTruncateVisiblePreservesANSIAndWidth(t *testing.T) {
	orig, had := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if had {
			os.Setenv("NO_COLOR", orig)
		} else {
			os.Unsetenv("NO_COLOR")
		}
		SetColorEnabled(defaultColorEnabled())
	})
	SetColorEnabled(true)

	got := truncateVisible(Cyan("cached")+" "+Magenta("recursive/TLD"), 10)
	if width := visibleWidth(got); width != 10 {
		t.Fatalf("truncated width = %d, want 10: %q", width, got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Fatalf("truncated ANSI string is not reset: %q", got)
	}
	if plain := stripANSI(got); plain != "cached rec" {
		t.Fatalf("truncated text = %q, want %q", plain, "cached rec")
	}
}

func TestColorToggleAndNoColor(t *testing.T) {
	orig, had := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if had {
			os.Setenv("NO_COLOR", orig)
		} else {
			os.Unsetenv("NO_COLOR")
		}
		SetColorEnabled(defaultColorEnabled())
	})
	SetColorEnabled(true)
	if got := Green("x"); !strings.Contains(got, "\x1b[32m") {
		t.Errorf("Green with colors on = %q, want ANSI green", got)
	}
	if got := Bold("x"); !strings.Contains(got, "\x1b[1m") {
		t.Errorf("Bold with colors on = %q, want ANSI bold", got)
	}
	SetColorEnabled(false)
	if got := Red("x"); got != "x" {
		t.Errorf("Red with colors off = %q, want plain", got)
	}
	os.Setenv("NO_COLOR", "1")
	SetColorEnabled(true)
	if got := Cyan("x"); got != "x" {
		t.Errorf("Cyan with NO_COLOR set = %q, want plain", got)
	}
}
