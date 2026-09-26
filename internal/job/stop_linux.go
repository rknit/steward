package job

import (
	"syscall"
	"unsafe"
)

// raiseStop sends sig to stew's group, stew included, and returns a function that undoes its setup once stew
// continues. The Go runtime keeps its own SIGTSTP handler even after signal.Reset, and that handler ignores SIGTSTP,
// so for the stop stew sets the kernel's default action and restores the runtime's handler afterwards.
func raiseStop(sig syscall.Signal) (restore func()) {
	if sig == syscall.SIGSTOP {
		syscall.Kill(0, sig)
		return func() {}
	}
	// Large enough for the kernel's struct sigaction on every Linux architecture.
	var dfl, old [4]uint64
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig),
		uintptr(unsafe.Pointer(&dfl)), uintptr(unsafe.Pointer(&old)), 8, 0, 0)
	if errno != 0 {
		syscall.Kill(0, sig)
		syscall.Kill(syscall.Getpid(), syscall.SIGSTOP)
		return func() {}
	}
	syscall.Kill(0, sig)
	return func() {
		syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&old)), 0, 8, 0, 0)
	}
}
