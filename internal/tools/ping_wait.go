package tools

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A ping used as a wait is a loopback ping with more than 15 probes whose output is thrown away (>nul, >/dev/null,
// | Out-Null). Nothing reads its output, so it can run as a background job without losing anything the model would
// see. Every other ping (remote target, short count, visible output, unknown flags) is a diagnostic and stays inline.
//
// The match is deliberately narrow: a missed wait costs a blocked call (the old behaviour), a wrong promotion hides
// a diagnostic. When in doubt, the command is not promoted.

// pingWaitRe matches a command that starts with ping and whose ping segment ends in a stdout suppressor, followed by
// the end of the line or a chain operator (&&, ||, &, ;). Group 1 is the ping arguments (lazy, so it stops before the
// suppressor) and is checked token by token below. The destination is a whole token, so "127.0.0.11>nul" yields the
// token "127.0.0.11", which is not loopback, and is rejected.
var pingWaitRe = regexp.MustCompile(`(?i)^\s*ping(?:\.exe)?\s+(.*?)\s*(?:>>?\s*(?:nul|/dev/null|\$null)(?:\s+2>(?:&1|nul|/dev/null))?|\|\s*out-null)\s*(?:$|&&|\|\||&|;)`)

// pingLoopback are the only destinations a promoted ping may have: nothing leaves the host.
var pingLoopback = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// pingCountFlags: -n (Windows, also /n) and -c (Linux). The count is what makes the wait long.
var pingCountFlags = map[string]bool{"-n": true, "-c": true, "/n": true}

// pingMinProbes is the exclusive lower bound: a count of 15 or less is a short check, not a wait.
const pingMinProbes = 15

// pingWaitScript returns the shell line a ping wait would run, or false when the call is not one. Without a shell
// there is no way to suppress output, so a bare ping argv is never a wait. A shell argv ("cmd /c <line>",
// "bash -c <line>", "powershell -Command <line>") carries its line as the last argument and is checked the same way.
func pingWaitScript(args ExecArgs) (string, bool) {
	if len(args.Argv) == 0 {
		return stripHeredocs(args.Command), true
	}
	if len(args.Argv) != 3 {
		return "", false
	}
	exe := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
	if !shellExes[exe] {
		return "", false
	}
	switch strings.ToLower(args.Argv[1]) {
	case "/c", "-c", "-command":
		return args.Argv[2], true
	}
	return "", false
}

// parsePingWait reports the probe count of a promotable ping line, or false. The line must be exactly the ping
// flags and one loopback destination, in either order.
func parsePingWait(line string) (int, bool) {
	m := pingWaitRe.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	t := strings.Fields(m[1])
	if len(t) != 3 {
		return 0, false
	}
	var count string
	switch {
	case pingCountFlags[strings.ToLower(t[0])] && isProbeCount(t[1]) && pingLoopback[strings.ToLower(t[2])]:
		count = t[1]
	case pingLoopback[strings.ToLower(t[0])] && pingCountFlags[strings.ToLower(t[1])] && isProbeCount(t[2]):
		count = t[2]
	default:
		return 0, false
	}
	n, err := strconv.Atoi(count)
	if err != nil || n <= pingMinProbes {
		return 0, false
	}
	return n, true
}

func isProbeCount(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// tryPromotePingWait promotes a silent loopback ping longer than 15 probes to a background job, the same way a long
// pure pause is promoted (same command, cwd and host; the job runs it, nothing is retried). Other pings are not
// handled here.
func tryPromotePingWait(args ExecArgs) (string, bool, error) {
	if !autofixOn(identity(args.Caller, args.Conv)) {
		return "", false, nil
	}
	line, ok := pingWaitScript(args)
	if !ok {
		return "", false, nil
	}
	n, ok := parsePingWait(strings.TrimSpace(line))
	if !ok {
		return "", false, nil
	}
	start := JobArgs{Host: args.Host, Action: "start", Command: args.Command, Argv: args.Argv, Cwd: args.Cwd, Caller: args.Caller}
	out, err := promoteSleepJob(start)
	if err != nil {
		return "", true, err
	}
	notice := "[FastExec Auto-Remediation] A ping wait of " + strconv.Itoa(n) + " probes with its output suppressed was promoted to a background job, so the call returns now. Next: job(action='wait', job_id=...) blocks up to 45 s per call; job(action='status') or tail checks progress."
	return notice + "\n" + out, true, nil
}
