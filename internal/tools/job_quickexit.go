package tools

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

// Falha rápida de um job: um comando que morre logo depois de começar (interpretador ausente, erro de sintaxe,
// arquivo que não existe) respondia só "[job] started", e o modelo precisava de status + tail/read_file para ver o
// erro (medido no P4 do bench ciclo1b: +37,7% de tokens). Aqui o start/status/wait/tail já devolvem rc, vida e log.

// jobStartBudget: quanto o 'start' espera, sem parâmetro extra, antes de responder. Se o comando terminar nesse prazo,
// a resposta já traz rc e saída (dispensa start + wait). Se não, o job segue e a resposta é a de sempre, com o id.
// 12 s cobre build/teste curto sem prender a chamada: o teto do cliente é 60 s e o start nunca passa disso.
// Medido na telemetria de 14/11: waits de até 10–20 s eram ~19 de 62; cada start de job vivo paga o orçamento inteiro.
// Variável só para os testes encurtarem.
var jobStartBudget = 12 * time.Second

// startAutoNote: linha [FastExec ...] que avisa que a espera automática do start resolveu a chamada.
// Em inglês: tudo que o modelo lê segue o AGENTS.md.
func startAutoNote() string {
	return fmt.Sprintf("[FastExec auto-wait] start waited up to %s: the command finished, output below (no wait/tail).", human(jobStartBudget))
}

// jobStateRunning: estado de um job ainda vivo (transport/localjob_windows.go).
const jobStateRunning = "RUNNING"

// jobStateExited: o processo terminou sozinho. Só esse estado entra na "falha rápida": job parado a pedido do usuário
// (STOPPED) não é surpresa e continua com a resposta de antes.
const jobStateExited = "EXITED"

const (
	quickExitLines = 10               // linhas mostradas do log
	quickExitChars = 400              // teto de caracteres do trecho de log
	quickExitLife  = 10 * time.Second // "morreu na largada": até aqui rc e log cabem na resposta
	quickExitLog   = 1 << 10          // log curto: o trecho é o log inteiro
)

// jobLife devolve quanto o job viveu e se ele já terminou (metadado ou, na falta dele, a última atividade do log).
func jobLife(j *transport.LocalJobInfo) (time.Duration, bool) {
	if j.State == jobStateRunning {
		return time.Since(j.Meta.CreatedAt), false
	}
	if j.Meta.EndedAt != nil {
		return j.Meta.EndedAt.Sub(j.Meta.CreatedAt), true
	}
	return j.LastActivity.Sub(j.Meta.CreatedAt), true
}

// isQuickExit: job terminado com vida curta e log curto — o rc e o log cabem na resposta sem virar despejo.
func isQuickExit(j *transport.LocalJobInfo) bool {
	if j.State != jobStateExited {
		return false
	}
	if j.Meta.EndedAt == nil {
		// fim detectado pelo log, sem metadado de término: a vida vem da última atividade
		if j.LastActivity.Sub(j.Meta.CreatedAt) > quickExitLife {
			return false
		}
	} else if life, _ := jobLife(j); life > quickExitLife {
		return false
	}
	return j.LogSize <= quickExitLog
}

func rcText(rc *int) string {
	if rc == nil {
		return "?"
	}
	return fmt.Sprint(*rc)
}

// logExcerpt devolve as últimas linhas do log, já limpas e cortadas em maxChars.
func logExcerpt(log string, lines, maxChars int) string {
	s := strings.TrimRight(cleanOutput(log), "\n")
	if s == "" {
		return ""
	}
	if ls := strings.Split(s, "\n"); len(ls) > lines {
		s = strings.Join(ls[len(ls)-lines:], "\n")
	}
	if r := []rune(s); len(r) > maxChars {
		s = "…" + string(r[len(r)-maxChars:])
	}
	return s
}

// Ferramentas cujo sumiço no host custa um turno inteiro ao modelo (o comando morre com rc 127/9009 e nada diz o que
// fazer). Vale para o job o mesmo FastHint que o exec já dá.
var missingToolNames = []string{
	"python", "python3", "py", "pip", "pip3", "node", "npm", "npx", "go", "gofmt", "pwsh", "powershell",
	"git", "bash", "sh", "java", "dotnet", "ruby", "perl", "php", "docker", "sqlite3", "curl", "jq", "rg", "make",
}

var (
	reCmdNotRecognized  = regexp.MustCompile(`(?i)'([^'\r\n]+)' is not recognized as an internal or external command`)
	reTermNotRecognized = regexp.MustCompile(`(?i)The term '([^'\r\n]+)' is not recognized as the name of a cmdlet`)
	reShNotFound        = regexp.MustCompile(`(?i)([\w.+-]+): (?:command not found|not found)`)
)

