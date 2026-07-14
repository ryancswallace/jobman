//go:build windows

package supervisor

import (
	"os/exec"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func processExitInfo(command *exec.Cmd, waitErr error, observedAt time.Time) *model.ExitInfo {
	information := &model.ExitInfo{ObservedAt: observedAt}
	if command.ProcessState != nil {
		if code := command.ProcessState.ExitCode(); code >= 0 {
			information.ExitCode = &code
		}
	}
	if information.ExitCode == nil && waitErr != nil {
		information.PlatformReason = "process_wait_failed"
	}

	return information
}
