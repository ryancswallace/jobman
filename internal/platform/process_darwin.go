//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func applySupervisorConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func applyTargetConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func inspectProcess(pid int) (ProcessIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("query process identity: %w", err)
	}
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("query boot identity: %w", err)
	}

	return ProcessIdentity{
		PID: pid,
		Creation: fmt.Sprintf(
			"%d.%06d",
			info.Proc.P_starttime.Sec,
			info.Proc.P_starttime.Usec,
		),
		Boot: fmt.Sprintf("%d.%06d", boot.Sec, boot.Usec),
	}, nil
}

func processAlive(identity ProcessIdentity) (bool, error) {
	err := syscall.Kill(identity.PID, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}

	return false, fmt.Errorf("probe process: %w", err)
}

func terminateProcess(identity ProcessIdentity, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}

	err := syscall.Kill(-identity.PID, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}

	return err
}

func isProcessGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}
