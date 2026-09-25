// Package report renders runner events: phase lines, the progress animation, failure content, the summary,
// and the stew runs show page.
package report

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rknit/steward/internal/runner"
)

// FormatDuration renders d as "10.5s" under a minute, otherwise "1m2s" or "1h5m12s".
// Rounding happens before choosing the form, so 59.96s renders as "1m0s".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if r := d.Round(100 * time.Millisecond); r < time.Minute {
		return fmt.Sprintf("%.1fs", r.Seconds())
	}
	d = d.Round(time.Second)
	h, m, s := d/time.Hour, d%time.Hour/time.Minute, d%time.Minute/time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}

// head is a phase line up to (not including) " ... ": "==> api: ci.pre-commit -> ci.quick".
func head(project string, ph runner.Phase) string {
	label := ph.Name
	if ph.Used != ph.Name {
		label += " -> " + ph.Used
	}
	return "==> " + project + ": " + label
}

// statusText is the part after " ... ": "done (1.2s)" or "interrupted".
func statusText(out runner.Outcome) string {
	switch out.Status {
	case runner.Done, runner.Pass, runner.Skip, runner.Fail:
		return string(out.Status) + " (" + FormatDuration(out.Duration) + ")"
	}
	return string(out.Status)
}

func blockedLine(project string, ph runner.Phase, by []string) string {
	return head(project, ph) + " ... blocked by " + strings.Join(by, ", ") + "\n"
}

// writeContent writes the content area below a fail or interrupted line.
func writeContent(w io.Writer, out runner.Outcome) {
	if out.Status != runner.Fail && out.Status != runner.Interrupted {
		return
	}
	var b bytes.Buffer
	for _, st := range out.Steps {
		b.WriteString("--- stew: " + st.Step + ": " + st.Cmd + "\n")
		b.Write(st.Output)
		if len(st.Output) > 0 && st.Output[len(st.Output)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	b.WriteString("(" + out.Cause + ")\n")
	w.Write(b.Bytes())
}
