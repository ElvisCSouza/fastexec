package mcp

// error_cause.go: o que a telemetria grava quando uma tool falha. O erro do Go costuma ser genérico ("comando falhou
// com código 1"); a causa está na resposta (stderr do comando). Gravar a primeira linha útil dela permite agrupar os
// erros mais frequentes e medir se uma dica de remediação resolveu.

import (
	"regexp"
	"strconv"
	"strings"
)

const causeMax = 300

var reExitPrefix = regexp.MustCompile(`^\[exit (-?\d+)\]\s*`)

// errorCause devolve a mensagem para error_message e o exit code real (o de "[exit N]" quando houver; senão 1).
func errorCause(err error, outText string) (string, int) {
	msg := err.Error()
	code := 1
	text := strings.TrimSpace(outText)
	if m := reExitPrefix.FindStringSubmatch(text); m != nil {
		if n, perr := strconv.Atoi(m[1]); perr == nil {
			code = n
		}
		text = strings.TrimSpace(text[len(m[0]):])
	}
	if text == "" || text == msg {
		return msg, code
	}
	cause := ""
	for _, line := range strings.Split(text, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			cause = l
			break
		}
	}
	if r := []rune(cause); len(r) > causeMax {
		cause = string(r[:causeMax]) + "…"
	}
	return msg + ": " + cause, code
}
