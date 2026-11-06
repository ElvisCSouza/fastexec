//go:build windows

package transport

import (
	"testing"
	"time"
)

// Bug 5: "start /b" deixa um filho vivo depois que o cmd termina. O job o encerra no fim do exec; a chamada precisa dizer isso
// (Leftover > 0) em vez de devolver sucesso silencioso.
func TestLeftoverDetectaFilhoSoltoEncerradoPeloJob(t *testing.T) {
	start := time.Now()
	res, err := ExecLocal(`start "" /b ping -n 30 127.0.0.1 >nul`, 30*time.Second)
	if err != nil {
		t.Fatalf("erro: %v", err)
	}
	if res.Leftover < 1 {
		t.Errorf("Leftover = %d, quer >= 1 (ping solto não foi detectado)", res.Leftover)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("o job deveria encerrar o filho ao fim do exec, levou %v", elapsed)
	}
}

func TestLeftoverZeroSemFilhoSolto(t *testing.T) {
	res, err := ExecLocal("echo ok", 10*time.Second)
	if err != nil {
		t.Fatalf("erro: %v", err)
	}
	if res.Leftover != 0 {
		t.Errorf("Leftover = %d em comando sem filho solto", res.Leftover)
	}
}
