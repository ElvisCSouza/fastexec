package graph

// markdown.go: grafo de documentação. Um Markdown vira nós (uma seção por cabeçalho ATX, kind=doc_section, nome
// "arquivo > H1 > H2") e arestas de saída, tudo por regra determinística, nunca por LLM:
//   - doc_link   : [x](outro.md#ancora) entre Markdowns, caminho relativo à pasta do próprio arquivo (como no
//                  GitHub), então uma referência quebrada é mesmo uma referência quebrada;
//   - doc_wikilink: [[Pagina]] do Obsidian, resolvido por caminho ou por nome de arquivo único no repositório;
//   - doc_mentions: `Nome` / `pkg.Nome()` que casa EXATAMENTE com o nome de um símbolo já indexado, ou um caminho
//                  de arquivo do repositório (relativo à pasta do MD; na falta, relativo à raiz). Bloco de código
//                  ( cerca ``` ) só gera menção de caminho, porque ali identificador solto é código de exemplo.
// Front matter YAML não gera nó nem menção. O alvo da aresta é o caminho/nome (a âncora fica de fora): quem
// responde "onde isso é citado" é o par (source_file, source_line).

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"homelab/fastexec/internal/codescan"
)

const (
	// maxDocSize: MD maior que isso não é indexado (documentação gerada, log colado).
	maxDocSize = 512 << 10
	// maxDocRefs: teto de referências extraídas de um único MD (uma aresta por relação+alvo no fim das contas).
	maxDocRefs = 500
	// maxDocCite: documentos mostrados na linha "docs:" antes do "(+K)".
	maxDocCite = 3
	// maxBrokenRefs: referências quebradas listadas no outline antes do "(+K more)".
	maxBrokenRefs = 5

	// RelDocLink, RelDocWiki, RelDocMention são os relation_type das arestas de documentação; KindDocSection é o
	// kind dos nós de seção.
	RelDocLink     = "doc_link"
	RelDocWiki     = "doc_wikilink"
	RelDocMention  = "doc_mentions"
	KindDocSection = "doc_section"
)

// DocSection é uma seção de um Markdown: do cabeçalho até a linha anterior ao próximo.
type DocSection struct {
	Name    string // "docs/x.md > FastExec > Instalação"
	Header  string // linha do cabeçalho, crua e trimada ("## Instalação")
	Text    string // texto do cabeçalho sem "#"
	Line    int
	EndLine int
	Level   int
}

// DocRef é uma referência de saída de um Markdown (link, wikilink ou menção entre crases).
type DocRef struct {
	Line   int
	Kind   string // RelDocLink | RelDocWiki | RelDocMention
	Target string // como escrito no arquivo
}

