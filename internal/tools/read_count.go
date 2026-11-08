package tools

import (
	"strings"
	"sync"
)

// rereadHintAt is the read number (per file, per process) that triggers the
// graph hint. Only that exact read gets it, so it is never repeated.
const rereadHintAt = 3

var (
	readCountMu sync.Mutex
	readCounts  = map[string]int{}
)

// noteRead counts a read of host:path and returns the new count.
func noteRead(host, path string) int {
	key := strings.ToLower(host + "|" + path)
	readCountMu.Lock()
	defer readCountMu.Unlock()
	if len(readCounts) > 2000 {
		readCounts = map[string]int{}
	}
	readCounts[key]++
	return readCounts[key]
}
