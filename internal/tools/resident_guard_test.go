package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInferBinaryFromCommand(t *testing.T) {
	tests := []struct {
		cmd      string
		expected string
	}{
		{"go build ./cmd/agenteha", "agenteha"},
		{"go build -trimpath -o bin/server.exe ./cmd/server", "server.exe"},
		{"go build -o=myprog ./...", "myprog"},
		{"cargo build --bin worker", ""}, // inferBinaryFromCommand covers flags
	}

	for _, tc := range tests {
		got := inferBinaryFromCommand(tc.cmd)
		if got != tc.expected {
			t.Errorf("cmd %q: expected %q, got %q", tc.cmd, tc.expected, got)
		}
	}
}

func TestCheckResidentService_NoBuild(t *testing.T) {
	hint := checkResidentService("", "git status", "local")
	if hint != "" {
		t.Errorf("expected empty hint for git status, got %q", hint)
	}
}

// residentHelperEnv marks the re-executed test binary as the resident process.
const residentHelperEnv = "FASTEXEC_RESIDENT_HELPER"

// TestResidentHelperProcess is not a test on its own: when the env flag is set,
// the re-executed test binary becomes the resident process and sleeps until the
// parent kills it.
func TestResidentHelperProcess(t *testing.T) {
	if os.Getenv(residentHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(2 * time.Minute)
}

// startResidentProcess copies the running test binary to dir/name and starts it
// as a real process, so the guard sees a process that was already running.
func startResidentProcess(t *testing.T, dir, name string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, image, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=^TestResidentHelperProcess$")
	cmd.Env = append(os.Environ(), residentHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the resident helper %s: %v", name, err)
	}
	// Registered after t.TempDir's cleanup, so it runs first: the process is
	// dead before its directory is removed.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

func TestCheckResidentService_DetectsActiveProcess(t *testing.T) {
	dir := t.TempDir()
	startResidentProcess(t, dir, "agenteha.exe")
	// The guard only warns about processes started more than 2 s before the build.
	time.Sleep(2500 * time.Millisecond)

	hint := checkResidentService(dir, "go build -o agenteha.exe .", "local")
	if !strings.Contains(hint, "[FastExec Guard]") {
		t.Fatalf("esperado aviso do FastExec Guard para processo ativo, obtido: %q", hint)
	}
	if !strings.Contains(hint, "agenteha") {
		t.Errorf("esperado menção a agenteha no aviso, obtido: %q", hint)
	}
	t.Logf("Aviso gerado com sucesso:\n%s", hint)
}

func TestCheckResidentService_NoProcessNoWarning(t *testing.T) {
	dir := t.TempDir()
	// Same shape as a real build output, but no process with this name runs.
	if err := os.WriteFile(filepath.Join(dir, "fastexec-sem-processo.exe"), []byte("fake"), 0755); err != nil {
		t.Fatal(err)
	}
	if hint := checkResidentService(dir, "go build -o fastexec-sem-processo.exe .", "local"); hint != "" {
		t.Errorf("sem processo residente não deve haver aviso, obtido: %q", hint)
	}
}
