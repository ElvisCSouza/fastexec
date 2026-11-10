//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RunningProcess struct {
	PID       uint32
	Name      string
	StartTime time.Time
}

func findRunningProcessesByName(name string) []RunningProcess {
	target := strings.ToLower(name)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	var found []RunningProcess
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}

		commPath := filepath.Join("/proc", e.Name(), "comm")
		commBytes, err := os.ReadFile(commPath)
		if err != nil {
			continue
		}
		procName := strings.TrimSpace(string(commBytes))
		if strings.EqualFold(procName, target) {
			info, err := e.Info()
			var startTime time.Time
			if err == nil {
				startTime = info.ModTime()
			}
			found = append(found, RunningProcess{
				PID:       uint32(pid),
				Name:      procName,
				StartTime: startTime,
			})
		}
	}
	return found
}
