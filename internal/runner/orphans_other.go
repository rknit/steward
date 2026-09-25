//go:build !linux

package runner

// AdoptOrphans does nothing outside Linux, which has no child subreaper. PID 1 reaps stopped leftovers there.
func AdoptOrphans() error { return nil }
