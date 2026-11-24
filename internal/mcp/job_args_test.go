package mcp

import (
	"strings"
	"testing"
)

// A timeout on job is refused (there is no timeout to map it to), and the refusal lists only what the action reads
// and says what to do instead.
func TestJobTimeoutRefusedWithNextStep(t *testing.T) {
	_, _, err := norm(t, "job", `{"action":"wait","job_id":"j1","timeout_ms":30000}`)
	if err == nil {
		t.Fatal("job timeout_ms was accepted")
	}
	for _, want := range []string{
		"job: unknown parameter 'timeout_ms'",
		"Valid for action 'wait': action, cursor, host, job_id.",
		"call it again to keep waiting",
		"action 'start' with await=true",
		"Nothing was run.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err.Error(), want)
		}
	}
}

// Without an action the full list is shown, not one action's.
func TestJobUnknownWithoutActionListsAll(t *testing.T) {
	_, _, err := norm(t, "job", `{"timeout_ms":5}`)
	if err == nil || !strings.Contains(err.Error(), "Valid: action,") {
		t.Fatalf("error = %v, want the full valid list", err)
	}
}

// Only single-target equivalences are renamed: dir/workdir/directory -> cwd and id -> job_id.
func TestJobAliasesToCanonical(t *testing.T) {
	out, notes, err := norm(t, "job", `{"action":"start","command":"x","dir":"/tmp/x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"cwd":"/tmp/x"`) || !strings.Contains(strings.Join(notes, ";"), "dir -> cwd") {
		t.Errorf("dir not read as cwd: %s notes=%v", out, notes)
	}
	out, notes, err = norm(t, "job", `{"action":"status","id":"j1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"job_id":"j1"`) || !strings.Contains(strings.Join(notes, ";"), "id -> job_id") {
		t.Errorf("id not read as job_id: %s notes=%v", out, notes)
	}
}

// Every parameter named in jobActionParams is a real field of job's arguments.
func TestJobActionParamsExist(t *testing.T) {
	fs := argSpecs["job"]
	if fs == nil {
		t.Fatal("job has no typed argument spec")
	}
	for action, list := range jobActionParams {
		for _, name := range strings.Split(list, ", ") {
			if !fs.names[name] {
				t.Errorf("action %q lists %q, which job does not take", action, name)
			}
		}
	}
}
