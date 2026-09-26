package report

import (
	"io"

	"github.com/rknit/steward/internal/runner"
)

// Plain reports without escape codes: the line head at section start, the status when the section ends.
type Plain struct {
	W io.Writer
}

// SectionStart implements runner.Reporter.
func (p *Plain) SectionStart(s runner.Section) {
	io.WriteString(p.W, head(s.Project, s.Name)+" ... ")
}

// SectionEnd implements runner.Reporter.
func (p *Plain) SectionEnd(s runner.Section, out runner.Outcome) {
	io.WriteString(p.W, statusText(out)+"\n")
	writeContent(p.W, out)
}

// Blocked implements runner.Reporter.
func (p *Plain) Blocked(s runner.Section, failed []string) {
	io.WriteString(p.W, blockedLine(s.Project, s.Name, failed))
}
