package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
)

// pagerCommand returns the first non-empty of STEW_PAGER, PAGER, and "less". "cat" means no pager, returned as "".
func pagerCommand() string {
	for _, name := range []string{"STEW_PAGER", "PAGER"} {
		if v := os.Getenv(name); v != "" {
			if v == "cat" {
				return ""
			}
			return v
		}
	}
	return "less"
}

// page writes out through the pager when tty is set, and directly otherwise.
// Quitting the pager early is not an error. If the pager cannot run, out is written directly.
func page(stdout, stderr io.Writer, out []byte, tty bool) error {
	pager := pagerCommand()
	if !tty || pager == "" {
		_, err := stdout.Write(out)
		return err
	}

	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if _, ok := os.LookupEnv("LESS"); !ok {
		cmd.Env = append(cmd.Env, "LESS=FRX")
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		_, err := stdout.Write(out)
		return err
	}

	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	if err := cmd.Start(); err != nil {
		_, err := stdout.Write(out)
		return err
	}
	in.Write(out)
	in.Close()
	var exit *exec.ExitError
	if err := cmd.Wait(); errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127) {
		_, err := stdout.Write(out)
		return err
	}
	return nil
}