var (
	reHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$`)
	reLink    = regexp.MustCompile(`!?\[[^\]\n]*\]\([ \t]*<?([^)\s>]+)>?`)
	reWiki    = regexp.MustCompile(`\[\[([^\]\n|#]*)(?:#[^\]\n|]*)?(?:\|[^\]\n]*)?\]\]`)
	reSpan    = regexp.MustCompile("`([^`\n]+)`")
	// rePathToken acha candidatos a caminho dentro de bloco de código; a existência do arquivo decide.
	rePathToken = regexp.MustCompile(`[A-Za-z0-9_@./\\-]*[A-Za-z0-9_@/\\-]\.[A-Za-z0-9]{1,8}`)
	reLineSuf   = regexp.MustCompile(`^(.*?):\d+(?:-\d+)?$`)
)

// ParseMarkdown extrai as seções e as referências de saída de um Markdown. rel é o caminho relativo ao
// repositório (com '/'), usado para nomear as seções e para resolver link relativo.
func ParseMarkdown(rel string, src []byte) ([]DocSection, []DocRef) {
	rel = filepath.ToSlash(rel)
	lines := splitLines(src)
	var heads []DocSection
	var refs []DocRef
	fence := ""
	i := frontMatterEnd(lines)
	for ; i < len(lines); i++ {
		n := i + 1
		line := lines[i]
		if fence != "" {
			if closesFence(strings.TrimSpace(line), fence) {
				fence = ""
				continue
			}
			for _, tk := range pathTokens(line) {
				refs = append(refs, DocRef{Line: n, Kind: RelDocMention, Target: tk})
			}
			continue
		}
		if len(refs) >= maxDocRefs {
			break
		}
		t := strings.TrimSpace(line)
		if f := openingFence(t); f != "" {
			fence = f
			continue
		}
		if m := reHeading.FindStringSubmatch(line); m != nil {
			text := strings.TrimRight(strings.TrimSpace(m[2]), "# \t")
			heads = append(heads, DocSection{Header: t, Text: text, Line: n, Level: len(m[1])})
			continue
		}
		for _, m := range reLink.FindAllStringSubmatch(line, -1) {
			refs = append(refs, DocRef{Line: n, Kind: RelDocLink, Target: m[1]})
		}
		for _, m := range reWiki.FindAllStringSubmatch(line, -1) {
			if p := strings.TrimSpace(m[1]); p != "" {
				refs = append(refs, DocRef{Line: n, Kind: RelDocWiki, Target: p})
			}
		}
		for _, m := range reSpan.FindAllStringSubmatch(line, -1) {
			for _, tk := range spanTokens(m[1]) {
				refs = append(refs, DocRef{Line: n, Kind: RelDocMention, Target: tk})
			}
		}
	}

	// nomes: "arquivo > H1 > H2" (o caminho de cabeçalhos), subindo a pilha pelo nível
	level := map[int]string{}
	secs := make([]DocSection, 0, len(heads))
	if len(heads) == 0 {
		if end := lastContent(lines, 1, len(lines)); end > 0 {
			secs = append(secs, DocSection{Name: rel, Header: rel, Line: 1, EndLine: end})
		}
		return secs, refs
	}
	for i, h := range heads {
		level[h.Level] = h.Text
		for l := h.Level + 1; l <= 6; l++ {
			delete(level, l)
		}
		var parts []string
		for l := 1; l <= h.Level; l++ {
			if t := level[l]; t != "" {
				parts = append(parts, t)
			}
		}
		end := len(lines)
		if i+1 < len(heads) {
			end = heads[i+1].Line - 1
		}
		h.Name = rel + " > " + strings.Join(parts, " > ")
		h.EndLine = lastContent(lines, h.Line, end)
		secs = append(secs, h)
	}
	return secs, refs
}

// DocDecls devolve as seções de um Markdown como declarações (usado por code_outline).
func DocDecls(rel string, src []byte) []codescan.Decl {
	secs, _ := ParseMarkdown(rel, src)
	decls := make([]codescan.Decl, 0, len(secs))
	for _, s := range secs {
		sig := s.Header
		if sig == "" {
			sig = rel
		}
		decls = append(decls, codescan.Decl{Name: s.Name, Kind: KindDocSection, Sig: sig, Line: s.Line, EndLine: s.EndLine, Exported: true})
	}
	return decls
}

// DocOutline devolve as seções de um Markdown e a linha "docs: N broken refs: ..." (vazia quando não há quebra).
// root vazio (fora de repositório, host remoto) devolve só as seções. Não indexa nada: se o repositório não está
// no grafo, só a existência em disco é conferida — sem índice não há âncora para validar.
func DocOutline(root, rel string, src []byte) ([]codescan.Decl, string) {
	decls := DocDecls(rel, src)
	if root == "" {
		return decls, ""
	}
	ix := readIndex(root)
	if ix == nil {
		return decls, ""
	}
	_, refs := ParseMarkdown(rel, src)
	var broken []string
	for _, r := range refs {
		if r.Kind == RelDocMention {
			continue
		}
		if _, b := ix.refTarget(rel, r); b != "" {
			broken = append(broken, fmt.Sprintf("%s:%d -> %s (not found)", rel, r.Line, b))
		}
	}
	if len(broken) == 0 {
		return decls, ""
	}
	line := fmt.Sprintf("docs: %d broken refs: %s", len(broken), strings.Join(broken[:min(len(broken), maxBrokenRefs)], "; "))
	if len(broken) > maxBrokenRefs {
		line += fmt.Sprintf(" (+%d more)", len(broken)-maxBrokenRefs)
	}
	return decls, line
}

// readIndex lê o índice do repositório sem atualizá-lo (code_outline não indexa): nil quando o grafo nunca foi
// usado ou o repositório não está indexado.
func readIndex(root string) *docIndex {
	if !exists() || !indexed(root) {
		return nil
	}
	d, err := store()
	if err != nil {
		return nil
	}
	ix, err := loadDocIndex(d, repoKey(root), root, true)
	if err != nil {
		return nil
	}
	return ix
}

// --- resolução ---

// docIndex são os conjuntos com que uma referência é resolvida: arquivos indexados, quais são Markdown, as seções
// de cada Markdown (âncoras) e os nomes de símbolos.
type docIndex struct {
	root     string
	files    map[string]bool
	docs     map[string]bool
	sections map[string]map[string]bool
	symbols  map[string]bool
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func loadDocIndex(q querier, key, root string, withSections bool) (*docIndex, error) {
	ix := &docIndex{root: root, files: map[string]bool{}, docs: map[string]bool{}, symbols: map[string]bool{}}
	rows, err := q.Query(`SELECT file_path FROM code_files WHERE repo_root = ?`, key)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			ix.files[f] = true
			if codescan.IsDoc(f) {
				ix.docs[f] = true
			}
		}
	}
	rows.Close()
	rows, err = q.Query(`SELECT DISTINCT name FROM code_symbols WHERE repo_root = ? AND kind != ?`, key, KindDocSection)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			ix.symbols[n] = true
		}
	}
	rows.Close()
	if !withSections {
		return ix, nil
	}
	ix.sections = map[string]map[string]bool{}
	rows, err = q.Query(`SELECT file_path, name FROM code_symbols WHERE repo_root = ? AND kind = ?`, key, KindDocSection)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f, name string
		if rows.Scan(&f, &name) != nil {
			continue
		}
		if ix.sections[f] == nil {
			ix.sections[f] = map[string]bool{}
		}
		if _, a := docAnchor(name); a != "" {
			ix.sections[f][a] = true
		}
	}
	rows.Close()
	return ix, nil
}

