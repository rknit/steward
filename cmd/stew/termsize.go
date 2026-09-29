package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// terminalSize returns a function that reads f's terminal size in cells.
func terminalSize(f *os.File) func() (width, height int, ok bool) {
	return func() (int, int, bool) {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err != nil || ws.Col == 0 || ws.Row == 0 {
			return 0, 0, false
		}
		return int(ws.Col), int(ws.Row), true
	}
}
