//go:build !linux

package job

import "syscall"

// raiseStop sends sig to stew's group and stops stew with SIGSTOP: the Go runtime's SIGTSTP handler ignores SIGTSTP.
func raiseStop(sig syscall.Signal) (restore func()) {
	syscall.Kill(0, sig)
	if sig != syscall.SIGSTOP {
		syscall.Kill(syscall.Getpid(), syscall.SIGSTOP)
	}
	return func() {}
}
