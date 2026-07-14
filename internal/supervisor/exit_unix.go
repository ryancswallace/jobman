//go:build !windows

package supervisor

import (
	"os/exec"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func processExitInfo(command *exec.Cmd, waitErr error, observedAt time.Time) *model.ExitInfo {
	information := &model.ExitInfo{ObservedAt: observedAt}
	if command.ProcessState != nil {
		if code := command.ProcessState.ExitCode(); code >= 0 {
			information.ExitCode = &code
		}
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			information.Signal = status.Signal().String()
		}
	}
	if information.ExitCode == nil && information.Signal == "" && waitErr != nil {
		information.PlatformReason = "process_wait_failed"
	}

	return information
}