// notFoundProgram: nome do programa que o shell não achou, segundo o log ("" quando o log não diz).
func notFoundProgram(log string) string {
	for _, re := range []*regexp.Regexp{reCmdNotRecognized, reTermNotRecognized, reShNotFound} {
		if m := re.FindStringSubmatch(log); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// knownMissingTool reduz o nome (caminho, extensão, caixa) e devolve o nome canônico quando é uma ferramenta conhecida.
func knownMissingTool(name string) string {
	n := strings.ToLower(strings.Trim(strings.TrimSpace(name), `"'`))
	n = strings.TrimSuffix(strings.TrimSuffix(n, ".exe"), ".cmd")
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	for _, t := range missingToolNames {
		if n == t {
			return n
		}
	}
	return ""
}

func firstToken(line string) string {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) == 0 {
		return ""
	}
	return strings.Trim(f[0], `"'`)
}

// clipLine corta uma linha de comando para caber numa linha da resposta.
func clipLine(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// jobMissingToolHint: o job morreu porque o programa não existe no host. Devolve o mesmo FastHint do exec para Python
// (o texto já testado, que cita pwsh e as file tools) e o equivalente para as outras ferramentas; "" quando não é isso.
func jobMissingToolHint(label string, rc *int, log string) string {
	name := knownMissingTool(notFoundProgram(log))
	if name == "" && rc != nil && (*rc == 127 || *rc == 9009) {
		// nem todo shell imprime o nome que faltou (e o log pode estar truncado): o 1º token da linha é o candidato
		name = knownMissingTool(firstToken(label))
	}
	switch name {
	case "":
		return ""
	case "python", "python3", "py":
		return pythonMissingHint
	}
	return fmt.Sprintf("[FastExec FastHint] '%s' is not installed on this host: use the argv form with a program that exists here (pwsh for scripts, in 'input'), or the file tools (read_file pattern/ranges, code_outline, edit_file).", name)
}

// localJobExitReport responde por um job local que morreu na largada: rc, vida e as últimas linhas do log, para o
// modelo não gastar um 'status' + 'tail'/read_file só para ver o erro.
func localJobExitReport(j *transport.LocalJobInfo) string {
	life, _ := jobLife(j)
	tail, _ := transport.TailLocalJob(j.Meta.ID, quickExitLines)
	var b strings.Builder
	fmt.Fprintf(&b, "[job] EXITED rc=%s after %s (local) | id=%s | log %d B\n", rcText(j.Meta.ExitCode), human(life), j.Meta.ID, j.LogSize)
	fmt.Fprintf(&b, "command: %s\n", jobLabel(j.Meta))
	if ex := logExcerpt(tail, quickExitLines, quickExitChars); ex != "" {
		b.WriteString(ex + "\n")
	}
	if h := jobMissingToolHint(jobLabel(j.Meta), j.Meta.ExitCode, tail); h != "" {
		b.WriteString(h + "\n")
	}
	fmt.Fprintf(&b, "Next: job(action='tail', job_id='%s') for the whole log.", j.Meta.ID)
	return b.String()
}

// startTailCutNote: o comando terminou na janela do start e o log passou das 30 linhas mostradas — a resposta diz o
// que foi retido e a chamada exata que traz o resto (regra: quando algo é retido, dizer o próximo passo). Com até 30
// linhas esta nota não existe.
func startTailCutNote(m *transport.LocalJobMeta, total, shown int) string {
	if total > 500 { // o tail local traz no máximo 500 linhas: as linhas do começo só com read_file
		return fmt.Sprintf("(showing %d of %d lines; job(action='tail', job_id='%s', lines=500) for the last 500, read_file '%s' for the rest)", shown, total, m.ID, m.LogPath)
	}
	return fmt.Sprintf("(showing %d of %d lines; job(action='tail', job_id='%s', lines=500) for the rest)", shown, total, m.ID)
}

// localStartReport é a resposta do 'start' local (sem await): espera até jobStartBudget. Se o processo terminou nesse
// prazo, responde com rc e saída (a mesma forma do await); se continua rodando, a resposta é exatamente a de antes.
func localStartReport(m *transport.LocalJobMeta) string {
	if j, finished, err := transport.LocalJobWait(m.ID, jobStartBudget); err == nil && finished {
		if isQuickExit(j) {
			return startAutoNote() + "\n" + localJobExitReport(j)
		}
		tail, _ := transport.TailLocalJob(m.ID, 30)
		tail = capOutput(cleanOutput(tail))
		out := startAutoNote() + "\n" + waitReport(j.Meta.ID, "local|"+j.Meta.ID, tail, true, localStateText(j))
		if total, terr := transport.LocalJobLogLines(m.ID); terr == nil && total > 30 {
			out += "\n" + startTailCutNote(m, total, len(logLines(tail)))
		}
		return out
	}
	note := ""
	if m.State != jobStateRunning {
		note = "\n(the process already exited; see the output with tail)"
	}
	return fmt.Sprintf("[job] started (local)\nid:      %s\ncommand: %s\nlog:     %s%s\nUse job(host='local', action='status'|'tail'|'wait'|'stop', job_id='%s')",
		m.ID, jobLabel(*m), m.LogPath, note, m.ID)
}

// localStateText: estado do job para o cabeçalho do wait, com rc e vida quando o job terminou.
func localStateText(j *transport.LocalJobInfo) string {
	s := fmt.Sprint(j.State)
	if j.State == jobStateRunning {
		return s
	}
	life, finished := jobLife(j)
	if finished {
		s = fmt.Sprintf("%s rc=%s after %s", s, rcText(j.Meta.ExitCode), human(life))
	}
	return s
}

// jobLogHint: FastHint a partir do log de um job (usado por wait/tail, que já mostram o log).
func jobLogHint(label string, rc *int, tail string) string {
	return jobMissingToolHint(label, rc, tail)
}
