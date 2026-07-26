package ui

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 80 * time.Millisecond

// Spinner renders progress for a bounded concurrent task.
type Spinner struct {
	out   io.Writer
	isTTY bool
	label string
	total int

	count int64
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func NewSpinner(out io.Writer, isTTY bool, label string, total int) *Spinner {
	return &Spinner{out: out, isTTY: isTTY, label: label, total: total}
}

func (s *Spinner) Start() {
	if !s.isTTY {
		fmt.Fprintf(s.out, "%s...\n", s.label)
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.loop()
}

// Inc reports that one more unit of work has completed.
func (s *Spinner) Inc() { atomic.AddInt64(&s.count, 1) }

func (s *Spinner) loop() {
	defer close(s.done)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	frame := 0
	s.render(spinnerFrames[0])
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			frame++
			s.render(spinnerFrames[frame%len(spinnerFrames)])
		}
	}
}

func (s *Spinner) render(glyph string) {
	suffix := ""
	if s.total > 0 {
		suffix = fmt.Sprintf("  %d/%d", atomic.LoadInt64(&s.count), s.total)
	}
	fmt.Fprintf(s.out, "\r\x1b[K%s %s%s", Cyan(glyph), s.label, Gray(suffix))
}

// Stop halts the animation, clears the line on a TTY and prints finalMsg (if any).
func (s *Spinner) Stop(finalMsg string) {
	if s.isTTY {
		s.once.Do(func() {
			close(s.stop)
			<-s.done
			fmt.Fprint(s.out, "\r\x1b[K")
		})
	}
	if finalMsg != "" {
		fmt.Fprintln(s.out, finalMsg)
	}
}
