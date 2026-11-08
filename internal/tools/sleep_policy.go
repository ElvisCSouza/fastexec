package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Sleep policy for sleeps embedded in a larger command line.
//
// A pure `sleep N` is handled by tryRemediateSleep. Here we decide what to do
// with `cmd1; sleep 5; cmd2` and poll loops: instead of rejecting the whole
// script, short waits are allowed (the LLM is told so), and long ones are
// rejected with the exact next step.

const (
	embeddedSleepBudgetSec = 15.0 // total of all embedded sleeps
	pollLoopSleepMaxSec    = 2.0  // per-iteration sleep inside until/while/for
)

var embeddedSleepRegex = regexp.MustCompile(`(?i)\b(?:sleep|start-sleep(?:\s+(?:-s|-seconds))?)\s+(\d+(?:\.\d+)?)\b`)
var pyEmbeddedSleepRegex = regexp.MustCompile(`(?i)time\.sleep\(\s*(\d+(?:\.\d+)?)\s*\)`)
var loopKeywordRegex = regexp.MustCompile(`(?i)\b(until|while|for|do)\b`)
var winTimeoutRegex = regexp.MustCompile(`(?i)\btimeout(?:\.exe)?\s+(?:/t\s+)?\d+`)

// sleepAllowance decides whether a command rejected by the sleep guard may
// run anyway. ok=true: run it and append notice to the result.
// ok=false: reject with reason (which names the next step).
func sleepAllowance(cmd string) (notice string, reason string, ok bool) {
	if winTimeoutRegex.MatchString(cmd) && !embeddedSleepRegex.MatchString(cmd) {
		return "", sleepGuardMsg, false
	}

	var secs []float64
	for _, re := range []*regexp.Regexp{embeddedSleepRegex, pyEmbeddedSleepRegex} {
		for _, m := range re.FindAllStringSubmatch(cmd, -1) {
			if f, err := strconv.ParseFloat(m[1], 64); err == nil {
				secs = append(secs, f)
			}
		}
	}
	if len(secs) == 0 {
		return "", sleepGuardMsg, false
	}

	var total, max float64
	for _, s := range secs {
		total += s
		if s > max {
			max = s
		}
	}

	if loopKeywordRegex.MatchString(cmd) && max <= pollLoopSleepMaxSec {
		return fmt.Sprintf("[FastExec Auto-Remediation] Poll loop with sleep <= %gs per iteration was allowed. Keep a hard iteration limit; if the wait can exceed ~30 s, use job(action='wait') instead.", pollLoopSleepMaxSec), "", true
	}

	if total <= embeddedSleepBudgetSec {
		return fmt.Sprintf("[FastExec Auto-Remediation] Embedded sleep (%gs in total) was allowed because it is within the %gs budget. FastExec is event-driven: prefer waiting on the port, file or job instead of a fixed pause.", total, embeddedSleepBudgetSec), "", true
	}

	return "", fmt.Sprintf("[FastExec Guard] Embedded sleeps total %gs (budget %gs). Nothing was run. Next: start the long work with job(action='start') and then job(action='wait', job_id=...) (blocks up to 35 s per call); or split the command and drop the pause.", total, embeddedSleepBudgetSec), false
}

// fixTimeoutWrappers is documentation of the prefixes accepted before the
// coreutils `timeout` (see coreutilsTimeoutRegex).
var _ = strings.TrimSpace
