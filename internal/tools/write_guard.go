// write_guard.go: guards de escrita. write_file recusa regravar arquivo existente e não vazio sem overwrite (o modelo
// gera o arquivo inteiro como saída, o token mais caro; edit_file manda só o trecho). edit_file casa o target mesmo
// quando ele vem com LF e o arquivo está em CRLF (ou o contrário), em vez de falhar e custar outra ida e volta.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/transport"
)

// existingNonEmpty diz se o arquivo já existe com conteúdo; size = -1 quando o host remoto não informa o tamanho.
func existingNonEmpty(target transport.HostTarget, path string) (int64, bool) {
	if target.IsLocal {
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			return 0, false
		}
		return fi.Size(), true
	}
	if target.TCPAddr == "" {
		return 0, false
	}
	p := strings.ReplaceAll(filepath.ToSlash(path), "'", `'\''`)
	res, err := transport.ExecDaemon(target.TCPAddr, "test -s '"+p+"'", transport.DefaultTimeout)
	if err != nil || res == nil || res.ExitCode != 0 {
		return 0, false
	}
	return -1, true
}

func overwriteRefused(path string, size int64) error {
	sz := ""
	if size >= 0 {
		sz = fmt.Sprintf(" (%.1f KB)", float64(size)/1024)
	}
	return fmt.Errorf("[FastExec Guard] '%s' already exists%s: change parts of it with 'edit_file' (several changes fit in one call via 'edits'); to replace the whole file pass overwrite: true", path, sz)
}

// matchEOL devolve target e replacement ajustados ao fim de linha do conteúdo quando só assim o target é achado.
func matchEOL(content, target, replacement string) (string, string) {
	if strings.Contains(content, target) {
		return target, replacement
	}
	if strings.Contains(content, "\r\n") && strings.Contains(target, "\n") && !strings.Contains(target, "\r\n") {
		t := strings.ReplaceAll(target, "\n", "\r\n")
		if strings.Contains(content, t) {
			return t, strings.ReplaceAll(strings.ReplaceAll(replacement, "\r\n", "\n"), "\n", "\r\n")
		}
	}
	if strings.Contains(target, "\r\n") {
		t := strings.ReplaceAll(target, "\r\n", "\n")
		if strings.Contains(content, t) {
			return t, strings.ReplaceAll(replacement, "\r\n", "\n")
		}
	}
	return target, replacement
}
