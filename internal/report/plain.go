package report

import (
	"io"

	"github.com/rknit/steward/internal/runner"
)

// Plain reports without escape codes: the line head at phase start, the status when the phase ends.
type Plain struct {
	W io.Writer
}

// PhaseStart implements runner.Reporter.
func (p *Plain) PhaseStart(project string, ph runner.Phase) {
	io.WriteString(p.W, head(project, ph)+" ... ")
}

// PhaseEnd implements runner.Reporter.
func (p *Plain) PhaseEnd(project string, ph runner.Phase, out runner.Outcome) {
	io.WriteString(p.W, statusText(out)+"\n")
	writeContent(p.W, out)
}

// Blocked implements runner.Reporter.
func (p *Plain) Blocked(project string, ph runner.Phase, by []string) {
	io.WriteString(p.W, blockedLine(project, ph, by))
}
