package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/telemetry"
)

// savLines conta as linhas de um texto (a última sem quebra também conta).
func savLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// retryNote descreve a chamada que o FastExec corrigiu ou respondeu antes de ela falhar.
func retryNote(label, out string) string {
	o := strings.Join(strings.Fields(out), " ")
	if len(o) > 200 {
		o = o[:200] + "…"
	}
	return fmt.Sprintf("Intercepted %q and fixed or answered it on the spot. FastExec replied: %s", label, o)
}

// cleanNote descreve o que a limpeza da saída tirou, com números.
func cleanNote(raw, clean string) string {
	parts := []string{fmt.Sprintf("%d lines → %d", savLines(raw), savLines(clean))}
	if n := strings.Count(raw, "\x1b["); n > 0 {
		parts = append(parts, fmt.Sprintf("%d color/cursor codes removed", n))
	}
	if n := strings.Count(raw, "\r") - strings.Count(raw, "\r\n"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d progress redraws collapsed", n))
	}
	return strings.Join(parts, " · ")
}

// localFileSize devolve o tamanho de um arquivo do host local (0 se for remoto ou não existir).
func localFileSize(host, path string) int {
	if host == "" && filepath.VolumeName(path) != "" {
		host = "local"
	}
	t, err := ResolveTargetHost(host, path)
	if err != nil || !t.IsLocal {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return int(info.Size())
}

// noteEditSaving anota a economia de editar por trecho (regra "edit"): a ferramenta comum reenviaria o arquivo
// inteiro; o patch é o que o agente mandou.
func noteEditSaving(fileBytes, patchBytes int, path string) {
	telemetry.NoteSavingDetail(telemetry.CatEdit, fileBytes, patchBytes,
		fmt.Sprintf("edited by patch: %d B file, %d B of changes sent", fileBytes, patchBytes))
}

// noteReadSaving: ler um trecho (ou um resumo) devolve menos que o arquivo inteiro, que é o que um cat/Read faria.
func noteReadSaving(host, path, out string) {
	size := localFileSize(host, path)
	if size <= len(out) {
		return
	}
	note := fmt.Sprintf("returned %d lines", savLines(out))
	if size <= 4<<20 {
		if b, err := os.ReadFile(path); err == nil {
			note = fmt.Sprintf("file has %d lines; returned %d", savLines(string(b)), savLines(out))
		}
	}
	telemetry.NoteSavingDetail(telemetry.CatSlice, size, len(out), note)
}
