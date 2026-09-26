package main

import (
	"strings"
	"testing"

	"github.com/rknit/steward/internal/trust"
)

func TestTrustTable(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	writeTrustTable(&b, []trust.Entry{
		{Name: "workspace", Command: "direnv allow ."},
		{Name: "core", Path: "libs/core", Command: "a\nb"},
	})
	want := "┌───────────┬────────────────┐\n" +
		"│ trust     │ command        │\n" +
		"├───────────┼────────────────┤\n" +
		"│ workspace │ direnv allow . │\n" +
		"│ core      │ $'a\\nb'        │\n" +
		"└───────────┴────────────────┘\n"
	if got := b.String(); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}
