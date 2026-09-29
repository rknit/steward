package report

import (
	"io"

	"github.com/rknit/steward/internal/runner"
)

// Plain reports without escape codes: a started line when a section starts, and its end line when it ends.
type Plain struct {
	W io.Writer
}

// SectionStart implements runner.Reporter.
func (p *Plain) SectionStart(s runner.Section) {
	io.WriteString(p.W, head(s.Project, s.Name)+" ... started\n")
}

// SectionEnd implements runner.Reporter.
func (p *Plain) SectionEnd(s runner.Section, out runner.Outcome) {
	line := head(s.Project, s.Name) + " ... " + statusText(out) + "\n"
	p.W.Write(append([]byte(line), content(out)...))
}

// Blocked implements runner.Reporter.
func (p *Plain) Blocked(s runner.Section, failed []string) {
	io.WriteString(p.W, blockedLine(s.Project, s.Name, failed))
}
