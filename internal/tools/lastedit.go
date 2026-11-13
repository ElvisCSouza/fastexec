package tools

// lastedit.go: desfazer a ÚLTIMA gravação do FastExec, sem git e sem cópia em disco.
//
// Cada write_file/edit_file guarda, em memória, os arquivos que ELE (e o rename/assinatura automáticos que vieram junto)
// mudaram: o conteúdo de antes e o hash do que foi gravado. Só a última operação fica; a seguinte a substitui. `fastexec
// undo`, dado ao exec, restaura esses arquivos. Reiniciar o daemon esvazia o espaço — é de propósito: é um botão de
// "errei agora", não um histórico.
//
// A segurança é o hash: um arquivo que mudou depois da gravação (outra edição, o usuário) não é sobrescrito, e o
// retorno diz qual ficou de fora.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"homelab/fastexec/internal/graph"
)

const (
	undoMaxFileBytes  = 8 << 20
	undoMaxTotalBytes = 32 << 20
)

// UndoCommand é a linha que o rodapé manda a LLM executar.
const UndoCommand = "fastexec undo"

type undoFile struct {
	path    string
	existed bool
	before  []byte
	wrote   [sha256.Size]byte
	gone    bool // depois da gravação o arquivo não existia (ex.: removido)
}

type undoOp struct {
	files  []*undoFile
	seen   map[string]bool
	bytes  int
	tooBig bool
}

var undoState struct {
	mu      sync.Mutex
	pending *undoOp // a operação em curso (entre beginUndoOp e sealUndoOp)
	last    *undoOp // a última operação concluída
}

// beginUndoOp abre a operação desta chamada. Duas chamadas simultâneas compartilham o rascunho: no pior caso o undo
// restaura a união das duas, e o hash protege qualquer arquivo que não bata.
func beginUndoOp() {
	undoState.mu.Lock()
	undoState.pending = &undoOp{seen: map[string]bool{}}
	undoState.mu.Unlock()
}

// captureUndo guarda o conteúdo atual de path, uma vez por operação, ANTES de qualquer byte ser gravado.
func captureUndo(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	undoState.mu.Lock()
	defer undoState.mu.Unlock()
	op := undoState.pending
	if op == nil || op.tooBig || op.seen[abs] {
		return
	}
	op.seen[abs] = true
	f := &undoFile{path: abs}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		if st.Size() > undoMaxFileBytes || op.bytes+int(st.Size()) > undoMaxTotalBytes {
			op.tooBig = true
			return
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			op.tooBig = true // sem o conteúdo de antes não há como prometer o desfazer
			return
		}
		f.existed, f.before = true, b
		op.bytes += len(b)
	}
	op.files = append(op.files, f)
}

// sealUndoOp fecha a operação: lê o que ficou no disco (hash) e a torna a "última". Operação que não capturou nada
// (a edição falhou antes de gravar) deixa a anterior intacta.
func sealUndoOp() {
	undoState.mu.Lock()
	defer undoState.mu.Unlock()
	op := undoState.pending
	undoState.pending = nil
	if op == nil || len(op.files) == 0 && !op.tooBig {
		return
	}
	for _, f := range op.files {
		b, err := os.ReadFile(f.path)
		if err != nil {
			f.gone = true
			continue
		}
		f.wrote = sha256.Sum256(b)
	}
	undoState.last = op
}

// isUndoCommand reconhece `fastexec undo` dado ao exec (como linha de comando ou como argv).
func isUndoCommand(command string, argv []string) bool {
	if len(argv) > 0 {
		command = strings.Join(argv, " ")
	}
	c := strings.ToLower(strings.Join(strings.Fields(command), " "))
	return c == UndoCommand || c == "fastexec.exe undo" || c == "fx undo"
}

// lastEditUndo restaura a última operação e devolve o texto da resposta.
func lastEditUndo() string {
	undoState.mu.Lock()
	op := undoState.last
	undoState.last = nil
	undoState.mu.Unlock()
	if op == nil {
		return "nothing to undo: FastExec keeps only the last edit, in memory, and there is none (the daemon may have restarted)."
	}
	if op.tooBig {
		return "cannot undo: the last edit touched files too large to keep in memory."
	}
	var restored, skipped []string
	for i := len(op.files) - 1; i >= 0; i-- {
		f := op.files[i]
		cur, err := os.ReadFile(f.path)
		switch {
		case err == nil && !f.gone && sha256.Sum256(cur) == f.wrote, errors.Is(err, os.ErrNotExist) && f.gone:
		default:
			skipped = append(skipped, filepath.Base(f.path))
			continue
		}
		if f.existed {
			if err := os.WriteFile(f.path, f.before, 0o644); err != nil {
				skipped = append(skipped, filepath.Base(f.path))
				continue
			}
		} else if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			skipped = append(skipped, filepath.Base(f.path))
			continue
		}
		graph.Touch(f.path)
		restored = append(restored, filepath.Base(f.path))
	}
	msg := fmt.Sprintf("undone: restored %d file(s): %s (the last edit; nothing older is kept).", len(restored), strings.Join(reverseStrings(restored), ", "))
	if len(skipped) > 0 {
		msg += fmt.Sprintf(" Not restored, changed since FastExec wrote them: %s.", strings.Join(skipped, ", "))
	}
	return msg
}

func reverseStrings(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
