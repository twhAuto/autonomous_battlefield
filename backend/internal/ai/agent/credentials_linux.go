package agent

import "golang.org/x/sys/unix"

// ProtectCredentials prevents same-UID CLI children from reading the worker's
// environment/memory through procfs or ptrace. Call before starting any CLI. Linux
// protects in every environment, so production does not change the result.
func ProtectCredentials(production bool) error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
