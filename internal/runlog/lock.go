package runlog

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// ErrRunning means another process holds the run's lock: the run is still in progress.
var ErrRunning = errors.New("run is in progress")

// lockDir opens dir and takes an exclusive flock on it without waiting. The lock lasts until the file is closed
// or the process exits. Go opens files close-on-exec, so commands a run starts never inherit it.
// A dir removed or replaced before the lock was taken is fs.ErrNotExist: the lock would belong to a dead inode.
// beforeFlock, when not nil, runs between the open and the lock; tests use it to act as a concurrent prune.
func lockDir(dir string, beforeFlock func(string)) (*os.File, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if beforeFlock != nil {
		beforeFlock(dir)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, err
	}
	locked, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	current, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !os.SameFile(locked, current) {
		f.Close()
		return nil, fs.ErrNotExist
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
