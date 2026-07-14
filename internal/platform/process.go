// Package platform isolates operating-system process management.
package platform

import (
	"errors"
	"fmt"
	"os/exec"
)

// ErrIdentityMismatch means a PID no longer identifies the process Jobman
// originally observed.
var ErrIdentityMismatch = errors.New("process identity mismatch")

// ProcessIdentity is a PID plus platform creation and boot identity.
type ProcessIdentity struct {
	PID      int    `json:"pid"`
	Creation string `json:"creation"`
	Boot     string `json:"boot"`
}

// ConfigureSupervisor detaches a supervisor from the submitting terminal.
func ConfigureSupervisor(cmd *exec.Cmd) {
	applySupervisorConfiguration(cmd)
}

// ConfigureTarget creates a separately addressable target process tree.
func ConfigureTarget(cmd *exec.Cmd) {
	applyTargetConfiguration(cmd)
}

// Inspect returns the current identity for pid.
func Inspect(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("inspect process: invalid pid %d", pid)
	}

	return inspectProcess(pid)
}

// Alive reports whether identity still identifies a live process.
func Alive(identity ProcessIdentity) (bool, error) {
	current, err := Inspect(identity.PID)
	if err != nil {
		if isProcessGone(err) {
			return false, nil
		}

		return false, err
	}
	if current.Creation != identity.Creation || current.Boot != identity.Boot {
		return false, ErrIdentityMismatch
	}

	return processAlive(identity)
}

// Terminate sends a graceful or forced request to a verified process tree.
func Terminate(identity ProcessIdentity, force bool) error {
	alive, err := Alive(identity)
	if err != nil {
		return fmt.Errorf("verify process before termination: %w", err)
	}
	if !alive {
		return nil
	}

	if err := terminateProcess(identity, force); err != nil {
		return fmt.Errorf("terminate process tree %d: %w", identity.PID, err)
	}

	return nil
}
