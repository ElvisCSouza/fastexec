// fakelsp_test.go: o servidor de linguagem FALSO desta suíte.
//
// O teste do assistente automático (lsp_autoedit.go) precisa de um servidor que responda exatamente o que o caso
// pede — inclusive o que o caso NÃO quer: um rename que aponta para o homônimo errado, um pull de diagnóstico
// que nunca chega, um quickfix de import. O gopls não dá esse controle, e depender dele deixaria a suíte refém
// do que está instalado na máquina.
//
// O falso é um processo de verdade: o binário de teste roda a si mesmo (FAKELSP_TOOLS=1) e fala o protocolo por
// stdin/stdout, com framing reimplementado aqui de propósito — um falso que usasse o leitor do cliente não
// pegaria um erro de framing. O roteiro vem de um JSON em FAKELSP_CTL; sem ele o servidor é "honesto" (responde
// pelo identificador do texto, como o falso do núcleo em internal/lsp/fake_test.go).
package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// toolFakeEdit é uma edição roteirizada. File é relativo à raiz (vazio = o próprio documento da requisição),
// com barra normal; Line/Start/End são 0-based em unidades UTF-16 (o ASCII dos fixtures casa byte a byte).
type toolFakeEdit struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	New   string `json:"new"`
}

// toolFakeAction é um code action roteirizado.
type toolFakeAction struct {
	Title string         `json:"title"`
	Kind  string         `json:"kind"`
	Edits []toolFakeEdit `json:"edits"`
	Diags []toolFakeDiag `json:"diags"`
}

