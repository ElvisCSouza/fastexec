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
	ExitCode      *int       `json:"exit_code,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CancelAsked   bool       `json:"cancel_asked,omitempty"`
	Reason        string     `json:"reason,omitempty"`
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

func StartLocalJob(command string, argv []string, cwd, wantID string) (*LocalJobMeta, bool, error) {
	return nil, false, fmt.Errorf("native local jobs are not available on this OS; use the daemon or systemd-run")
}

func LocalJobStatus(id string) (*LocalJobInfo, error) {
	return nil, fmt.Errorf("local job not found")
}

func LocalJobList() ([]LocalJobInfo, error) {
	return []LocalJobInfo{}, nil
}

func LocalJobStop(id string) (*CancelReport, error) {
	return nil, fmt.Errorf("local job not found")
}

func LocalJobWait(id string, maxWait time.Duration) (*LocalJobInfo, bool, error) {
	return nil, false, fmt.Errorf("local job not found")
}

func TailLocalJob(id string, n int) (string, error) {
	return "", fmt.Errorf("local job not found")
}

func LocalJobLogLines(id string) (int, error) {
	return 0, fmt.Errorf("local job not found")
}

// CancelReport: veredito de cancelamento (ver localjob_windows.go).
type CancelReport struct {
	Verdict string
	Reason  string
	Info    *LocalJobInfo
}

// LocalJobLogChunk: leitura incremental do log (stub fora do Windows).
func LocalJobLogChunk(id string, cursor int64) (LogChunk, error) {
	return LogChunk{}, fmt.Errorf("local job not found")
}
