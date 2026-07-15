//go:build linux

package platform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func attachStartedTarget(pid int) (string, error) { return strconv.Itoa(pid), nil }

const (
	linuxProcRoot   = "/proc"
	linuxBootIDPath = linuxProcRoot + "/sys/kernel/random/boot_id"
)

func supportsPauseResume() bool { return true }

func applySupervisorConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func applyTargetConfiguration(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func inspectProcess(pid int) (ProcessIdentity, error) {
	stat, err := readLinuxProcessStat(pid)
	if err != nil {
		return ProcessIdentity{}, err
	}

	boot, err := os.ReadFile(linuxBootIDPath)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read boot identity: %w", err)
	}

	return ProcessIdentity{
		PID:      pid,
		Creation: stat.creation,
		Boot:     strings.TrimSpace(string(boot)),
	}, nil
}

type linuxProcessStat struct {
	state    string
	creation string
}

func readLinuxProcessStat(pid int) (linuxProcessStat, error) {
	statPath, err := procStatPath(pid)
	if err != nil {
		return linuxProcessStat{}, err
	}
	data, err := os.ReadFile(statPath)
	if err != nil {
		return linuxProcessStat{}, fmt.Errorf("read process stat: %w", err)
	}

	return parseLinuxProcessStat(data)
}

func parseLinuxProcessStat(data []byte) (linuxProcessStat, error) {
	closing := bytes.LastIndexByte(data, ')')
	if closing < 0 {
		return linuxProcessStat{}, errors.New("parse process stat: missing command terminator")
	}
	fields := strings.Fields(string(data[closing+1:]))
	if len(fields) <= 19 {
		return linuxProcessStat{}, errors.New("parse process stat: missing start time")
	}
	if len(fields[0]) != 1 {
		return linuxProcessStat{}, errors.New("parse process stat: invalid process state")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return linuxProcessStat{}, fmt.Errorf("parse process start time: %w", err)
	}

	return linuxProcessStat{state: fields[0], creation: fields[19]}, nil
}

func procStatPath(pid int) (string, error) {
	wanted := strconv.Itoa(pid)
	selfNamespace, err := os.Stat(linuxProcRoot + "/self/ns/pid")
	if err != nil {
		return "", fmt.Errorf("inspect current process namespace: %w", err)
	}
	directRoot := fmt.Sprintf("%s/%d", linuxProcRoot, pid)
	if linuxProcEntryMatches(directRoot, wanted, selfNamespace) {
		return directRoot + "/stat", nil
	}

	entries, err := os.ReadDir(linuxProcRoot)
	if err != nil {
		return "", fmt.Errorf("enumerate processes: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		root := linuxProcRoot + "/" + entry.Name()
		if linuxProcEntryMatches(root, wanted, selfNamespace) {
			return root + "/stat", nil
		}
	}

	return "", fmt.Errorf("read process stat: %w", os.ErrNotExist)
}

// linuxProcEntryMatches disambiguates namespace-local PIDs when /proc exposes
// entries from an ancestor PID namespace, as can happen in nested containers.
// Choosing a same-numbered process from another namespace would make process
// identity checks and signals unsafe.
func linuxProcEntryMatches(root, wanted string, selfNamespace os.FileInfo) bool {
	candidateNamespace, err := os.Stat(root + "/ns/pid")
	if err != nil || !os.SameFile(selfNamespace, candidateNamespace) {
		return false
	}
	status, err := os.ReadFile(root + "/status")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "NSpid:" {
			return fields[len(fields)-1] == wanted
		}
	}

	return false
}

func processAlive(identity ProcessIdentity) (bool, error) {
	err := syscall.Kill(identity.PID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false, fmt.Errorf("probe process: %w", err)
	}

	stat, err := readLinuxProcessStat(identity.PID)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect process state: %w", err)
	}
	if stat.creation != identity.Creation {
		return false, ErrIdentityMismatch
	}

	return stat.state != "Z" && stat.state != "X" && stat.state != "x", nil
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

func pauseProcess(identity ProcessIdentity) error {
	err := syscall.Kill(-identity.PID, syscall.SIGSTOP)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}

	return err
}

func resumeProcess(identity ProcessIdentity) error {
	err := syscall.Kill(-identity.PID, syscall.SIGCONT)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}

	return err
}

func isProcessGone(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
