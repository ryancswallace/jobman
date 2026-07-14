//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	maxWindowsProcessID          = uint64(1<<32 - 1)
	windowsStillActiveExitStatus = 259
)

func applySupervisorConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

func applyTargetConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func inspectProcess(pid int) (ProcessIdentity, error) {
	processID, err := windowsProcessID(pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("open process: %w", err)
	}

	var creation, exit, kernel, user windows.Filetime
	queryErr := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
	if queryErr != nil {
		queryErr = fmt.Errorf("query process times: %w", queryErr)
	}
	if err := finishProcessHandle(handle, queryErr); err != nil {
		return ProcessIdentity{}, err
	}

	return ProcessIdentity{
		PID:      pid,
		Creation: strconv.FormatInt(creation.Nanoseconds(), 10),
		Boot:     "windows",
	}, nil
}

func processAlive(identity ProcessIdentity) (bool, error) {
	processID, err := windowsProcessID(identity.PID)
	if err != nil {
		return false, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}

		return false, fmt.Errorf("open process: %w", err)
	}

	var code uint32
	queryErr := windows.GetExitCodeProcess(handle, &code)
	if queryErr != nil {
		queryErr = fmt.Errorf("query process exit code: %w", queryErr)
	}
	if err := finishProcessHandle(handle, queryErr); err != nil {
		return false, err
	}

	return code == windowsStillActiveExitStatus, nil
}

func terminateProcess(identity ProcessIdentity, _ bool) error {
	processID, err := windowsProcessID(identity.PID)
	if err != nil {
		return err
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		processID,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}

		return err
	}
	return finishProcessHandle(handle, windows.TerminateProcess(handle, 1))
}

func isProcessGone(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
}

func windowsProcessID(pid int) (uint32, error) {
	if pid <= 0 || uint64(pid) > maxWindowsProcessID {
		return 0, fmt.Errorf("convert process id: pid %d is outside the Windows process-id range", pid)
	}

	return uint32(pid), nil
}

func finishProcessHandle(handle windows.Handle, operationErr error) error {
	closeErr := windows.CloseHandle(handle)
	if closeErr != nil {
		closeErr = fmt.Errorf("close process handle: %w", closeErr)
	}

	return errors.Join(operationErr, closeErr)
}
