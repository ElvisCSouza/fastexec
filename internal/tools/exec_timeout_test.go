package tools

import (
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/transport"
)

func TestDefaultTimeoutFicaAbaixoDoLimiteDoCliente(t *testing.T) {
	if transport.DefaultTimeout >= maxClientTimeout {
		t.Errorf("DefaultTimeout (%v) deveria ser menor que o teto (%v)", transport.DefaultTimeout, maxClientTimeout)
	}
	if MaxClientTimeoutMs != 210000 {
		t.Errorf("o teto deveria ser 210000 ms, é %d", MaxClientTimeoutMs)
	}
}

func TestExecTimeoutAcimaDoTetoEhLimitado(t *testing.T) {
	old := maxClientTimeout
	maxClientTimeout = 1500 * time.Millisecond // o teto real (210 s) é demorado demais para testar
	t.Cleanup(func() { maxClientTimeout = old })

	start := time.Now()
	out, err := HandleExec(ExecArgs{
		Host:      "local",
		Argv:      []string{"node", "-e", "console.log('parcial');setTimeout(function(){},60000)"},
		TimeoutMs: 350000,
	})
	if err == nil {
		t.Fatalf("deveria estourar o teto: %q", out)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("devia voltar no teto, não nos 350 s pedidos: %v", time.Since(start))
	}
	for _, want := range []string{"[timeout após 1.5s", "árvore de processos encerrada", "parcial", "foi limitado a 1500 ms", "tool 'job'"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em: %q", want, out)
		}
	}
}

func TestExecTimeoutNormalTambemOrientaOJob(t *testing.T) {
	out, err := HandleExec(ExecArgs{Host: "local", Argv: []string{"node", "-e", "setTimeout(function(){},60000)"}, TimeoutMs: 1000})
	if err == nil || !strings.Contains(out, "tool 'job'") || strings.Contains(out, "foi limitado") {
		t.Errorf("timeout normal deve orientar o job sem a nota de limite: %v %q", err, out)
	}
}
