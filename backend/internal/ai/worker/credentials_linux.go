package worker

import "golang.org/x/sys/unix"

// ProtectCredentials prevents same-UID CLI children from reading the worker's
// environment/memory through procfs or ptrace. Call before starting any CLI.
func ProtectCredentials() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
