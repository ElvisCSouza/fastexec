//go:build !windows

package transport

import (
	"fmt"
	"os/exec"
	"time"
)

type LocalJobMeta struct {
	ID            string     `json:"id"`
	Command       string     `json:"command,omitempty"`
	Argv          []string   `json:"argv,omitempty"`
	Cwd           string     `json:"cwd,omitempty"`
	PID           int        `json:"pid"`
	CreatedAt     time.Time  `json:"created_at"`
	JobObjectName string     `json:"job_object_name"`
	LogPath       string     `json:"log_path"`
	State         string     `json:"state"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
}

type LocalJobInfo struct {
	Meta         LocalJobMeta
	State        string
	Active       uint32
	LastActivity time.Time
	LogSize      int64
}

func handoffToJob(localSpec, time.Time, *exec.Cmd, *jobObject, string, *switchWriter, *switchWriter, <-chan error) (string, []byte, []byte, error) {
	return "", nil, nil, fmt.Errorf("handoff to a job is only available on Windows")
}

func LocalJobsDir() (string, error) {
	return "/tmp/fastexec_jobs", nil
}

func StartLocalJob(command string, argv []string, cwd, wantID string) (*LocalJobMeta, error) {
	return nil, fmt.Errorf("native local jobs are not available on this OS; use the daemon or systemd-run")
}

func LocalJobStatus(id string) (*LocalJobInfo, error) {
	return nil, fmt.Errorf("local job not found")
}

func LocalJobList() ([]LocalJobInfo, error) {
	return []LocalJobInfo{}, nil
}

func LocalJobStop(id string) (*LocalJobInfo, error) {
	return nil, fmt.Errorf("local job not found")
}

func LocalJobWait(id string, maxWait time.Duration) (*LocalJobInfo, bool, error) {
	return nil, false, fmt.Errorf("local job not found")
}

func TailLocalJob(id string, n int) (string, error) {
	return "", fmt.Errorf("local job not found")
}
