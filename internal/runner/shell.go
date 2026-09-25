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

// Shell runs an argv (sh -c by default), stdin from /dev/null, each in its own session and process group,
// with no controlling terminal.
type Shell struct {
	// KillDelay is how long to wait after SIGTERM before SIGKILL, and how long to wait for output pipes
	// to close after the shell exits (a background process may keep them open). It must be greater than zero.
	KillDelay time.Duration
	// Force, when closed, SIGKILLs the running command's process group at once. stew closes it on a second stop
	// signal (Ctrl-C, SIGTERM, or SIGHUP) while it waits for an interrupted command. A nil Force never fires.
	Force <-chan struct{}
}

// Run implements Executor.
func (s Shell) Run(ctx context.Context, dir string, env []string, argv []string, stdout, stderr io.Writer) Result {
	if ctx.Err() != nil {
		return Result{Err: context.Cause(ctx)}
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	c.Stdout = stdout
	c.Stderr = stderr
	// c.Stdin stays nil: the command reads from /dev/null.
	// Setsid (not Setpgid) makes the command a session and process-group leader with no controlling
	// terminal: syscall.Kill(-pid, sig) still reaches the whole group, but opening /dev/tty fails with
	// ENXIO instead of stopping the command with SIGTTIN/SIGTTOU against stew's terminal.
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
		var i Interrupt
		stop := syscall.SIGTERM
		if errors.As(context.Cause(ctx), &i) {
			if i.Signal == syscall.SIGINT {
				syscall.Kill(group, syscall.SIGINT)
				select {
				case err = <-done:
				case <-s.Force:
					syscall.Kill(group, syscall.SIGKILL)
					err = <-done
				}
				break
			}
			stop = i.Signal
		}
		syscall.Kill(group, stop)
		select {
		case err = <-done:
		case <-time.After(s.KillDelay):
			syscall.Kill(group, syscall.SIGKILL)
			err = <-done
		case <-s.Force:
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
