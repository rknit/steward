package runner

import "syscall"

// prSetChildSubreaper is PR_SET_CHILD_SUBREAPER from linux/prctl.h; package syscall does not define it.
const prSetChildSubreaper = 36

// AdoptOrphans makes stew the reaper of processes its commands leave behind, so stew can reap stopped leftovers
// itself instead of relying on PID 1, which does not reap in some containers.
func AdoptOrphans() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); errno != 0 {
		return errno
	}
	return nil
}
