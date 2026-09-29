package main

import (
	"testing"

	"github.com/rknit/steward/internal/workspace"
)

func TestJobLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name               string
		serial             bool
		config, flag, want int
	}{
		{"cpus", false, 0, 0, 6},
		{"config beats cpus", false, 3, 0, 3},
		{"flag beats config", false, 3, 2, 2},
		{"serial ignores flag and config", true, 3, 2, 1},
	} {
		ws := &workspace.Workspace{Serial: tc.serial, Jobs: tc.config}
		if got := jobLimit(ws, tc.flag, 6); got != tc.want {
			t.Errorf("%s: jobLimit = %d, want %d", tc.name, got, tc.want)
		}
	}
}
