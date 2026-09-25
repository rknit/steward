package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Shell runs commands with `sh -c`, stdin from /dev/null, each in its own process group.
type Shell struct {
	// KillDelay is how long to wait after SIGTERM before SIGKILL, and how long to wait for output pipes
	// to close after the shell exits (a background process may keep them open).
	KillDelay time.Duration
}

// Run implements Executor.
func (s Shell) Run(ctx context.Context, dir, cmd string, stdout, stderr io.Writer) Result {
	if ctx.Err() != nil {
		return Result{Err: context.Cause(ctx)}
	}
	c := exec.Command("sh", "-c", cmd)
	c.Dir = dir
	c.Stdout = stdout
	c.Stderr = stderr
	// c.Stdin stays nil: the command reads from /dev/null.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = s.KillDelay
	if err := c.Start(); err != nil {
		return Result{Err: err}
	}

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		group := -c.Process.Pid
		if errors.Is(context.Cause(ctx), ErrInterrupted) {
			syscall.Kill(group, syscall.SIGINT)
			err = <-done
			break
		}
		syscall.Kill(group, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(s.KillDelay):
			syscall.Kill(group, syscall.SIGKILL)
			err = <-done
		}
	}
	return resultOf(c.ProcessState, err)
}

func resultOf(state *os.ProcessState, err error) Result {
	if state == nil {
		return Result{Err: err}
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return Result{Signal: signalName(ws.Signal())}
	}
	return Result{ExitCode: state.ExitCode()}
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGABRT: "SIGABRT", syscall.SIGALRM: "SIGALRM", syscall.SIGBUS: "SIGBUS",
	syscall.SIGFPE: "SIGFPE", syscall.SIGHUP: "SIGHUP", syscall.SIGILL: "SIGILL",
	syscall.SIGINT: "SIGINT", syscall.SIGKILL: "SIGKILL", syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGQUIT: "SIGQUIT", syscall.SIGSEGV: "SIGSEGV", syscall.SIGTERM: "SIGTERM",
	syscall.SIGUSR1: "SIGUSR1", syscall.SIGUSR2: "SIGUSR2",
}

func signalName(sig syscall.Signal) string {
	if name, ok := signalNames[sig]; ok {
		return name
	}
	return strconv.Itoa(int(sig))
}