// toolFakeDiag é um diagnóstico roteirizado.
type toolFakeDiag struct {
	Line    int    `json:"line"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Message string `json:"message"`
}

// toolFakeCtl é o roteiro inteiro. Tudo vazio = comportamento honesto.
type toolFakeCtl struct {
	// NoPrepareNull faz o prepareRename responder null (símbolo não renomeável ali).
	NoPrepareNull bool `json:"noPrepareNull"`
	// Rename são os arquivos que o rename devolve — é assim que o teste encena o homônimo errado.
	Rename map[string][]toolFakeEdit `json:"rename"`
	// References são os usos devolvidos por textDocument/references, por arquivo.
	References map[string][]toolFakeEdit `json:"references"`
	// Actions são os code actions devolvidos para qualquer range.
	Actions []toolFakeAction `json:"actions"`
	// Format são as edições de formatação.
	Format []toolFakeEdit `json:"format"`
	// Hang são métodos que nunca recebem resposta (o chamador tem que estourar o prazo).
	Hang []string `json:"hang"`
	// NoPush desliga o publishDiagnostics depois de didOpen/didChange.
	NoPush bool `json:"noPush"`
	// NoPull tira o diagnosticProvider das capacidades (servidor só de push, como o gopls antigo).
	NoPull bool `json:"noPull"`
	// Stderr são linhas que o servidor escreve no próprio erro antes de falar o protocolo. É como os
	// servidores de verdade contam por que desistiram, e é o que a nota de "não carregou" tem que citar.
	Stderr []string `json:"stderr"`
}

// runToolFakeServer é o processo servidor. Só roda quando FAKELSP_TOOLS=1.
func runToolFakeServer() {
	var ctl toolFakeCtl
	if p := os.Getenv("FAKELSP_CTL"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &ctl)
		}
	}
	for _, line := range ctl.Stderr {
		fmt.Fprintln(os.Stderr, line)
	}
	s := &toolFakeSrv{
		out:  bufio.NewWriter(os.Stdout),
		ctl:  ctl,
		root: os.Getenv("FAKELSP_ROOT"),
		docs: map[string]string{},
	}
	in := bufio.NewReader(os.Stdin)
	for {
		body, err := readToolFakeFrame(in)
		if err != nil {
			return
		}
		var m toolFakeMsg
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if s.handle(&m) {
			return
		}
	}
}

type toolFakeMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type toolFakeSrv struct {
	out *bufio.Writer

	mu   sync.Mutex
	docs map[string]string

	ctl  toolFakeCtl
	root string
}

// ---------------------------------------------------------------------------
// Framing (independente do cliente)
// ---------------------------------------------------------------------------

func readToolFakeFrame(br *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("tool-fake-lsp: no Content-Length")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (s *toolFakeSrv) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(b))
	s.out.Write(b)
	s.out.Flush()
}

func (s *toolFakeSrv) reply(id json.RawMessage, result any) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *toolFakeSrv) replyErr(id json.RawMessage, code int, msg string) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (s *toolFakeSrv) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// ---------------------------------------------------------------------------
// Despacho
// ---------------------------------------------------------------------------

func (s *toolFakeSrv) handle(m *toolFakeMsg) (exit bool) {
	switch m.Method {
	case "initialize":
		s.reply(m.ID, map[string]any{"capabilities": s.capabilities()})
		return false
	case "initialized", "workspace/didChangeConfiguration", "$/setTrace":
		return false
	case "shutdown":
		if m.ID != nil {
			s.reply(m.ID, nil)
		}
		return false
	case "exit":
		return true
	case "textDocument/didOpen", "textDocument/didChange":
		s.didOpen(m.Params)
		return false
	case "textDocument/didClose":
		s.didClose(m.Params)
		return false
	}
	if m.ID == nil {
		return false
	}
	for _, h := range s.ctl.Hang {
		if h == m.Method {
			return false // nunca responde: é o caso "prazo estourado"
		}
	}
	s.operation(m)
	return false
}

func (s *toolFakeSrv) capabilities() map[string]any {
	caps := map[string]any{
		"textDocumentSync":           1,
		"referencesProvider":         true,
		"renameProvider":             map[string]any{"prepareProvider": true},
		"codeActionProvider":         true,
		"documentFormattingProvider": true,
		"diagnosticProvider":         map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false},
	}
	if s.ctl.NoPull {
		delete(caps, "diagnosticProvider")
	}
	return caps
}

func (s *toolFakeSrv) operation(m *toolFakeMsg) {
	id := m.ID
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
		NewName string `json:"newName"`
		Context struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
			End struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"end"`
		} `json:"range"`
	}
	_ = json.Unmarshal(m.Params, &p)

	switch m.Method {
	case "textDocument/prepareRename":
		if s.ctl.NoPrepareNull {
			s.reply(id, nil)
			return
		}
		text := s.text(p.TextDocument.URI)
		name, rng, ok := toolFakeIdentRange(text, p.Position.Line, p.Position.Character)
		if !ok {
			s.reply(id, nil)
			return
		}
		s.reply(id, map[string]any{"range": rng, "placeholder": name})

	case "textDocument/rename":
		if len(s.ctl.Rename) == 0 {
			// Honesto: troca todas as ocorrências do identificador no próprio arquivo.
			text := s.text(p.TextDocument.URI)
			name, _, ok := toolFakeIdentRange(text, p.Position.Line, p.Position.Character)
			if !ok {
				s.reply(id, nil)
				return
			}
			edits := []map[string]any{}
			for _, o := range toolFakeOccurrences(text, name) {
				edits = append(edits, map[string]any{"range": o, "newText": p.NewName})
			}
			s.reply(id, map[string]any{"changes": map[string]any{p.TextDocument.URI: edits}})
			return
		}
		changes := map[string]any{}
		for file, list := range s.ctl.Rename {
			uri := p.TextDocument.URI
			if file != "" {
				// Arquivo alvo: as posições roteirizadas são as que o teste escreveu no fixture.
				uri = pathURI(s.file(file))
			}
			edits := []map[string]any{}
			for _, e := range list {
				edits = append(edits, map[string]any{
					"range":   toolFakeRange(e.Line, e.Start, e.End),
					"newText": e.New,
				})
			}
			changes[uri] = edits
		}
		s.reply(id, map[string]any{"changes": changes})

	case "textDocument/references":
		var loc []map[string]any
		if len(s.ctl.References) > 0 {
			// Ordem determinística: a PRIMEIRA posição da lista inteira é a declaração, e é ela que sai
			// quando includeDeclaration é falso (é assim que todo servidor responde).
			files := make([]string, 0, len(s.ctl.References))
			for f := range s.ctl.References {
				files = append(files, f)
			}
			sort.Strings(files)
			for _, file := range files {
				uri := p.TextDocument.URI
				if file != "" {
					uri = pathURI(s.file(file))
				}
				for _, e := range s.ctl.References[file] {
					loc = append(loc, map[string]any{"uri": uri, "range": toolFakeRange(e.Line, e.Start, e.End)})
				}
			}
		} else {
			// Honesto: as ocorrências do identificador no próprio arquivo.
			text := s.text(p.TextDocument.URI)
			name, _, ok := toolFakeIdentRange(text, p.Position.Line, p.Position.Character)
			if ok {
				for _, o := range toolFakeOccurrences(text, name) {
					loc = append(loc, map[string]any{"uri": p.TextDocument.URI, "range": o})
				}
			}
		}
		if !p.Context.IncludeDeclaration && len(loc) > 0 {
			loc = loc[1:]
		}
		if loc == nil {
			loc = []map[string]any{}
		}
		s.reply(id, loc)

	case "textDocument/codeAction":
		out := []map[string]any{}
		for _, a := range s.ctl.Actions {
			grouped := map[string][]map[string]any{}
			for _, e := range a.Edits {
				uri := p.TextDocument.URI
				if e.File != "" {
					uri = pathURI(s.file(e.File))
				}
				grouped[uri] = append(grouped[uri], map[string]any{
					"range":   toolFakeRange(e.Line, e.Start, e.End),
					"newText": e.New,
				})
			}
			changes := map[string]any{}
			for uri, list := range grouped {
				changes[uri] = list
			}
			item := map[string]any{
				"title": a.Title,
				"kind":  a.Kind,
				"edit":  map[string]any{"changes": changes},
			}
			if len(a.Diags) > 0 {
				ds := []map[string]any{}
				for _, d := range a.Diags {
					ds = append(ds, map[string]any{
						"range":    toolFakeRange(d.Line, d.Start, d.End),
						"severity": 1,
						"message":  d.Message,
					})
				}
				item["diagnostics"] = ds
			}
			out = append(out, item)
		}
		s.reply(id, out)

	case "textDocument/formatting":
		edits := []map[string]any{}
		for _, e := range s.ctl.Format {
			edits = append(edits, map[string]any{
				"range":   toolFakeRange(e.Line, e.Start, e.End),
				"newText": e.New,
			})
		}
		s.reply(id, edits)

	case "textDocument/diagnostic":
		s.reply(id, map[string]any{
			"kind":  "full",
			"items": toolFakeDiagnostics(s.text(p.TextDocument.URI)),
		})

	default:
		s.replyErr(id, -32601, "tool-fake-lsp: unsupported "+m.Method)
	}
}

