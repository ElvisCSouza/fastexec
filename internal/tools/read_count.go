package tools

import (
	"strings"
	"sync"
	"time"
)

// Per-channel counters. The key includes the MCP channel ("_caller":
// clientInfo.name#pid), so two different clients never trigger each other's
// hints. Inside one channel (e.g. several Claude Desktop conversations) a hint
// may rarely reach the neighbour conversation; it is only a hint.

// rereadHintAt is the read number (per file, per channel) that triggers the
// graph hint. Only that exact read gets it, so it is never repeated.
const rereadHintAt = 3

var (
	readCountMu sync.Mutex
	readCounts  = map[string]int{}
)

// noteRead counts a read of host:path in a channel and returns the new count.
func noteRead(caller, host, path string) int {
	key := strings.ToLower(caller + "|" + host + "|" + path)
	readCountMu.Lock()
	defer readCountMu.Unlock()
	if len(readCounts) > 2000 {
		readCounts = map[string]int{}
	}
	readCounts[key]++
	return readCounts[key]
}

type editRun struct {
	path string
	at   time.Time
	run  int
}

var (
	editMu     sync.Mutex
	editLast   = map[string]*editRun{}
	editHinted = map[string]bool{}
)

const editBatchWindow = 90 * time.Second

// editBatchHint: on the 2nd single-change edit_file in a row on the same file
// (same channel, < 90 s apart) suggest one call with edits=[...]. Once per file.
func editBatchHint(caller, path string, ops int) string {
	now := time.Now()
	p := strings.ToLower(path)
	editMu.Lock()
	defer editMu.Unlock()
	if len(editLast) > 500 {
		editLast, editHinted = map[string]*editRun{}, map[string]bool{}
	}
	r := editLast[caller]
	if r != nil && r.path == p && now.Sub(r.at) < editBatchWindow {
		r.run++
		r.at = now
	} else {
		r = &editRun{path: p, at: now, run: 1}
		editLast[caller] = r
	}
	key := caller + "|" + p
	if r.run >= 2 && ops == 1 && !editHinted[key] {
		editHinted[key] = true
		return "[FastExec Hint] Second edit_file in a row on this file: send all the changes in one call with edits=[{target, replacement}, ...] (applied in order, all or nothing); it saves a round trip per change."
	}
	return ""
}
