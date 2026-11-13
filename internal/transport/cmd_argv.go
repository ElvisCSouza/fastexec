package transport

import (
	"path/filepath"
	"strings"
)

// cmdCPayload reconhece o argv ["cmd", "/c", "<linha com aspas>"] e devolve a linha. A LLM escreve assim para ter
// "um shell só dela", mas o Go escapa as aspas da string (\") e o cmd.exe não conhece esse escape: o findstr recebe
// /C:\texto\ e não acha nada, nem no arquivo que contém o texto. O resultado é um "zero ocorrências" FALSO — no CS4 v16
// uma verificação assim levou o modelo a repetir cinco buscas atrás de um controle que "não achava" o que existe.
// A linha vai ao cmd como linha crua (a mesma via do campo command), que é o que o autor quis dizer.
func cmdCPayload(argv []string) (string, bool) {
	if len(argv) < 3 {
		return "", false
	}
	exe := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(argv[0], `\`, "/")), ".exe"))
	if exe != "cmd" {
		return "", false
	}
	rest := argv[1:]
	if strings.EqualFold(rest[0], "/s") {
		rest = rest[1:]
	}
	if len(rest) != 2 || !strings.EqualFold(rest[0], "/c") {
		return "", false
	}
	if !strings.Contains(rest[1], `"`) {
		return "", false // sem aspas o escape do Go não muda nada: segue o caminho de sempre
	}
	return rest[1], true
}