// ---------------------------------------------------------------------------
// Documentos
// ---------------------------------------------------------------------------

func (s *toolFakeSrv) didOpen(params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		return
	}
	text := p.TextDocument.Text
	if len(p.ContentChanges) > 0 {
		text = p.ContentChanges[len(p.ContentChanges)-1].Text
	}
	s.mu.Lock()
	s.docs[p.TextDocument.URI] = text
	s.mu.Unlock()
	if s.ctl.NoPush {
		return
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         p.TextDocument.URI,
		"diagnostics": toolFakeDiagnostics(text),
	})
}

func (s *toolFakeSrv) didClose(params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		return
	}
	s.mu.Lock()
	delete(s.docs, p.TextDocument.URI)
	s.mu.Unlock()
	if s.ctl.NoPush {
		return
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri": p.TextDocument.URI, "diagnostics": []any{},
	})
}

// text é o documento como o servidor o vê: o último texto enviado, ou o arquivo em disco.
func (s *toolFakeSrv) text(uri string) string {
	s.mu.Lock()
	t, ok := s.docs[uri]
	s.mu.Unlock()
	if ok {
		return t
	}
	b, err := os.ReadFile(uriToPath(uri))
	if err != nil {
		return ""
	}
	return string(b)
}

// file resolve um caminho relativo à raiz contra FAKELSP_ROOT. A variável existe para não depender do diretório
// de trabalho do processo (que no Windows pode voltar com nome curto, diferente do caminho que o cliente usou).
func (s *toolFakeSrv) file(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(s.root, filepath.FromSlash(rel))
}

