// Package job runs one command as a job of its own, the way an interactive shell runs a foreground job.
package job

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

// passed are the signals stew passes to the job. Terminal signals reach the job without stew when the job owns
// the terminal, so any of these that stew gets was meant for stew alone, and the job gets it exactly once.
var passed = []os.Signal{
	syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP,
	syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGTSTP,
}

// stopGrace bounds the wait for SIGCONT after stew stops its group. The kernel discards stop signals to an
// orphaned process group, and then no SIGCONT comes.
const stopGrace = time.Second

// Run starts argv in dir as the leader of a new process group, with stew's stdin, stdout, and stderr, and env as its
// whole environment. It waits for the job to exit and returns its wait status.
//
// When stew's group is the terminal's foreground group, the job's group takes the terminal, so Ctrl-C, Ctrl-\,
// Ctrl-Z, and a hangup reach only the job. When the job stops, stew takes the terminal back and stops its own
// group with it. When stew continues, it gives the terminal back if its group is in the foreground, and continues
// the job.
func Run(dir string, argv, env []string) (syscall.WaitStatus, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		tty = nil
	} else {
		defer tty.Close()
	}
	return run(tty, dir, argv, env, func(pgid int, sig syscall.Signal) { syscall.Kill(-pgid, sig) })
}

// run is Run with stew's controlling terminal tty, nil for none, and pass sending each signal stew gets on to the
// job's group.
func run(
	tty *os.File, dir string, argv, env []string, pass func(pgid int, sig syscall.Signal),
) (syscall.WaitStatus, error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return 0, err
	}

	sigs := make(chan os.Signal, len(passed))
	signal.Notify(sigs, passed...)
	defer signal.Stop(sigs)
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)

	attr := &syscall.SysProcAttr{Setpgid: true}
	if foreground(tty) {
		attr.Foreground, attr.Ctty = true, int(tty.Fd())
	}
	p, err := os.StartProcess(path, argv, &os.ProcAttr{
		Dir:   dir,
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   attr,
	})
	if err != nil {
		return 0, err
	}
	defer p.Release()
	pgid := p.Pid
	if tty != nil {
		// stew moves the terminal between groups while its own group may be in the background.
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
		defer takeTerminal(tty, pgid)
	}

	waits := make(chan waited, 1)
	go wait(p.Pid, waits)
	for {
		select {
		case sig := <-sigs:
			pass(pgid, sig.(syscall.Signal))
		case w := <-waits:
			if w.err != nil {
				return 0, w.err
			}
			if !w.status.Stopped() {
				return w.status, nil
			}
			takeTerminal(tty, pgid)
			stopGroup(w.status.StopSignal(), cont)
			if foreground(tty) {
				setForeground(tty, pgid)
			}
			syscall.Kill(-pgid, syscall.SIGCONT)
			go wait(p.Pid, waits)
		}
	}
}

type waited struct {
	status syscall.WaitStatus
	err    error
}

// wait reports the next time pid stops or exits.
func wait(pid int, waits chan<- waited) {
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil)
		if !errors.Is(err, syscall.EINTR) {
			waits <- waited{ws, err}
			return
		}
	}
}

// stopGroup stops stew's group the way the job stopped, as a Ctrl-Z would have stopped a job holding both,
// and returns once the group continues.
func stopGroup(jobSig syscall.Signal, cont chan os.Signal) {
	sig := syscall.SIGTSTP
	if jobSig == syscall.SIGSTOP {
		sig = syscall.SIGSTOP
	}
	for len(cont) > 0 {
		<-cont
	}
	restore := raiseStop(sig)
	defer restore()
	select {
	case <-cont:
	case <-time.After(stopGrace):
	}
}

// foreground reports whether stew's group is the foreground group of tty.
func foreground(tty *os.File) bool {
	if tty == nil {
		return false
	}
	pgid, err := terminalGroup(tty)
	return err == nil && pgid == syscall.Getpgrp()
}

// takeTerminal gives tty back to stew's group when the job's group holds it.
func takeTerminal(tty *os.File, job int) {
	if tty == nil {
		return
	}
	if pgid, err := terminalGroup(tty); err == nil && pgid == job {
		setForeground(tty, syscall.Getpgrp())
	}
}

func terminalGroup(tty *os.File) (int, error) {
	var pgid int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&pgid))); errno != 0 {
		return 0, errno
	}
	return int(pgid), nil
}

func setForeground(tty *os.File, pgid int) {
	id := int32(pgid)
	syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&id)))
}
