package tools

import (
	"os"
	"path/filepath"

	"homelab/fastexec/internal/telemetry"
)

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

// noteReadSaving: ler um trecho (ou um resumo) devolve menos que o arquivo inteiro, que é o que um cat/Read faria.
func noteReadSaving(host, path, out string) {
	if size := localFileSize(host, path); size > len(out) {
		telemetry.NoteSaving(telemetry.CatSlice, size, len(out))
	}
}

// noteEditSaving: trocar um trecho envia só o texto da troca; sem edit_file seria preciso reenviar o arquivo inteiro.
func noteEditSaving(args EditFileArgs) {
	sent := len(args.Target) + len(args.Replacement)
	for _, op := range args.Edits {
		sent += len(op.Target) + len(op.Replacement)
	}
	if size := localFileSize(args.Host, args.Path); size > sent {
		telemetry.NoteSaving(telemetry.CatEdit, size, sent)
	}
}