// toolFakeDiagnostics: uma linha com "//ERR <msg>" vira erro (severity 1) e com "//WARN <msg>" vira aviso
// (severity 2). O resto é limpo, e o texto da mensagem vem do comentário — é isso que permite o teste afirmar
// "o erro antigo foi ignorado": a mesma mensagem, antes e depois da edição, não conta como nova.
func toolFakeDiagnostics(text string) []map[string]any {
	out := []map[string]any{}
	for i, line := range strings.Split(text, "\n") {
		sev, msg := 0, ""
		if j := strings.Index(line, "//ERR "); j >= 0 {
			sev, msg = 1, strings.TrimSpace(strings.TrimRight(line[j+6:], "\r"))
		} else if j := strings.Index(line, "//WARN "); j >= 0 {
			sev, msg = 2, strings.TrimSpace(strings.TrimRight(line[j+7:], "\r"))
		}
		if sev == 0 {
			continue
		}
		content := strings.TrimRight(line, "\r")
		out = append(out, map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": i, "character": 0},
				"end":   map[string]any{"line": i, "character": len(content)},
			},
			"severity": sev,
			"source":   "tool-fake-lsp",
			"message":  msg,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Texto (fixtures ASCII: posição = byte, que em ASCII é a unidade UTF-16)
// ---------------------------------------------------------------------------

func toolFakeRange(line, start, end int) map[string]any {
	return map[string]any{
		"start": map[string]any{"line": line, "character": start},
		"end":   map[string]any{"line": line, "character": end},
	}
}

func toolFakeIdentChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// toolFakeIdentRange devolve o identificador sob a posição, no formato que o protocolo usa.
func toolFakeIdentRange(text string, line, char int) (string, map[string]any, bool) {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return "", nil, false
	}
	l := strings.TrimRight(lines[line], "\r")
	if char > len(l) {
		char = len(l)
	}
	start := char
	if start >= len(l) || !toolFakeIdentChar(l[start]) {
		if start > 0 && toolFakeIdentChar(l[start-1]) {
			start--
		} else {
			return "", nil, false
		}
	}
	for start > 0 && toolFakeIdentChar(l[start-1]) {
		start--
	}
	end := start
	for end < len(l) && toolFakeIdentChar(l[end]) {
		end++
	}
	if start == end {
		return "", nil, false
	}
	return l[start:end], toolFakeRange(line, start, end), true
}

// toolFakeOccurrences acha as ocorrências de palavra inteira do nome.
func toolFakeOccurrences(text, name string) []map[string]any {
	if name == "" {
		return nil
	}
	out := []map[string]any{}
	for i := 0; i+len(name) <= len(text); {
		j := strings.Index(text[i:], name)
		if j < 0 {
			break
		}
		at := i + j
		before := at == 0 || !toolFakeIdentChar(text[at-1])
		after := at+len(name) >= len(text) || !toolFakeIdentChar(text[at+len(name)])
		if before && after {
			line, start, end := toolFakeLineChar(text, at, at+len(name))
			out = append(out, toolFakeRange(line, start, end))
		}
		i = at + len(name)
	}
	return out
}

func toolFakeLineChar(text string, from, to int) (line, start, end int) {
	line = strings.Count(text[:from], "\n")
	nl := strings.LastIndex(text[:from], "\n")
	start = from - nl - 1
	end = start + (to - from)
	return line, start, end
}

// pathURI replica lsp.PathToURI sem importar o pacote (o falso tem que ser independente do código sob teste).
func pathURI(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// uriToPath replica lsp.URItoPath.
func uriToPath(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // "/C:/x" -> "C:/x"
	}
	return filepath.FromSlash(p)
}