// refTarget resolve uma referência para o alvo indexado (caminho relativo ao repositório, ou nome de símbolo).
// Devolve "" quando não há alvo (link externo, crase que não casa com nada) e a segunda string preenchida quando
// a referência é um link/wikilink quebrado (o texto mostrado no aviso).
func (ix *docIndex) refTarget(rel string, r DocRef) (string, string) {
	switch r.Kind {
	case RelDocLink:
		p, frag := splitFragment(r.Target)
		if !looksLikePath(p) {
			return "", ""
		}
		t := linkPath(rel, p)
		if t == "" {
			return "", ""
		}
		if !ix.exists(t) { //nem no índice nem no disco: referência quebrada
			return "", r.Target
		}
		if !ix.files[t] {
			return "", ""
		}
		if frag != "" && ix.anchorMissing(t, frag) {
			return "", r.Target
		}
		if !codescan.IsDoc(t) { // só link entre Markdowns vira aresta
			return "", ""
		}
		return t, ""
	case RelDocWiki:
		t := ix.resolveWiki(r.Target)
		if t == "" {
			return "", r.Target
		}
		return t, ""
	default: // menção entre crases
		if t := linkPath(rel, r.Target); t != "" && looksLikePath(r.Target) && ix.files[t] {
			return t, ""
		}
		if t := cleanRel(r.Target); t != "" && looksLikePath(r.Target) && ix.files[t] {
			return t, ""
		}
		if strings.ContainsAny(r.Target, "/\\") {
			return "", ""
		}
		if n := lastName(r.Target); ix.symbols[n] {
			return n, ""
		}
		return "", ""
	}
}

// anchorMissing diz se a âncora não corresponde a nenhuma seção do arquivo alvo; arquivo sem seções no índice
// (recém-criado, ainda não indexado) não é acusado.
func (ix *docIndex) anchorMissing(file, frag string) bool {
	secs, indexed := ix.sections[file]
	if !indexed {
		return false
	}
	return !secs[slug(frag)]
}

