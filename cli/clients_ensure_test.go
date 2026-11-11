package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Num computador novo a pasta do Claude Desktop existe, mas o claude_desktop_config.json ainda não.
func TestEnsureDesktopConfigs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("layout de pastas do Windows")
	}
	home := sandbox(t)
	if got := ensureDesktopConfigs(false); len(got) != 0 {
		t.Fatalf("sem pasta do app nada deve ser criado: %v", got)
	}
	dir := filepath.Join(home, "AppData", "Local", "Claude-Data", "claude_abc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := ensureDesktopConfigs(false)
	want := filepath.Join(dir, "claude_desktop_config.json")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("criados %v, esperado [%s]", got, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "{}\n" {
		t.Fatalf("conteúdo %q", b)
	}
	// um arquivo que já existe nunca é tocado
	if err := os.WriteFile(want, []byte(`{"mcpServers":{"x":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ensureDesktopConfigs(false); len(got) != 0 {
		t.Fatalf("arquivo existente: criados %v", got)
	}
	if b, _ := os.ReadFile(want); string(b) != `{"mcpServers":{"x":{}}}` {
		t.Fatalf("arquivo existente foi alterado: %q", b)
	}
	// e o setup passa a achar o cliente
	os.Remove(want)
	ensureDesktopConfigs(false)
	if cf := clientFiles(); len(cf) != 1 || cf[0].Client != "Claude Desktop" {
		t.Fatalf("clientFiles %v", cf)
	}
}
