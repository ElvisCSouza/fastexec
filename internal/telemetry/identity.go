package telemetry

import (
	"runtime/debug"
	"strconv"
	"strings"
)

// Identity of the process and of the client that produced a call. Every value that cannot be known is written
// explicitly: "unknown" for text and -1 for integers. Nothing is backfilled for old rows (the column DEFAULT does it).

// Unknown is the sentinel for text fields that were not provided.
const Unknown = "unknown"

// UnknownPID is the sentinel for client_pid when the client did not send a pid.
const UnknownPID int64 = -1

var (
	buildVersion = Unknown // cli.Version (ldflags); set by the CLI at startup
	invokeMode   = Unknown // "proxy" (MCP over stdio), "daemon" (TCP worker), "cli" (direct command)
)

// SetBuildVersion records the FastExec version of this process (app_version).
func SetBuildVersion(v string) {
	if v = strings.TrimSpace(v); v != "" {
		buildVersion = v
	}
}

// SetInvokeMode records how this process was started (invoke_mode).
func SetInvokeMode(m string) {
	if m = strings.TrimSpace(m); m != "" {
		invokeMode = m
	}
}

// buildRevision returns the VCS revision of the binary (app_build): first 12 hex chars, plus "+dirty" when the
// working tree had changes at build time. "unknown" when the binary was built without VCS info.
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Unknown
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return Unknown
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// SplitCaller separates the proxy identity "name#pid" (see mcp caller). The name is everything before the LAST '#',
// so a name that contains '#' still parses. Without a valid pid, the pid is UnknownPID and the whole text is the name.
func SplitCaller(caller string) (name string, pid int64) {
	i := strings.LastIndex(caller, "#")
	if i < 0 {
		return textOrUnknown(caller), UnknownPID
	}
	n, err := strconv.ParseInt(caller[i+1:], 10, 64)
	if err != nil || n <= 0 {
		return textOrUnknown(caller), UnknownPID
	}
	return textOrUnknown(caller[:i]), n
}

func textOrUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}

// withIdentity fills the identity fields of an entry that the caller did not set. Values already set by the caller
// are kept; empty ones become the explicit sentinels.
func withIdentity(e AuditEntry) AuditEntry {
	if e.AppVersion == "" {
		e.AppVersion = buildVersion
	}
	if e.AppBuild == "" {
		e.AppBuild = buildRevision()
	}
	if e.InvokeMode == "" {
		e.InvokeMode = invokeMode
	}
	if e.ClientName == "" && e.ClientPID == 0 {
		e.ClientName, e.ClientPID = SplitCaller(e.Client)
		if e.Client == "" {
			e.ClientName, e.ClientPID = Unknown, UnknownPID
		}
	}
	if e.ClientVersion == "" {
		e.ClientVersion = Unknown
	}
	return e
}
