//go:build windows

// runjob: runs a command inside a Windows Job Object and reports what the whole process tree consumed.
// usage: runjob -m metrics.json -- program args...
// The child starts suspended, is put in the job, then resumed, so no descendant can escape the accounting.
// Output (metrics.json): cpu_user_s, cpu_kernel_s, cpu_total_s, peak_job_mem_mb (commit, all processes together),
// peak_process_mem_mb (largest single process), processes (total ever started), wall_s, exit_code.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which x/sys/windows does not define.
type basicAccounting struct {
	TotalUserTime, TotalKernelTime                       int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime   int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses uint32
	TotalTerminatedProcesses                             uint32
}

const jobObjectBasicAccountingInformation = 1

func main() {
	args := os.Args[1:]
	metrics := ""
	for len(args) > 0 && args[0] != "--" {
		if args[0] == "-m" && len(args) > 1 {
			metrics = args[1]
			args = args[2:]
			continue
		}
		fmt.Fprintln(os.Stderr, "usage: runjob -m metrics.json -- program args...")
		os.Exit(2)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: runjob -m metrics.json -- program args...")
		os.Exit(2)
	}
	prog := args[1]
	rest := args[2:]
	path, err := exec.LookPath(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runjob:", err)
		os.Exit(127)
	}
	argv := append([]string{path}, rest...)
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".cmd" || ext == ".bat" {
		argv = append([]string{"cmd.exe", "/c"}, argv...)
		path, _ = exec.LookPath("cmd.exe")
	}

	job, err := windows.CreateJobObject(nil, nil)
	must(err, "CreateJobObject")
	ext := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	ext.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)))
	must(err, "SetInformationJobObject")

	si := windows.StartupInfo{Flags: windows.STARTF_USESTDHANDLES}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.StdInput, _ = windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	si.StdOutput, _ = windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	si.StdErr, _ = windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	for _, h := range []windows.Handle{si.StdInput, si.StdOutput, si.StdErr} {
		if h != 0 && h != windows.InvalidHandle {
			_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
		}
	}
	var pi windows.ProcessInformation
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	must(err, "cmdline")
	app, err := windows.UTF16PtrFromString(path)
	must(err, "path")
	t0 := time.Now()
	err = windows.CreateProcess(app, cmdline, nil, nil, true, windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT, nil, nil, &si, &pi)
	must(err, "CreateProcess")
	must(windows.AssignProcessToJobObject(job, pi.Process), "AssignProcessToJobObject")
	_, err = windows.ResumeThread(pi.Thread)
	must(err, "ResumeThread")
	windows.CloseHandle(pi.Thread)
	_, err = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
	must(err, "Wait")
	wall := time.Since(t0)
	var code uint32
	_ = windows.GetExitCodeProcess(pi.Process, &code)

	var acc basicAccounting
	err = windows.QueryInformationJobObject(job, jobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&acc)), uint32(unsafe.Sizeof(acc)), nil)
	must(err, "QueryInformationJobObject basic")
	err = windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil)
	must(err, "QueryInformationJobObject extended")

	sec := func(v int64) float64 { return float64(v) / 1e7 } // 100 ns units
	out := map[string]any{
		"cpu_user_s":          sec(acc.TotalUserTime),
		"cpu_kernel_s":        sec(acc.TotalKernelTime),
		"cpu_total_s":         sec(acc.TotalUserTime + acc.TotalKernelTime),
		"peak_job_mem_mb":     float64(ext.PeakJobMemoryUsed) / (1 << 20),
		"peak_process_mem_mb": float64(ext.PeakProcessMemoryUsed) / (1 << 20),
		"processes":           acc.TotalProcesses,
		"wall_s":              wall.Seconds(),
		"exit_code":           code,
	}
	if metrics != "" {
		b, _ := json.Marshal(out)
		_ = os.WriteFile(metrics, b, 0o644)
	}
	os.Exit(int(code))
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "runjob: %s: %v\n", what, err)
		os.Exit(1)
	}
}
