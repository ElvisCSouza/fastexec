package tools

// edit_classes.go: classes nomeadas de recusa do edit_file. Cada recusa começa com "[edit_file <classe>]" para o modelo
// e o log reconhecerem a causa sem ler o resto. Todas recusam antes de qualquer escrita (tudo ou nada): nenhuma
// classe aplica a troca "mesmo assim".

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

const (
	editClassFileNotFound      = "file_not_found"     // o arquivo não existe no host
	editClassTargetNotFound    = "target_not_found"   // o texto do alvo não está no arquivo
	editClassAmbiguousTarget   = "ambiguous_target"   // o alvo aparece mais de uma vez: não há como saber qual trocar
	editClassStalePrecondition = "stale_precondition" // o arquivo mudou desde o expected_sha256 informado
)

// editRefusal monta a recusa de uma classe nomeada.
func editRefusal(class, format string, a ...any) error {
	return fmt.Errorf("[edit_file %s] %s", class, fmt.Sprintf(format, a...))
}

// contentSHA256 é o hash do texto decodificado (o mesmo que o modelo viu na leitura).
func contentSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// readEditErr classifica a falha de leitura do edit_file: arquivo ausente vira file_not_found; o resto segue como antes.
func readEditErr(path string, err error) error {
	msg := strings.ToLower(err.Error())
	if errors.Is(err, fs.ErrNotExist) || strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "cannot find the file") || strings.Contains(msg, "local file not found") {
		return editRefusal(editClassFileNotFound, "'%s' does not exist; nothing was written. To create it, use write_file.", path)
	}
	return fmt.Errorf("could not read '%s' to edit it: %w", path, err)
}

// checkExpectedSHA recusa a edição quando o arquivo lido não é o que o modelo descreveu. Sem expected, não verifica.
// A recusa mostra só o hash atual (nunca o conteúdo), para o modelo reler e tentar de novo.
func checkExpectedSHA(path, content, expected string) error {
	if expected == "" {
		return nil
	}
	cur := contentSHA256(content)
	if strings.EqualFold(strings.TrimSpace(expected), cur) {
		return nil
	}
	return editRefusal(editClassStalePrecondition,
		"'%s' changed since the expected sha256 was taken (current sha256 %s); nothing was written. Read the file again and retry with the current hash.", path, cur)
}

// ambiguousRefusal recusa um alvo que aparece n vezes, citando até cinco linhas onde ele começa.
func ambiguousRefusal(label, path, content, match string, n int) error {
	var lines []string
	for from, k := 0, 0; k < 5; k++ {
		i := strings.Index(content[from:], match)
		if i < 0 {
			break
		}
		at := from + i
		lines = append(lines, strconv.Itoa(strings.Count(content[:at], "\n")+1))
		from = at + len(match)
		if from >= len(content) {
			break
		}
	}
	where := strings.Join(lines, ", ")
	if n > len(lines) {
		where += ", ..."
	}
	return editRefusal(editClassAmbiguousTarget,
		"%s matched %d times in '%s' (starting at lines %s); nothing was written. Add surrounding lines so it matches once, or use 'symbol'.", label, n, path, where)
}
