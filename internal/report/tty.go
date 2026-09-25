package report

import (
	"io"
	"sync"
	"time"

	"github.com/rknit/steward/internal/runner"
)

// frameInterval is the progress animation's frame time.
const frameInterval = 300 * time.Millisecond

var frames = []string{".", "..", "..."}

// TTY reports to a terminal, animating the dots of the running phase's line.
type TTY struct {
	w      io.Writer
	ticker func() (<-chan time.Time, func()) // returns the tick channel and its stop function

	mu   sync.Mutex
	head string
	stop chan struct{}
	done chan struct{}
}

// NewTTY returns a TTY reporter that animates at frameInterval.
func NewTTY(w io.Writer) *TTY {
	return newTTY(w, func() (<-chan time.Time, func()) {
		t := time.NewTicker(frameInterval)
		return t.C, t.Stop
	})
}

func newTTY(w io.Writer, ticker func() (<-chan time.Time, func())) *TTY {
	return &TTY{w: w, ticker: ticker}
}

// redraw replaces the current line. Callers hold t.mu.
func (t *TTY) redraw(tail string) {
	io.WriteString(t.w, "\r\x1b[K"+t.head+tail)
}

// PhaseStart implements runner.Reporter.
func (t *TTY) PhaseStart(project string, ph runner.Phase) {
	t.mu.Lock()
	t.head = head(project, ph)
	t.redraw(" " + frames[0])
	t.mu.Unlock()

	tick, stopTicker := t.ticker()
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	go func(stop, done chan struct{}) {
		defer close(done)
		defer stopTicker()
		frame := 0
		for {
			select {
			case <-tick:
				frame = (frame + 1) % len(frames)
				t.mu.Lock()
				t.redraw(" " + frames[frame])
				t.mu.Unlock()
			case <-stop:
				return
			}
		}
	}(t.stop, t.done)
}

// PhaseEnd implements runner.Reporter.
func (t *TTY) PhaseEnd(project string, ph runner.Phase, out runner.Outcome) {
	close(t.stop)
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	t.redraw(" ... " + statusText(out) + "\n")
	writeContent(t.w, out)
}

// Blocked implements runner.Reporter.
func (t *TTY) Blocked(project string, ph runner.Phase, by []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	io.WriteString(t.w, blockedLine(project, ph, by))
}
