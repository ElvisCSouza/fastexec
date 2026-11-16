package tools

// ts_project.go: camada 2 DETERMINÍSTICA para TypeScript quando NÃO há servidor de linguagem instalado. Em vez do
// typescript-language-server, usa o próprio compilador (tsc -p) no tsconfig do projeto e devolve os erros do
// arquivo editado — de forma TARDIA (goroutine + hash), para a resposta da edição não esperar o tsc. Só para
// "typescript" (.ts/.tsx/.mts/.cts/.js/.jsx), só dentro de um projeto com tsconfig.json, só o que não é sintaxe
// (a camada 1 já cobriu sintaxe). Best-effort: sem tsc, silêncio.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tsProjectBudget: teto do tsc de projeto em segundo plano (projeto grande demora; o que passa é descartado).
const tsProjectBudget = 60 * time.Second

// tsProjectRoot sobe do arquivo até o tsconfig.json mais próximo (o tsc -p precisa dele). "" se não houver.
func tsProjectRoot(abs string) string {
	for d := filepath.Dir(abs); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "tsconfig.json")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

var tsReDiag = regexp.MustCompile(`^(.*)\((\d+),(\d+)\): error (TS\d+): (.*)$`)

type tsDiag struct {
	file string
	line int
	col  int
	code string
	msg  string
}

// tsProjectDiags roda tsc -p no root e devolve os diagnósticos. ok=false quando o tsc não existe ou não respondeu
// (aí nada é afirmado).
func tsProjectDiags(root string) ([]tsDiag, bool) {
	exe := extToolPath("tsc")
	if exe == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), tsProjectBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--noEmit", "--pretty", "false", "-p", root)
	cmd.Dir = root // o tsc imprime os caminhos relativos ao projeto
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, false
	}
	// err != nil é o esperado quando há erros de compilação; o conteúdo é que importa.
	_ = err
	var out []tsDiag
	for _, l := range strings.Split(buf.String(), "\n") {
		m := tsReDiag.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		out = append(out, tsDiag{file: m[1], line: line, col: col, code: m[4], msg: m[5]})
	}
	return out, true
}

// tsRobustez: uma corrida de tsc por root de cada vez (o tsc de projeto é pesado; não empilha em edições seguidas).
var tsInFlight sync.Map

// tsHandOff entrega ao fundo a checagem de tipos do projeto para o arquivo recém-gravado. O resultado só é
// publicado se o arquivo não mudou (hash) e chega na próxima chamada, como o diagnóstico tardio do servidor.
func (a *autoEdit) tsHandOff(text string) {
	if a == nil || a.lang != "typescript" {
		return
	}
	root := tsProjectRoot(a.abs)
	if root == "" {
		return
	}
	if _, busy := tsInFlight.LoadOrStore(root, true); busy {
		return // já há uma corrida para este projeto; a próxima edição pega o resultado
	}
	sum := sha256.Sum256([]byte(text))
	abs := a.abs
	go func() {
		defer tsInFlight.Delete(root)
		diags, ok := tsProjectDiags(root)
		if !ok {
			return
		}
		b, err := os.ReadFile(abs)
		if err != nil || sha256.Sum256(b) != sum {
			return // o arquivo mudou: o resultado é de uma versão que já não existe
		}
		rel := relToRoot(root, abs)
		if rel == "" {
			rel = filepath.Base(abs)
		}
		base := filepath.Clean(abs)
		var forFile []tsDiag
		for _, d := range diags {
			if !tsDiagIsFile(root, d.file, base) {
				continue
			}
			if code, e := strconv.Atoi(strings.TrimPrefix(d.code, "TS")); e == nil && tsSyntaxCode(code) {
				continue // sintaxe é da camada 1
			}
			forFile = append(forFile, d)
		}
		if len(forFile) == 0 {
			queueLate("⏱ TS (tsc, sem servidor de typescript) " + rel + ": ✓ compila (tipos verificados)")
			return
		}
		for i, d := range forFile {
			if i == validateMaxErrs {
				queueLate(fmt.Sprintf("⏱ TS (tsc, sem servidor) %s: %s", rel, moreErrs(len(forFile)-i)))
				break
			}
			src := ""
			if b, err := os.ReadFile(abs); err == nil {
				src = srcLine(string(b), d.line)
			}
			queueLate("⏱ TS (tsc, sem servidor) " + errLine(rel, d.line, d.col, d.code, d.msg, src, "")[len("✗ "):])
		}
	}()
}

// tsDiagIsFile casa o caminho do diagnóstico (relativo ao root, ou absoluto) com o arquivo editado.
func tsDiagIsFile(root, diagFile string, base string) bool {
	f := diagFile
	if !filepath.IsAbs(f) {
		f = filepath.Join(root, filepath.FromSlash(f))
	}
	return strings.EqualFold(filepath.Clean(f), base)
}
