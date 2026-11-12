package tools

import (
	"fmt"
	"time"
)

// Tempo decorrido no fim da resposta. Medido no bench ciclo1b/ciclo2 (tarefa P3, "job de ~50 s: dizer o resultado e
// quanto levou"): o comando devolvia a saída completa mas sem tempo, e a LLM rodava tudo DE NOVO com
// s=$(date +%s%3N); ...; echo ELAPSED_MS=... só para medir — o dobro da latência e turnos extras com o imposto fixo
// de schema. Um rodapé de 20 caracteres responde a pergunta no mesmo turno.
//
// Abaixo de elapsedMin nada muda: a execução rápida continua byte-idêntica à de antes (nenhum byte novo na resposta).

// elapsedMin: limiar a partir do qual o rodapé aparece. Um comando de 3 s já é "longo" para quem quer medir.
const elapsedMin = 3 * time.Second

// elapsedText: duração em formato curto — uma casa decimal até 2 min ("50.3s"), "2m05s" acima disso (a casa decimal
// deixa de ser útil e o número de minutos é o que a LLM cita).
func elapsedText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= 2*time.Minute {
		total := int(d.Round(time.Second) / time.Second)
		return fmt.Sprintf("%dm%02ds", total/60, total%60)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// elapsedFooter: a linha "[elapsed X]" que fecha a saída do exec, ou "" quando ela não cabe (comando rápido) ou não
// serve (o timeout já diz o tempo no "[timeout after Xs]"; o job promovido cita o tempo na nota de promoção).
func elapsedFooter(d time.Duration, timedOut bool) string {
	if timedOut || d < elapsedMin {
		return ""
	}
	return "[elapsed " + elapsedText(d) + "]"
}

// ranText: "ran 50.3s" — a mesma informação para um job que já terminou, onde o "up <desde a criação>" de antes
// continuava crescendo depois da morte do processo (e, num job longo, nenhuma outra linha dizia quanto ele durou).
func ranText(d time.Duration) string { return "ran " + elapsedText(d) }
