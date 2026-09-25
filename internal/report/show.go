package report

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rknit/steward/internal/runner"
)

// Unfinished is the status of a phase that started but has no result: the run is still going or stew was killed.
const Unfinished runner.Status = "unfinished"

// ShownPhase is one phase on the stew runs show page.
type ShownPhase struct {
	Project   string
	Phase     runner.Phase   // Name and Used
	Outcome   runner.Outcome // Status, Duration, and Cause
	BlockedBy []string
	Log       []byte // the combined log; nil when the phase ran no command
}

// ShownSummary is the summary block of the stew runs show page.
type ShownSummary struct {
	Results  *runner.Results
	Total    time.Duration
	Finished bool
	Logs     string
}

// Show writes the stew runs show page: a header, each phase line with its whole log, then the summary if sum is set.
func Show(w io.Writer, id string, argv []string, phases []ShownPhase, sum *ShownSummary) {
	var b bytes.Buffer
	words := []string{"stew"}
	for _, arg := range argv {
		words = append(words, shellQuote(arg))
	}
	b.WriteString("run " + id + ": " + strings.Join(words, " ") + "\n")

	for _, p := range phases {
		if p.Outcome.Status == runner.Blocked {
			b.WriteString(blockedLine(p.Project, p.Phase, p.BlockedBy))
			continue
		}
		b.WriteString(head(p.Project, p.Phase) + " ... " + statusText(p.Outcome) + "\n")
		b.Write(p.Log)
		if len(p.Log) > 0 && p.Log[len(p.Log)-1] != '\n' {
			b.WriteByte('\n')
		}
		if p.Outcome.Status == runner.Fail || p.Outcome.Status == runner.Interrupted {
			b.WriteString("(" + p.Outcome.Cause + ")\n")
		}
	}

	if sum != nil {
		total := string(Unfinished)
		if sum.Finished {
			total = FormatDuration(sum.Total)
		}
		summary(&b, sum.Results, total, sum.Logs)
	}
	w.Write(b.Bytes())
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote returns s unchanged if the shell reads it as one plain word, otherwise single-quoted.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
