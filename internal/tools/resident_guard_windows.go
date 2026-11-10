//go:build windows

package tools

import (
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type RunningProcess struct {
	PID       uint32
	Name      string
	StartTime time.Time
}

func findRunningProcessesByName(name string) []RunningProcess {
	target := strings.ToLower(name)
	if !strings.HasSuffix(target, ".exe") {
		target += ".exe"
	}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	err = windows.Process32First(snap, &entry)
	if err != nil {
		return nil
	}

	var found []RunningProcess
	for {
		procName := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(procName, target) {
			startTime := getProcessStartTime(entry.ProcessID)
			found = append(found, RunningProcess{
				PID:       entry.ProcessID,
				Name:      procName,
				StartTime: startTime,
			})
		}

		err = windows.Process32Next(snap, &entry)
		if err != nil {
			break
		}
	}
	return found
}

func getProcessStartTime(pid uint32) time.Time {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, creation.Nanoseconds())
}
