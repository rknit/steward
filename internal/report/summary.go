package report

import (
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rknit/steward/internal/runner"
)

// Summary writes the bordered results table, then the total run time and the logs directory.
func Summary(w io.Writer, res *runner.Results, total time.Duration, logs string) {
	summary(w, res, FormatDuration(total), logs)
}

// summary writes the table, then "total: <total>" and "logs: <logs>".
func summary(w io.Writer, res *runner.Results, total, logs string) {
	table := [][]string{append([]string{"project"}, res.Columns...)}
	for _, row := range res.Rows {
		line := []string{row.Project}
		for _, c := range row.Cells {
			line = append(line, cellText(c))
		}
		table = append(table, line)
	}

	var b strings.Builder
	Table(&b, table)
	b.WriteString("total: " + total + "\n")
	b.WriteString("logs: " + logs + "\n")
	io.WriteString(w, b.String())
}

// Table writes rows as a bordered table. The first row is the header.
func Table(w io.Writer, rows [][]string) {
	widths := make([]int, len(rows[0]))
	for _, line := range rows {
		for i, cell := range line {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}

	var b strings.Builder
	border := func(left, mid, right string) {
		b.WriteString(left)
		for i, wd := range widths {
			if i > 0 {
				b.WriteString(mid)
			}
			b.WriteString(strings.Repeat("─", wd+2))
		}
		b.WriteString(right + "\n")
	}
	row := func(line []string) {
		for i, cell := range line {
			b.WriteString("│ " + cell + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)) + " ")
		}
		b.WriteString("│\n")
	}

	border("┌", "┬", "┐")
	row(rows[0])
	border("├", "┼", "┤")
	for _, line := range rows[1:] {
		row(line)
	}
	border("└", "┴", "┘")
	io.WriteString(w, b.String())
}

func cellText(s runner.Status) string {
	if s == "" {
		return "-"
	}
	return string(s)
}
