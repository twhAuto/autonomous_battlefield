//go:build !linux

package runner

import "os/exec"

// Public CLI workers are supported on verified Linux VMs only.
func isolateCommand(cmd *exec.Cmd) {
	// Non-Linux development uses exec.CommandContext cancellation; Linux-only
	// process-group isolation is implemented in process_linux.go.
}
