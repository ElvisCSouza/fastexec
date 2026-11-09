package tools

import (
	"fmt"
	"strings"
	"sync"
)

// Incremental job wait: every 'wait' used to return the same last lines of the
// log again (20 local, 50 remote). Now each wait returns only what is new since
// the previous wait of the same job, or one line saying nothing changed.
// The key is the job, not the caller: if two conversations wait on the same job
// one may get "no new output" for lines the other received, so every
// incremental answer names job(action='tail'), which always shows the full log.

var (
	jobSeenMu sync.Mutex
	jobSeen   = map[string][]string{}
)

const jobSeenKeep = 200

func logLines(s string) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// newSinceLastWait returns the lines of cur not returned by the previous wait
// of key, and whether a previous wait existed. scrolled=true means the
// overlap was lost (more new lines than the window), so cur is all new.
func newSinceLastWait(key, cur string) (fresh []string, hadPrev, scrolled bool) {
	curL := logLines(cur)
	jobSeenMu.Lock()
	defer jobSeenMu.Unlock()
	prev, hadPrev := jobSeen[key]
	keep := curL
	if len(keep) > jobSeenKeep {
		keep = keep[len(keep)-jobSeenKeep:]
	}
	if len(jobSeen) > 500 {
		jobSeen = map[string][]string{}
	}
	jobSeen[key] = append([]string(nil), keep...)
	if !hadPrev {
		return curL, false, false
	}
	// maior k: sufixo de prev (k linhas) == prefixo de cur (k linhas)
	for k := min(len(prev), len(curL)); k > 0; k-- {
		if equalLines(prev[len(prev)-k:], curL[:k]) {
			return curL[k:], true, false
		}
	}
	// cur inteiro já visto no meio de prev (janela igual, nada novo)
	if len(curL) > 0 && containsRun(prev, curL) {
		return nil, true, false
	}
	return curL, true, len(curL) > 0
}

func equalLines(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsRun(hay, run []string) bool {
	for i := 0; i+len(run) <= len(hay); i++ {
		if equalLines(hay[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

// waitReport formats a wait result with only the new log lines.
func waitReport(jobID, key, log string, finished bool, state string) string {
	fresh, hadPrev, scrolled := newSinceLastWait(key, log)
	var head string
	switch {
	case finished && state != "":
		head = fmt.Sprintf("[job %s finished: %s]", jobID, state)
	case finished:
		head = fmt.Sprintf("[job %s finished]", jobID)
	default:
		head = fmt.Sprintf("[job %s still running after %s]", jobID, human(jobWaitMax))
	}
	var body string
	switch {
	case hadPrev && len(fresh) == 0:
		body = "(no new output since the last wait; job(action='tail') shows the full log)"
	case hadPrev && scrolled:
		body = "(more new output than shown; job(action='tail', lines=200) for the rest)\n" + strings.Join(fresh, "\n")
	case hadPrev:
		body = fmt.Sprintf("(%d new line(s) since the last wait; job(action='tail') for the full log)\n%s", len(fresh), strings.Join(fresh, "\n"))
	default:
		body = strings.Join(fresh, "\n")
	}
	if !finished {
		head += "\nANTI-POLLING: do not call 'wait' back to back. Use your client's scheduling (e.g. a timer) to check again in a few minutes, or end your turn telling the user."
	}
	if strings.TrimSpace(body) == "" {
		return head
	}
	return head + "\n" + body
}
