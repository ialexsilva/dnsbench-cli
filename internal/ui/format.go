package ui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"dnsbench/internal/model"
)

const ansiReset = "\x1b[0m"

var colorEnabled = defaultColorEnabled()

func defaultColorEnabled() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor
}

func SetColorEnabled(on bool) {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		on = false
	}
	colorEnabled = on
}

func ColorEnabled() bool { return colorEnabled }

func colorize(code, s string) string {
	if !colorEnabled || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + ansiReset
}

func Green(s string) string   { return colorize("32", s) }
func Yellow(s string) string  { return colorize("33", s) }
func Red(s string) string     { return colorize("31", s) }
func Cyan(s string) string    { return colorize("36", s) }
func Magenta(s string) string { return colorize("35", s) }
func Gray(s string) string    { return colorize("90", s) }
func White(s string) string   { return colorize("97", s) }
func Bold(s string) string    { return colorize("1", s) }
func Dim(s string) string     { return colorize("2", s) }

func CategoryColor(c model.Category) func(string) string {
	switch c {
	case model.CatCached:
		return Cyan
	case model.CatUncached:
		return Yellow
	}
	return Magenta
}

func FormatMs(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1f s", v/1000)
	}
	return fmt.Sprintf("%.1f ms", v)
}

func FormatPct(v float64) string { return fmt.Sprintf("%.1f%%", v) }

func Bar(value, max float64, width int) string {
	if width <= 0 {
		return ""
	}
	if max <= 0 || value <= 0 {
		return strings.Repeat(" ", width)
	}
	if value > max {
		value = max
	}
	cells := value / max * float64(width)
	full := int(cells)
	eighths := int(math.Round((cells - float64(full)) * 8))
	if eighths == 8 {
		full++
		eighths = 0
	}
	if full >= width {
		full = width
		eighths = 0
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("█", full))
	used := full
	if eighths > 0 {
		b.WriteRune(rune(0x2590 - eighths))
		used++
	} else if full == 0 {
		b.WriteRune('▏')
		used++
	}
	if used < width {
		b.WriteString(strings.Repeat(" ", width-used))
	}
	return b.String()
}

func TruncatePad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > w {
		if w == 1 {
			return "…"
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func visibleWidth(s string) int { return len([]rune(stripANSI(s))) }

func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if visibleWidth(s) <= width {
		return s
	}

	var b strings.Builder
	visible := 0
	sawANSI := false
	for i := 0; i < len(s) && visible < width; {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			b.WriteString(s[i:j])
			sawANSI = true
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
		visible++
	}
	if sawANSI {
		b.WriteString(ansiReset)
	}
	return b.String()
}

func wrapVisible(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if visibleWidth(cur)+1+visibleWidth(w) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	return append(lines, cur)
}

func padRight(s string, w int) string {
	d := w - visibleWidth(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

func padLeft(s string, w int) string {
	d := w - visibleWidth(s)
	if d <= 0 {
		return s
	}
	return strings.Repeat(" ", d) + s
}

func normalizeSortKey(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "mean":
		return "mean"
	case "p95":
		return "p95"
	case "loss":
		return "loss"
	case "name":
		return "name"
	}
	return "median"
}

func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	h := s / 3600
	m := s % 3600 / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}
