package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rknit/steward/internal/runner"
)

// frameInterval is the progress animation's frame time.
const frameInterval = 300 * time.Millisecond

var frames = []string{".", "..", "..."}

// TTY reports to a terminal. Finished lines scroll up; below them a footer shows every running section with
// animated dots.
type TTY struct {
	w      io.Writer
	ticker func() (<-chan time.Time, func()) // returns the tick channel and its stop function
	size   func() (width, height int, ok bool)

	mu      sync.Mutex
	running []string // footer heads, in start order
	drawn   int      // footer lines on screen
	frame   int
	stop    chan struct{}
	done    chan struct{}
}

// NewTTY returns a TTY reporter that animates at frameInterval and fits its footer to size.
func NewTTY(w io.Writer, size func() (width, height int, ok bool)) *TTY {
	return newTTY(w, func() (<-chan time.Time, func()) {
		t := time.NewTicker(frameInterval)
		return t.C, t.Stop
	}, size)
}

func newTTY(w io.Writer, ticker func() (<-chan time.Time, func()), size func() (width, height int, ok bool)) *TTY {
	return &TTY{w: w, ticker: ticker, size: size}
}

// SectionStart implements runner.Reporter.
func (t *TTY) SectionStart(s runner.Section) {
	t.mu.Lock()
	t.running = append(t.running, head(s.Project, s.Name))
	first := len(t.running) == 1
	if first {
		t.frame = 0
	}
	t.redraw("")
	t.mu.Unlock()
	if first {
		t.animate()
	}
}

// SectionEnd implements runner.Reporter.
func (t *TTY) SectionEnd(s runner.Section, out runner.Outcome) {
	h := head(s.Project, s.Name)
	t.mu.Lock()
	last := len(t.running) == 1 && t.running[0] == h
	t.mu.Unlock()
	if last {
		close(t.stop)
		<-t.done
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = slices.DeleteFunc(t.running, func(r string) bool { return r == h })
	t.redraw(h + " ... " + statusText(out) + "\n" + string(content(out)))
}

// Blocked implements runner.Reporter.
func (t *TTY) Blocked(s runner.Section, failed []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.redraw(blockedLine(s.Project, s.Name, failed))
}

// animate advances the dots on every tick until t.stop closes.
func (t *TTY) animate() {
	tick, stopTicker := t.ticker()
	t.stop, t.done = make(chan struct{}), make(chan struct{})
	go func(stop, done chan struct{}) {
		defer close(done)
		defer stopTicker()
		for {
			select {
			case <-tick:
				t.mu.Lock()
				t.frame = (t.frame + 1) % len(frames)
				t.redraw("")
				t.mu.Unlock()
			case <-stop:
				return
			}
		}
	}(t.stop, t.done)
}

// redraw erases the footer, writes finished above where it was, and draws the footer again. Callers hold t.mu.
func (t *TTY) redraw(finished string) {
	var b strings.Builder
	b.WriteString("\r")
	if t.drawn > 1 {
		fmt.Fprintf(&b, "\x1b[%dA", t.drawn-1)
	}
	b.WriteString("\x1b[J")
	b.WriteString(finished)
	lines := t.footer()
	b.WriteString(strings.Join(lines, "\n"))
	t.drawn = len(lines)
	io.WriteString(t.w, b.String())
}

// footer returns the footer lines, cut to the terminal width and height so none wraps or scrolls.
func (t *TTY) footer() []string {
	lines := make([]string, len(t.running))
	for i, h := range t.running {
		lines[i] = h + " " + frames[t.frame]
	}
	width, height, ok := t.size()
	if !ok {
		return lines
	}
	if room := max(height-1, 1); len(lines) > room {
		lines = append(lines[:room-1], fmt.Sprintf("... %d more running", len(lines)-(room-1)))
	}
	for i, l := range lines {
		if len(l) > width-1 {
			lines[i] = l[:max(width-1, 0)]
		}
	}
	return lines
}
