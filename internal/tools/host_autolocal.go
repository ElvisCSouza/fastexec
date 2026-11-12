package tools

// host_autolocal.go: quando a chamada não informa 'host', o host padrão é remoto (Linux) e o comando traz um caminho
// de unidade Windows (W:\..., C:/...), a chamada roda no host 'local' e a saída avisa a LLM. Sem isso o comando era
// despachado para o Linux remoto e voltava "No such file or directory" / "pwsh: command not found" / "powershell:
// command not found": a LLM gastava um turno lendo o erro e outro reenviando com host='local'.
// A detecção é conservadora: letra de unidade seguida de ':\' ou ':/', no início da linha/token ou depois de um
// separador de shell (espaço, aspas, '=', '(', '|', '&', ';', '>'). URL (http://, file://) e texto solto não disparam,
// porque neles os dois-pontos vêm depois de uma letra. Host informado explicitamente nunca é trocado.

import (
	"fmt"
	"regexp"
	"strings"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/transport"
)

// winDrivePathRe: grupo 1 = o caminho Windows (ex: W:\ReposHomeLab\x.txt, c:/tmp/a.sh).
var winDrivePathRe = regexp.MustCompile(`(?:^|[\s"'=(|&;>])([A-Za-z]:[\\/][^\s"')|&;>]*)`)

// windowsPathIn devolve o primeiro caminho de unidade Windows encontrado nas partes, ou "".
func windowsPathIn(parts ...string) string {
	for _, p := range parts {
		if m := winDrivePathRe.FindStringSubmatch(p); m != nil {
			return m[1]
		}
	}
	return ""
}

// localHostForWindowsPath decide em que host uma chamada de exec/job roda. Devolve o host a usar e a nota para a LLM
// (vazia quando nada muda). As partes são command, argv e cwd.
func localHostForWindowsPath(host string, parts ...string) (string, string) {
	if strings.TrimSpace(host) != "" {
		return host, "" // host explícito sempre vence
	}
	if def := config.Get().Default(); def == "" || def == "local" {
		return host, "" // o padrão já é este computador
	}
	t, err := transport.ResolveHost("")
	if err != nil || t.IsLocal {
		return host, ""
	}
	p := windowsPathIn(parts...)
	if p == "" {
		return host, "" // sem caminho de unidade: comportamento atual
	}
	return "local", fmt.Sprintf("[FastExec Auto-Remediation] 'host' was not given and this command carries the Windows path '%s', "+
		"which does not exist on the default host '%s' (Linux). It ran on host 'local' instead. "+
		"Next time pass host='local'; when the target really is '%s', use its Linux path (e.g. /home/...).", p, t.Name, t.Name)
}

// withHostNote põe a nota de auto-remediação antes da saída, no mesmo formato dos outros avisos. Com erro e sem
// saída a nota é descartada: o MCP mostra a mensagem de erro ao cliente e uma saída não vazia a substituiria.
func withHostNote(note, out string, err error) string {
	if note == "" || (out == "" && err != nil) {
		return out
	}
	if out == "" {
		return note
	}
	return note + "\n---\n" + out
}
