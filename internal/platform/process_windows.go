//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	maxWindowsProcessID          = uint64(1<<32 - 1)
	windowsStillActiveExitStatus = 259
	jobObjectTerminateAccess     = 0x0008
	jobObjectQueryAccess         = 0x0004
	maximumJobProcesses          = 4096
)

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	openJobObject    = kernel32.NewProc("OpenJobObjectW")
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

func supportsPauseResume() bool { return true }

func applySupervisorConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

func applyTargetConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED,
		HideWindow:    true,
	}
}

func attachStartedTarget(pid int) (tree string, returnedErr error) {
	identity, err := inspectProcess(pid)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf(`Local\jobman-%d-%s`, pid, identity.Creation)
	encodedName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", fmt.Errorf("encode job-object name: %w", err)
	}
	job, err := windows.CreateJobObject(nil, encodedName)
	if err != nil {
		return "", fmt.Errorf("create target job object: %w", err)
	}
	defer func() {
		returnedErr = errors.Join(returnedErr, windows.CloseHandle(job))
	}()
	processID, err := windowsProcessID(pid)
	if err != nil {
		return "", err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		processID,
	)
	if err != nil {
		return "", fmt.Errorf("open suspended target: %w", err)
	}
	defer func() {
		returnedErr = errors.Join(returnedErr, windows.CloseHandle(process))
	}()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return "", fmt.Errorf("assign target to job object: %w", err)
	}
	if err := resumeInitialThread(processID); err != nil {
		return "", err
	}

	return name, nil
}

func resumeInitialThread(processID uint32) (returnedErr error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot target threads: %w", err)
	}
	defer func() { returnedErr = errors.Join(returnedErr, windows.CloseHandle(snapshot)) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return fmt.Errorf("enumerate target threads: %w", err)
	}
	for {
		if entry.OwnerProcessID == processID {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return fmt.Errorf("open suspended target thread: %w", openErr)
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil || closeErr != nil {
				return errors.Join(
					fmt.Errorf("resume suspended target thread: %w", resumeErr),
					closeErr,
				)
			}

			return nil
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return fmt.Errorf("enumerate target threads: %w", err)
		}
	}

	return errors.New("target has no resumable thread")
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
		Boot:     windowsBootIdentity(),
	}, nil
}

func windowsBootIdentity() string {
	type systemTimeOfDayInformation struct {
		BootTime     int64
		CurrentTime  int64
		TimeZoneBias int64
		TimeZoneID   uint32
		Reserved     uint32
		BootBias     uint64
		SleepBias    uint64
	}
	information := systemTimeOfDayInformation{}
	if err := windows.NtQuerySystemInformation(
		windows.SystemTimeOfDayInformation,
		unsafe.Pointer(&information),
		uint32(unsafe.Sizeof(information)),
		nil,
	); err != nil {
		// DurationSinceBoot is less precise but remains stable at one-second
		// resolution on systems where this information class is unavailable.
		return strconv.FormatInt(time.Now().Add(-windows.DurationSinceBoot()).Unix(), 10)
	}

	return strconv.FormatInt(information.BootTime, 10)
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

func terminateProcess(identity ProcessIdentity, force bool) error {
	if !force {
		if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(identity.PID)); err == nil {
			return nil
		}
		// A detached supervisor might not share a console with the target. In
		// that case preserve the configured grace window and let the forced
		// job-object termination perform the guaranteed tree-wide stop.
		return nil
	}
	if identity.Tree != "" {
		job, err := openNamedJobObject(identity.Tree, jobObjectTerminateAccess)
		if err == nil {
			return finishProcessHandle(job, windows.TerminateJobObject(job, 1))
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return err
		}
	}
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

func pauseProcess(identity ProcessIdentity) error {
	return applyToJobProcesses(identity, ntSuspendProcess)
}

func resumeProcess(identity ProcessIdentity) error {
	return applyToJobProcesses(identity, ntResumeProcess)
}

func openNamedJobObject(name string, access uintptr) (windows.Handle, error) {
	encoded, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	handle, _, callErr := openJobObject.Call(access, 0, uintptr(unsafe.Pointer(encoded)))
	if handle == 0 {
		return 0, callErr
	}

	return windows.Handle(handle), nil
}

func applyToJobProcesses(identity ProcessIdentity, procedure *windows.LazyProc) (returnedErr error) {
	if identity.Tree == "" {
		return ErrUnsupported
	}
	job, err := openNamedJobObject(identity.Tree, jobObjectQueryAccess)
	if err != nil {
		return err
	}
	defer func() { returnedErr = errors.Join(returnedErr, windows.CloseHandle(job)) }()
	processes, err := jobProcessIDs(job)
	if err != nil {
		return err
	}
	for _, processID := range processes {
		process, openErr := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, processID)
		if openErr != nil {
			if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
				continue
			}
			return openErr
		}
		status, _, _ := procedure.Call(uintptr(process))
		closeErr := windows.CloseHandle(process)
		if status != 0 || closeErr != nil {
			return errors.Join(fmt.Errorf("change target process suspension: NTSTATUS %#x", status), closeErr)
		}
	}

	return nil
}

func jobProcessIDs(job windows.Handle) ([]uint32, error) {
	buffer := make([]byte, 8+maximumJobProcesses*int(unsafe.Sizeof(uintptr(0))))
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&buffer[0])),
		uint32(len(buffer)), //nolint:gosec // Bounded constant-sized buffer.
		nil,
	); err != nil {
		return nil, err
	}
	count := *(*uint32)(unsafe.Pointer(&buffer[4]))
	if count > maximumJobProcesses {
		return nil, fmt.Errorf("target job contains %d processes, maximum is %d", count, maximumJobProcesses)
	}
	values := unsafe.Slice((*uintptr)(unsafe.Pointer(&buffer[8])), int(count))
	result := make([]uint32, 0, count)
	for _, value := range values {
		if uint64(value) > maxWindowsProcessID {
			return nil, fmt.Errorf("target job contains invalid process id %d", value)
		}
		result = append(result, uint32(value))
	}

	return result, nil
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
