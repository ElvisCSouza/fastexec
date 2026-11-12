// session_files.go: o registro dos arquivos que ESTA sessão do FastExec gravou.
//
// Ele existe por causa de uma pergunta só: depois de um rename automático, quais dos arquivos tocados já
// estavam sujos no git por motivos alheios ao FastExec? Sem separar as duas origens, o aviso "modified outside"
// listaria o próprio trabalho do FastExec — os arquivos que ele acabou de gravar — e viraria ruído em 100% dos
// casos. O registro é por host+caminho, com teto, e nunca bloqueia nem altera dado: igual aos contadores de
// leitura (read_count.go), só produz aviso.
package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
)

var (
	wroteMu    sync.Mutex
	wroteFiles = map[string]bool{}

	assistedMu sync.Mutex
	assisted   = map[string]string{}
)

// sessionKey identifica um arquivo gravado. O host entra na chave: o mesmo caminho em hosts diferentes são
// arquivos diferentes (e o host remoto nem chega aqui: o rename só roda no host local).
func sessionKey(host, path string) string {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	return strings.ToLower(host + "|" + filepath.ToSlash(abs))
}

// noteWrite registra que o FastExec gravou host:path nesta sessão.
func noteWrite(host, path string) {
	key := sessionKey(host, path)
	wroteMu.Lock()
	defer wroteMu.Unlock()
	if len(wroteFiles) > 4000 {
		wroteFiles = map[string]bool{}
	}
	wroteFiles[key] = true
}

// wroteThisSession diz se o FastExec já gravou host:path nesta sessão.
func wroteThisSession(host, path string) bool {
	key := sessionKey(host, path)
	wroteMu.Lock()
	defer wroteMu.Unlock()
	return wroteFiles[key]
}

// rememberAssisted guarda que o servidor de linguagem já decidiu o conteúdo final deste arquivo.
//
// Isto é uma trava de reentrância, não um cache: o edit_file por símbolo chama o write_file internamente com o
// texto que o rename acabou de produzir, e sem esta marca o assistente rodaria de novo sobre o próprio
// resultado — renomeando outra vez o que já está renomeado, com a posição do texto velho que não existe mais.
// Guarda o hash, não o texto: o que se pergunta adiante é igualdade, e um arquivo grande não fica na memória.
func rememberAssisted(host, path, text string) {
	if text == "" {
		return
	}
	key := sessionKey(host, path)
	sum := sha256.Sum256([]byte(text))
	assistedMu.Lock()
	defer assistedMu.Unlock()
	if len(assisted) > 4000 {
		assisted = map[string]string{}
	}
	assisted[key] = hex.EncodeToString(sum[:])
}

// isAssisted diz se text é exatamente o que o servidor já tinha pedido para host:path nesta sessão.
func isAssisted(host, path, text string) bool {
	if text == "" {
		return false
	}
	key := sessionKey(host, path)
	assistedMu.Lock()
	got := assisted[key]
	assistedMu.Unlock()
	if got == "" {
		return false
	}
	sum := sha256.Sum256([]byte(text))
	return got == hex.EncodeToString(sum[:])
}
