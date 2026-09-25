package report

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

// ProjectWrapper is one project's wrapper for the stew runs show header.
type ProjectWrapper struct {
	Project string
	Wrapper string
}

// Show writes the stew runs show page: a header, each phase line with its whole log, then the summary if sum is set.
func Show(w io.Writer, id string, argv []string, workspaceWrapper string, projectWrappers []ProjectWrapper,
	phases []ShownPhase, sum *ShownSummary) {
	var b bytes.Buffer
	b.WriteString("run " + id + ": " + Command(argv) + "\n")
	if workspaceWrapper != "" {
		b.WriteString("wrapper: " + displayWrapper(workspaceWrapper) + "\n")
	}
	for _, pw := range projectWrappers {
		b.WriteString("wrapper " + pw.Project + ": " + displayWrapper(pw.Wrapper) + "\n")
	}

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

// Command returns "stew" followed by argv, each argument shell-quoted when needed.
func Command(argv []string) string {
	words := []string{"stew"}
	for _, arg := range argv {
		words = append(words, shellQuote(arg))
	}
	return strings.Join(words, " ")
}

// displayWrapper keeps a wrapper on one line: verbatim, or shell-quoted if it has control characters or invalid UTF-8.
func displayWrapper(s string) string {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	return shellQuote(s)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote returns s unchanged if the shell reads it as one plain word, otherwise quoted.
// Control characters and invalid UTF-8 get $'…' quoting, so the result never spans lines or tabs.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	if !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl) {
		return ansiQuote(s)
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ansiQuote returns s as a bash $'…' string with control characters and invalid bytes escaped.
func ansiQuote(s string) string {
	var b strings.Builder
	b.WriteString("$'")
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == '\\' || r == '\'':
			b.WriteString(`\` + string(r))
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == utf8.RuneError && size == 1, unicode.IsControl(r):
			for _, c := range []byte(s[:size]) {
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		default:
			b.WriteString(s[:size])
		}
		s = s[size:]
	}
	b.WriteString("'")
	return b.String()
}
