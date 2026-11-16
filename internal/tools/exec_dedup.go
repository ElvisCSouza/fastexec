package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"homelab/fastexec/internal/telemetry"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Exec output dedup. The command ALWAYS runs (it may have side effects, and its output may have changed); only the
// repeated text is withheld. When the same command (same identity, host, cwd, input) returns byte-identical output of
// meaningful size within execDedupTTL, the 2nd identical result is replaced by a short stub and the 3rd returns the
// full text again, so the agent is never locked out of content it needs. A different output always passes through.

const execDedupMinBytes = 3000

type execSeen struct {
	hash    string
	at      time.Time
	stubbed bool
}

var (
	execDedupMu   sync.Mutex
	execDedupSeen = map[string]*execSeen{}
)

// spillPathRe matches the spill file path that shapeBig embeds in a truncated output. The name carries a timestamp, so
// two runs with identical content would otherwise hash differently and the dedup would never fire on big outputs.
var spillPathRe = regexp.MustCompile(`\d{8}-\d{6}-[0-9a-f]{8}\.txt`)

// dedupFingerprintText is the text the dedup compares: the output with run-specific spill file names masked.
func dedupFingerprintText(out string) string {
	return spillPathRe.ReplaceAllString(out, "SPILL")
}

func execDedupOff() bool { return os.Getenv("FASTEXEC_NO_EXEC_DEDUP") == "1" }

// dedupExecOutput returns out unchanged, or a stub when out repeats the previous identical run (see above).
func dedupExecOutput(args ExecArgs, label, out string) string {
	if execDedupOff() || abOff("dedup", identity(args.Caller, args.Conv)) || len(out) < execDedupMinBytes {
		return out
	}
	sum := sha256.Sum256([]byte(dedupFingerprintText(out)))
	h := hex.EncodeToString(sum[:])
	in := sha256.Sum256([]byte(args.Input))
	key := strings.ToLower(identity(args.Caller, args.Conv)+"|"+args.Host+"|"+args.Cwd+"|"+label) + "|" + hex.EncodeToString(in[:4])
	now := time.Now()

	execDedupMu.Lock()
	defer execDedupMu.Unlock()
	if len(execDedupSeen) > 500 {
		execDedupSeen = map[string]*execSeen{}
	}
	e := execDedupSeen[key]
	if e == nil || e.hash != h {
		execDedupSeen[key] = &execSeen{hash: h, at: now}
		return out
	}
	prev := now.Sub(e.at)
	e.at = now
	if e.stubbed { // 3rd identical run: give the full text again and start over
		e.stubbed = false
		return out
	}
	e.stubbed = true
	stub := fmt.Sprintf("[FastExec] Identical output to the same command run %ds ago (%d lines, ~%d tokens, sha %s): not repeated, nothing changed. Use what you already have; repeat the call once more only if you need the full text again.",
		int(prev.Seconds()), strings.Count(out, "\n")+1, len(out)/4, h[:8])
	telemetry.NoteSavingDetail(telemetry.CatDedup, len(out), len(stub),
		fmt.Sprintf("identical to the same command run %ds ago: %d lines not repeated", int(prev.Seconds()), strings.Count(out, "\n")+1))
	return stub
}
