//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// Darwin exposes these process states through extern_proc.P_stat but does not
// publish the constants through x/sys/unix. SZOMB is 5 in <sys/proc.h>.
const darwinProcessStateZombie int8 = 5

func attachStartedTarget(pid int) (string, error) { return strconv.Itoa(pid), nil }

func supportsPauseResume() bool { return true }

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

	return signalProcessGroup(identity, signal)
}

func pauseProcess(identity ProcessIdentity) error {
	return signalProcessGroup(identity, syscall.SIGSTOP)
}

func resumeProcess(identity ProcessIdentity) error {
	return signalProcessGroup(identity, syscall.SIGCONT)
}

func signalProcessGroup(identity ProcessIdentity, signal syscall.Signal) error {
	err := syscall.Kill(-identity.PID, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		zombie, inspectErr := originalProcessZombie(identity)
		if inspectErr == nil && zombie {
			return nil
		}
	}

	return err
}

// originalProcessZombie distinguishes Darwin's EPERM for an unsignalable
// zombie process group from a genuine authorization failure. It also verifies
// creation identity so PID reuse can never make an authorization error benign.
func originalProcessZombie(identity ProcessIdentity) (bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", identity.PID)
	if err != nil {
		if isProcessGone(err) {
			return false, nil
		}

		return false, err
	}
	creation := fmt.Sprintf(
		"%d.%06d",
		info.Proc.P_starttime.Sec,
		info.Proc.P_starttime.Usec,
	)
	if creation != identity.Creation {
		return false, ErrIdentityMismatch
	}

	return info.Proc.P_stat == darwinProcessStateZombie, nil
}

func isProcessGone(err error) bool {
	// kern.proc.pid reports EIO, rather than ESRCH, for a PID that disappeared
	// before SysctlKinfoProc could read it on supported macOS releases.
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EIO)
}