func (ix *docIndex) exists(rel string) bool {
	if ix.files[rel] {
		return true
	}
	if ix.root == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(ix.root, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir()
}

// resolveWiki resolve [[Pagina]]: caminho no repositório ou, sem barra, o único arquivo com esse nome (empate
// resolvido pelo menor caminho em ordem alfabética, para o resultado não depender da ordem do mapa).
func (ix *docIndex) resolveWiki(page string) string {
	p := strings.TrimSpace(filepath.ToSlash(page))
	if p == "" {
		return ""
	}
	if strings.ContainsAny(p, "/\\") {
		if !codescan.IsDoc(p) {
			p += ".md"
		}
		if q := cleanRel(p); ix.docs[q] {
			return q
		}
	}
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	var found []string
	for d := range ix.docs {
		if strings.EqualFold(strings.TrimSuffix(path.Base(d), path.Ext(d)), base) {
			found = append(found, d)
		}
	}
	sort.Strings(found)
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

// docAnchor separa o nome de uma seção ("docs/x.md > A > B") no arquivo e na âncora do último cabeçalho.
func docAnchor(section string) (file, anchor string) {
	if section == "" {
		return "", ""
	}
	i := strings.Index(section, " > ")
	if i < 0 {
		return section, ""
	}
	last := section[i+3:]
	if j := strings.LastIndex(last, " > "); j >= 0 {
		last = last[j+3:]
	}
	return section[:i], slug(last)
}

type docCite struct{ file, anchor string }

// docCiteLine monta "\ndocs: a.md#x, b.md (+K)" a partir de (arquivo, linha da menção). Vazio quando não há nada.
func docCiteLine(rows *sql.Rows) (string, error) {
	defer rows.Close()
	var list []docCite
	seen := map[string]bool{}
	for rows.Next() {
		var f, sec string
		var line int
		if err := rows.Scan(&f, &line, &sec); err != nil {
			return "", err
		}
		file, anchor := docAnchor(sec)
		if file == "" {
			file = f
		}
		k := file + "#" + anchor
		if seen[k] {
			continue
		}
		seen[k] = true
		list = append(list, docCite{file: file, anchor: anchor})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", nil
	}
	parts := make([]string, 0, maxDocCite)
	for i, c := range list {
		if i == maxDocCite {
			break
		}
		if c.anchor != "" {
			parts = append(parts, c.file+"#"+c.anchor)
		} else {
			parts = append(parts, c.file)
		}
	}
	out := "\ndocs: " + strings.Join(parts, ", ")
	if len(list) > maxDocCite {
		out += fmt.Sprintf(" (+%d)", len(list)-maxDocCite)
	}
	return out, nil
}

// docMentionsQuery: documentos que citam um símbolo. O EXISTS descarta menção que envelheceu (o símbolo ou o
// arquivo citado não existe mais no índice) sem precisar reprocessar o Markdown.
const docMentionsQuery = `SELECT e.source_file, e.source_line,
		COALESCE((SELECT s.name FROM code_symbols s
			WHERE s.repo_root = e.repo_root AND s.file_path = e.source_file AND s.kind = '` + KindDocSection + `'
				AND s.start_line <= e.source_line AND s.end_line >= e.source_line
			ORDER BY s.end_line - s.start_line LIMIT 1), '')
	FROM code_edges e
	WHERE e.repo_root = ? AND e.target_symbol = ? AND e.relation_type = '` + RelDocMention + `'
		AND (EXISTS (SELECT 1 FROM code_symbols s2 WHERE s2.repo_root = e.repo_root AND s2.name = e.target_symbol AND s2.kind != '` + KindDocSection + `')
			OR EXISTS (SELECT 1 FROM code_files f WHERE f.repo_root = e.repo_root AND f.file_path = e.target_symbol))
	ORDER BY e.source_file, e.source_line`

// docCitedByQuery: documentos que citam um arquivo (link, wikilink ou menção de caminho).
const docCitedByQuery = `SELECT e.source_file, e.source_line,
		COALESCE((SELECT s.name FROM code_symbols s
			WHERE s.repo_root = e.repo_root AND s.file_path = e.source_file AND s.kind = '` + KindDocSection + `'
				AND s.start_line <= e.source_line AND s.end_line >= e.source_line
			ORDER BY s.end_line - s.start_line LIMIT 1), '')
	FROM code_edges e
	WHERE e.repo_root = ? AND e.target_symbol = ? AND e.source_file != ?
		AND e.relation_type IN ('` + RelDocLink + `','` + RelDocWiki + `','` + RelDocMention + `')
		AND (EXISTS (SELECT 1 FROM code_files f WHERE f.repo_root = e.repo_root AND f.file_path = e.target_symbol)
			OR EXISTS (SELECT 1 FROM code_symbols s2 WHERE s2.repo_root = e.repo_root AND s2.name = e.target_symbol AND s2.kind != '` + KindDocSection + `'))
	ORDER BY e.source_file, e.source_line`

// docMentionsOf devolve a linha "docs:" de um símbolo (vazio quando o repositório não tem documentação).
func docMentionsOf(d *sql.DB, key, symbol string, hasDocs bool) string {
	if !hasDocs {
		return ""
	}
	rows, err := d.Query(docMentionsQuery, key, symbol)
	if err != nil {
		return ""
	}
	out, err := docCiteLine(rows)
	if err != nil {
		return ""
	}
	return out
}

// docCitedBy devolve a linha "docs:" de um arquivo: quem o cita (vazio quando não há documentação).
func docCitedBy(d *sql.DB, key, rel string, hasDocs bool) string {
	if !hasDocs {
		return ""
	}
	rows, err := d.Query(docCitedByQuery, key, rel, rel)
	if err != nil {
		return ""
	}
	out, err := docCiteLine(rows)
	if err != nil {
		return ""
	}
	return out
}

// --- utilidades ---

func splitLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	s = strings.TrimPrefix(s, "\xef\xbb\xbf") // BOM
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// frontMatterEnd devolve o índice (0-based) da primeira linha depois do front matter YAML, que é ignorado.
func frontMatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t == "---" || t == "..." {
			return i + 1
		}
	}
	return 0
}

