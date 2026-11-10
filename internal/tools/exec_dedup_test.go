package tools

import (
	"strings"
	"testing"
)

func resetExecDedup() {
	execDedupMu.Lock()
	execDedupSeen = map[string]*execSeen{}
	execDedupMu.Unlock()
}

func TestExecDedupStubThenFullAgain(t *testing.T) {
	resetExecDedup()
	args := ExecArgs{Host: "noteserver", Command: "big", Caller: "c#1"}
	big := strings.Repeat("linha de saida\n", 400)
	if got := dedupExecOutput(args, "big", big); got != big {
		t.Fatal("1st run must return the full text")
	}
	got := dedupExecOutput(args, "big", big)
	if got == big || !strings.Contains(got, "Identical output") {
		t.Fatalf("2nd identical run must be a stub, got %q", got)
	}
	if len(got) > 400 {
		t.Fatalf("stub too long: %d", len(got))
	}
	if got := dedupExecOutput(args, "big", big); got != big {
		t.Fatal("3rd identical run must return the full text again")
	}
}

func TestExecDedupChangedOutputPasses(t *testing.T) {
	resetExecDedup()
	args := ExecArgs{Host: "noteserver", Command: "big", Caller: "c#1"}
	a := strings.Repeat("a\n", 2000)
	b := strings.Repeat("b\n", 2000)
	dedupExecOutput(args, "big", a)
	if got := dedupExecOutput(args, "big", b); got != b {
		t.Fatal("changed output must pass through")
	}
}

func TestExecDedupSmallAndIsolation(t *testing.T) {
	resetExecDedup()
	args := ExecArgs{Host: "noteserver", Command: "x", Caller: "c#1"}
	small := "ok\n"
	dedupExecOutput(args, "x", small)
	if got := dedupExecOutput(args, "x", small); got != small {
		t.Fatal("small outputs are never deduplicated")
	}
	big := strings.Repeat("z\n", 3000)
	dedupExecOutput(args, "x", big)
	other := args
	other.Caller = "c#2"
	if got := dedupExecOutput(other, "x", big); got != big {
		t.Fatal("another channel must get the full text")
	}
	diffCwd := args
	diffCwd.Cwd = "/other"
	if got := dedupExecOutput(diffCwd, "x", big); got != big {
		t.Fatal("another cwd must get the full text")
	}
}

func TestExecDedupOffByEnv(t *testing.T) {
	resetExecDedup()
	t.Setenv("FASTEXEC_NO_EXEC_DEDUP", "1")
	args := ExecArgs{Host: "h", Command: "y", Caller: "c"}
	big := strings.Repeat("z\n", 3000)
	dedupExecOutput(args, "y", big)
	if got := dedupExecOutput(args, "y", big); got != big {
		t.Fatal("env off must disable dedup")
	}
}
