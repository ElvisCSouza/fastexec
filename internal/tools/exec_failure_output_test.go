package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/transport"
)

// TestExecFalhaNaoDescartaStdout: o stdout de um comando que falhou é resultado, não ruído. Linha composta
// ('busca && interpretador') em que o primeiro estágio imprime e o segundo morre devolvia só o erro do segundo — a
// LLM repetia a chamada inteira só para ver o que já tinha saído, um turno pago a mais. A regra é do exec, não de um
// repositório ou linguagem: vale para qualquer comando com saída nos dois canais.
func TestExecFalhaNaoDescartaStdout(t *testing.T) {
	out, err := formatExecOutput(`interpretador -m testes`, transport.ExecResult{
		ExitCode: 49,
		Stdout:   "pkg/a.go:12:alvo(x)\npkg/b.go:30:alvo(y)\n",
		Stderr:   "interpretador was not found\n",
	})
	if err == nil {
		t.Fatal("exit != 0 continua devolvendo erro para o MCP; quem mudou foi só o texto")
	}
	if !strings.HasPrefix(out, "[exit 49] interpretador was not found") {
		t.Errorf("o '[exit N]' com a stderr tem de abrir o texto (é dele que o erro do canal sai): %q", out)
	}
	if !strings.Contains(out, "pkg/a.go:12:alvo(x)") || !strings.Contains(out, "pkg/b.go:30:alvo(y)") {
		t.Errorf("o stdout do estágio que funcionou sumiu: %q", out)
	}
	if !strings.Contains(out, "[FastExec]") {
		t.Errorf("o acréscimo automático precisa se anunciar: %q", out)
	}

	// Linha composta de busca: grep com casamento já é desfecho benigno (o erro do MCP não muda por causa do stdout);
	// o que a LLM precisa ver é o casamento junto com o motivo da falha do estágio seguinte.
	out, err = formatExecOutput(`grep -rn "alvo" . && interpretador -m testes`, transport.ExecResult{
		ExitCode: 49,
		Stdout:   "pkg/a.go:12:alvo(x)\n",
		Stderr:   "interpretador was not found\n",
	})
	if err != nil {
		t.Errorf("busca com casamento segue benigna: %v", err)
	}
	if !strings.Contains(out, "pkg/a.go:12:alvo(x)") || !strings.Contains(out, "interpretador was not found") {
		t.Errorf("os dois canais têm de aparecer: %q", out)
	}

	// Sem stderr o stdout já era o corpo do erro: nada é repetido, nada é marcado de novo.
	out, _ = formatExecOutput("comando", transport.ExecResult{ExitCode: 2, Stdout: "saida unica\n"})
	if out != "[exit 2] saida unica\n" {
		t.Errorf("stdout como único canal não muda: %q", out)
	}

	// Sem stdout o texto é o de sempre.
	out, _ = formatExecOutput("comando", transport.ExecResult{ExitCode: 2, Stderr: "erro seco\n"})
	if out != "[exit 2] erro seco\n" {
		t.Errorf("sem stdout o texto não muda: %q", out)
	}

	// Silêncio total nos dois canais segue sendo silêncio (busca sem casamento é desfecho normal).
	out, _ = formatExecOutput(`grep -rn "nada" .`, transport.ExecResult{ExitCode: 1})
	if out != NoResultText {
		t.Errorf("exit 1 mudo continua NoResultText: %q", out)
	}
}

// TestNotaDeBashCurta: a nota de troca de shell volta em toda chamada Unix do canal e é cobrada de novo em todo turno
// seguinte (o bench roda com o cache do provedor desligado). Ela precisa continuar dizendo o que rodou e o próximo
// passo — o que não pode voltar é a explicação longa, que a primeira chamada já deu.
func TestNotaDeBashCurta(t *testing.T) {
	comBashReal(t)
	_, note, ok := bashPlanFor("local", `grep -rn "func main" . | wc -l`)
	if !ok {
		t.Fatal("linha Unix com bash no host deveria virar plano de bash")
	}
	if len(note) > 260 {
		t.Errorf("nota com %d caracteres: o texto repetido sai do orçamento do modelo a cada turno (%q)", len(note), note)
	}
	if !strings.Contains(note, "ran in bash") || !strings.Contains(note, "argv ['bash','-lc'") {
		t.Errorf("nota sem o que rodou e sem o próximo passo: %q", note)
	}
}