// lastContent: última linha com conteúdo entre start e end (1-based, inclusivo), nunca antes de start.
func lastContent(lines []string, start, end int) int {
	if end > len(lines) {
		end = len(lines)
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

func openingFence(t string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return strings.Repeat(string(c), n)
		}
	}
	return ""
}

func closesFence(t, open string) bool {
	c := open[0]
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	return n >= len(open) && strings.TrimSpace(t[n:]) == ""
}

// pathTokens acha candidatos a caminho numa linha de bloco de código (só caminho: identificador solto ali é
// exemplo, não referência).
func pathTokens(line string) []string {
	var out []string
	for _, m := range rePathToken.FindAllString(line, -1) {
		if t := stripLineSuffix(m); looksLikePath(t) {
			out = append(out, t)
		}
	}
	return out
}

// spanTokens reduz o conteúdo de uma crase a candidatos: `Foo()` → Foo, `pkg.Foo(x)` → pkg.Foo, `a.go:12` → a.go.
func spanTokens(s string) []string {
	if i := strings.Index(s, "("); i > 0 {
		s = s[:i]
	}
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		t := stripLineSuffix(strings.Trim(strings.TrimSpace(part), "`*_\"'"))
		if t != "" && !strings.ContainsAny(t, " \t") {
			out = append(out, t)
		}
	}
	return out
}

func stripLineSuffix(t string) string {
	if m := reLineSuf.FindStringSubmatch(t); m != nil {
		return m[1]
	}
	return t
}

// looksLikePath: tem extensão de arquivo conhecida ou separador de pasta. É o que separa "referência a arquivo"
// de "identificador" e o que evita acusar link externo (http://…) ou âncora solta (#secao) como quebrado.
func looksLikePath(t string) bool {
	if t == "" || strings.HasPrefix(t, "#") || strings.Contains(t, "://") {
		return false
	}
	if strings.ContainsAny(t, "/\\") {
		return true
	}
	if codescan.IsDoc(t) || codescan.LangFor(t) != nil {
		return true
	}
	return cfgExts[strings.ToLower(path.Ext(t))]
}

// cfgExts: extensões que não são código nem documento, mas ainda são arquivo do repositório.
var cfgExts = map[string]bool{
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".xml": true, ".html": true,
	".css": true, ".sql": true, ".txt": true, ".env": true, ".db": true, ".csv": true, ".lock": true,
}

// splitFragment separa "a.md#ancora" em caminho e âncora.
func splitFragment(t string) (string, string) {
	if i := strings.IndexByte(t, '#'); i >= 0 {
		return t[:i], t[i+1:]
	}
	return t, ""
}

// linkPath resolve um alvo de link relativo à pasta do próprio Markdown (regra do GitHub); alvo iniciado por '/'
// é relativo à raiz do repositório.
func linkPath(rel, target string) string {
	t := strings.TrimSpace(filepath.ToSlash(target))
	if t == "" || strings.HasPrefix(t, "#") || strings.Contains(t, "://") {
		return ""
	}
	if i := strings.IndexByte(t, '?'); i >= 0 {
		t = t[:i]
	}
	if strings.HasPrefix(t, "/") {
		return cleanRel(strings.TrimLeft(t, "/"))
	}
	return cleanRel(path.Join(path.Dir(rel), t))
}

func cleanRel(p string) string {
	p = path.Clean(filepath.ToSlash(p))
	if p == "." || p == "/" {
		return ""
	}
	return p
}

var accentFold = map[rune]string{
	'á': "a", 'à': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'õ': "o", 'ö': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u",
	'ç': "c", 'ñ': "n", 'ý': "y", 'ÿ': "y",
}

// slug imita a âncora do GitHub: minúsculas, sem acento, pontuação descartada e espaço em '-'.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if f, ok := accentFold[r]; ok {
			b.WriteString(f)
			dash = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		case r == '-' || r == ' ' || r == '\t' || r == '.':
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
