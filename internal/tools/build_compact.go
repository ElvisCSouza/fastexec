package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"homelab/fastexec/internal/telemetry"
)

// Compactação da saída de build/teste ("poupa saída de build").
//
// O problema: `go test ./...`, `dotnet build`, `npm test`, `tsc` e `cargo build` são os comandos mais repetidos
// do ciclo de trabalho e a maior parte do que eles imprimem não decide a próxima ação — uma linha por pacote que
// passou, avisos de restore, barras de compilação, banners de versão. O que decide é: erro/aviso com
// arquivo:linha:mensagem, o resumo final (passou/falhou, contagens) e o nome dos testes que falharam com a
// mensagem da falha. É isso que sobra aqui, e nada mais.
//
// Regras que o compactor respeita, sem exceção:
//   - erro nunca é cortado: o texto é dividido em blocos (separados por linha em branco) e todo bloco que
//     contenha um sinal de erro/aviso é mantido INTEIRO, com o contexto que veio junto;
//   - toda compactação é verificada antes de sair: se qualquer bloco de erro do original não estiver presente no
//     texto comprimido, a compactação é abortada e o texto original volta intacto;
//   - falha sem nenhum sinal de erro reconhecido aborta (nada é escondido às cegas);
//   - sucesso total vira o resumo (uma linha, ou as poucas linhas de resumo reais do próprio comando);
//   - toda compactação avisa no rodapé: `[FastExec] output compacted (N lines → M); use --raw for the full text.`
//
// Escapes: `--raw` no comando (ou argv) — só em comando de família reconhecida, e nunca depois de um separador
// `--` — e a variável FASTEXEC_NO_OUTPUT_FILTER=1 (aceita também FASTEXEC_NO_BUILD_COMPACT=1). Os dois devolvem
// a saída como veio, e avisam que não compactaram.

type buildFamily int

const (
	buildNone buildFamily = iota
	buildGo
	buildDotnet
	buildNode // npm/yarn/pnpm/npx: jest, vitest, vite, webpack, eslint...
	buildTS
	buildCargo
)

// buildTarget é a família reconhecida, o executável e o subcomando, para o resumo e para a nota.
type buildTarget struct {
	fam  buildFamily
	tool string
	sub  string
}

func (t buildTarget) label() string {
	if t.sub == "" {
		return t.tool
	}
	return t.tool + " " + t.sub
}

// outputFilterOff é o escape por ambiente: devolve a saída íntegra (até o teto de bytes do exec).
func outputFilterOff() bool {
	return os.Getenv("FASTEXEC_NO_OUTPUT_FILTER") != "" || os.Getenv("FASTEXEC_NO_BUILD_COMPACT") != ""
}

// ---- reconhecimento do comando -----------------------------------------------------------------------------

var shellSepRe = regexp.MustCompile(`(&&|\|\||;|\||\(|\)|\{|\})`)
var wrapQuoteRe = regexp.MustCompile(`^[` + "`" + `"']+|[` + "`" + `"']+$`)

// cmdTokens quebra a linha em tokens: separadores de shell viram espaço (o comando pode ser 'cd x && go test ./...')
// e as aspas das pontas caem (pwsh: & 'C:\...\dotnet.exe' build).
func cmdTokens(label string) []string {
	return strings.Fields(shellSepRe.ReplaceAllString(label, " "))
}

func exeName(tok string) string {
	t := wrapQuoteRe.ReplaceAllString(tok, "")
	t = strings.ReplaceAll(t, "\\", "/")
	if i := strings.LastIndexByte(t, '/'); i >= 0 {
		t = t[i+1:]
	}
	t = strings.ToLower(t)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		t = strings.TrimSuffix(t, ext)
	}
	return t
}

// flagAfter devolve o primeiro token que não é flag a partir de i (o subcomando de 'go', que aceita flags antes).
func nextToken(toks []string, i int) string {
	if i+1 >= len(toks) {
		return ""
	}
	return strings.ToLower(wrapQuoteRe.ReplaceAllString(toks[i+1], ""))
}

// longRunning diz que o comando é de longa duração (servidor/watch): não se compacta o que o agente pediu para
// acompanhar.
func longRunning(toks []string) bool {
	for _, t := range toks {
		switch strings.ToLower(wrapQuoteRe.ReplaceAllString(t, "")) {
		case "--watch", "-w", "watch", "dev", "serve", "start", "--serve", "--hot":
			return true
		}
	}
	return false
}

var goSubs = map[string]bool{"build": true, "test": true, "vet": true, "install": true}
var dotnetSubs = map[string]bool{"build": true, "test": true, "msbuild": true, "pack": true, "publish": true, "restore": true}
var cargoSubs = map[string]bool{"build": true, "test": true, "check": true, "clippy": true, "nextest": true}
var nodeSubs = map[string]bool{"test": true, "t": true, "ci": true, "install": true, "i": true, "build": true, "lint": true, "check": true, "typecheck": true}
var npxTools = map[string]bool{"jest": true, "vitest": true, "tsc": true, "webpack": true, "rollup": true, "esbuild": true, "eslint": true}

// detectBuildTarget reconhece a família a partir da linha (ou do argv juntado). Devolve false para qualquer
// comando fora da lista: `go run`, `dotnet run`, `npm start` e tudo o mais seguem intocados.
func detectBuildTarget(label string) (buildTarget, bool) {
	toks := cmdTokens(label)
	if len(toks) == 0 || longRunning(toks) {
		return buildTarget{}, false
	}
	for i, raw := range toks {
		switch exeName(raw) {
		case "go":
			if sub := nextToken(toks, i); goSubs[sub] {
				return buildTarget{buildGo, "go", sub}, true
			}
		case "dotnet":
			if sub := nextToken(toks, i); dotnetSubs[sub] {
				return buildTarget{buildDotnet, "dotnet", sub}, true
			}
		case "cargo":
			if sub := nextToken(toks, i); cargoSubs[sub] {
				return buildTarget{buildCargo, "cargo", sub}, true
			}
		case "tsc":
			return buildTarget{buildTS, "tsc", ""}, true
		case "npm", "yarn", "pnpm", "bun":
			tool := exeName(raw)
			sub := nextToken(toks, i)
			if sub == "run" && i+2 < len(toks) {
				sub = strings.ToLower(wrapQuoteRe.ReplaceAllString(toks[i+2], ""))
			}
			if nodeSubs[sub] {
				return buildTarget{buildNode, tool, sub}, true
			}
		case "npx", "pnpx":
			tool := nextToken(toks, i)
			if npxTools[tool] {
				return buildTarget{buildNode, "npx " + tool, ""}, true
			}
			if tool == "vite" && i+2 < len(toks) && strings.EqualFold(wrapQuoteRe.ReplaceAllString(toks[i+2], ""), "build") {
				return buildTarget{buildNode, "vite", "build"}, true
			}
		}
	}
	return buildTarget{}, false
}

// stripBuildRawEscape tira o marcador `--raw` de um comando de build/teste reconhecido, para o pedido de saída
// íntegra funcionar sem depender de variável de ambiente. Nada depois de um separador `--` é tocado (ali os
// argumentos são do script, não do FastExec), e o marcador só é removido quando o comando é de família
// reconhecida — em qualquer outro comando um `--raw` é do próprio programa e fica onde está.
func stripBuildRawEscape(args *ExecArgs) (label string, stripped bool) {
	if len(args.Argv) > 0 {
		out := make([]string, 0, len(args.Argv))
		hit, afterSep := false, false
		for i, a := range args.Argv {
			if a == "--" {
				afterSep = true
			}
			if !afterSep && i > 0 && strings.EqualFold(a, "--raw") {
				hit = true
				continue
			}
			out = append(out, a)
		}
		if !hit {
			return strings.Join(args.Argv, " "), false
		}
		joined := strings.Join(out, " ")
		if _, ok := detectBuildTarget(joined); !ok {
			return strings.Join(args.Argv, " "), false
		}
		args.Argv = out
		return joined, true
	}

	toks := cmdTokens(args.Command)
	hit, afterSep := false, false
	kept := make([]string, 0, len(toks))
	for i, t := range toks {
		if t == "--" {
			afterSep = true
		}
		if !afterSep && i > 0 && strings.EqualFold(t, "--raw") {
			hit = true
			continue
		}
		kept = append(kept, t)
	}
	if !hit {
		return args.Command, false
	}
	joined := strings.Join(kept, " ")
	if _, ok := detectBuildTarget(joined); !ok {
		return args.Command, false
	}
	args.Command = joined
	return joined, true
}

// ---- classificação das linhas ------------------------------------------------------------------------------

// buildErrRe é o sinal de erro/aviso: o que casa aqui NUNCA é descartado.
var buildErrRe = regexp.MustCompile(`(?i)(\berror\b|\berrors\b|\berros?\b|\bfalha\w*|\bfalhou\b|\bwarning\b|\baviso\b|\bwarn\b|\bfail(ed|ure|ing|s)?\b|\bpanic\b|traceback|\bexception\b|cannot (find|use|resolve)|not (found|recognized|defined)|\bno such file\b|undefined:|\bunresolved\b|\bassert|\bexpected\b|\bunexpected\b|✕|✗|●)`)

// codeLocRe é o segundo sinal forte: linha de diagnóstico com arquivo:linha (ou arquivo(linha,col)).
var codeLocRe = regexp.MustCompile(`[\w./\\-]+\.(go|cs|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|rb|py|c|h|cpp|hpp|swift)\b[:(]\d+`)

func isBuildErrorLine(trimmed string) bool {
	return buildErrRe.MatchString(trimmed) || codeLocRe.MatchString(trimmed)
}

var goTestNoiseRe = regexp.MustCompile(`^(ok\s|\?\s|PASS$|---\sPASS:|---\sSKIP:|===\s(RUN|PAUSE|CONT|NAME))`)
var dotnetNoiseRe = regexp.MustCompile(`(?i)^(MSBuild version|Determining projects to restore|All projects are up-to-date for restore|Restored |\d+ of \d+ projects|NuGet package restore|Waiting for|Build started|(Passed|Aprovado)\s+\S+.*\[[^\]]*\]\s*$|\d+ (test )?(files?|arquivos? de teste) .*(matched|corresponderam)|Tempo total|Total time)`)

// sourceLinkWarnRe: o aviso do SourceLink/Git quando a pasta não é um repositório (cópia de trabalho, zip). Sai uma vez por
// projeto e por TFM, centenas de linhas iguais que não dizem nada sobre o código; viram uma linha só.
var sourceLinkWarnRe = regexp.MustCompile(`(?i)Microsoft\.(Build\.Tasks\.Git|SourceLink\.\w+)\.targets\(\d+,\d+\): warning`)

// diagTailRe: o sufixo "[projeto.csproj::TargetFramework=...]" que o MSBuild repete em cada TFM do mesmo aviso.
var diagTailRe = regexp.MustCompile(`\s*\[[^\]]*\.(cs|vb|fs)proj[^\]]*\]\s*$`)

// nodeNoiseRe: cabeçalho do script ('> app@1.0.0 test', '> jest'), uma linha por suíte que passou, banner do vite,
// download do registry. O '>' do code frame do jest ("> 11 | expect(...)") NÃO entra: ele aponta a linha que falhou.
var nodeNoiseRe = regexp.MustCompile(`(?i)^(npm notice|npm warn deprecated|\s*(PASS|✓|√|❯|↓|↑)\s|>\s+[\w@/.-]+@[\w.-]+|>\s+(jest|vitest|node|tsc|webpack|vite|eslint|next)\s*$|vite v|Done in \d|Downloading|added \d+ package|found \d+ vulnerabilities|audited \d+ package|up to date, audited)`)
var cargoNoiseRe = regexp.MustCompile(`(?i)^\s*(Compiling|Checking|Downloading|Downloaded|Updating|Locking|Adding|Removing|Blocking|Fresh|Documenting)\b`)

// summaryRe é o resumo final que o próprio comando imprime: em sucesso ele é o que sobra (junto com avisos).
var summaryRe = map[buildFamily]*regexp.Regexp{
	buildGo:     regexp.MustCompile(`^(FAIL|ok\s|\?\s)`),
	buildDotnet: regexp.MustCompile(`(?i)^(Build succeeded|Build FAILED|Passed!|Failed!|Test Run (Successful|Failed)|Time Elapsed|\s*\d+ (Warning|Error)\(s\))`),
	buildNode:   regexp.MustCompile(`^(Test Suites:|Tests:|Snapshots:|Time:|Ran all test suites|\s*\d+ (passing|failing|pending))`),
	buildTS:     regexp.MustCompile(`^Found \d+ error`),
	buildCargo:  regexp.MustCompile(`^(test result:|error: could not compile|Finished|warning: .*generated)`),
}

// summarySkip: linhas de resumo que não valem a linha que ocupam.
var summarySkipRe = regexp.MustCompile(`^(Snapshots:|Ran all test suites)`)

func isFamilyNoise(fam buildFamily, trimmed string) bool {
	switch fam {
	case buildGo:
		return goTestNoiseRe.MatchString(trimmed)
	case buildDotnet:
		return dotnetNoiseRe.MatchString(trimmed)
	case buildNode:
		return nodeNoiseRe.MatchString(trimmed)
	case buildCargo:
		return cargoNoiseRe.MatchString(trimmed)
	}
	return false
}

type buildLine struct {
	text    string
	err     bool
	summary bool
	noise   bool
}

type buildBlock struct {
	lines []buildLine
	err   bool // tem pelo menos uma linha de erro/aviso
}

// splitBuildBlocks agrupa as linhas em blocos separados por linha em branco. O bloco é a unidade de leitura e de
// verificação: o contexto do erro (o '-->' do rust, o code frame do jest logo abaixo do '--- FAIL') fica porque é
// linha neutra, e depois da compactação todo bloco marcado como erro é conferido inteiro.
func splitBuildBlocks(fam buildFamily, text string) []buildBlock {
	var blocks []buildBlock
	var cur buildBlock
	flush := func() {
		if len(cur.lines) > 0 {
			blocks = append(blocks, cur)
		}
		cur = buildBlock{}
	}
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			flush()
			continue
		}
		l := buildLine{text: raw}
		switch {
		case isFamilyNoise(fam, trimmed):
			l.noise = true
		case summaryRe[fam].MatchString(trimmed):
			l.summary = true
		case isBuildErrorLine(trimmed):
			l.err = true
			cur.err = true
		}
		cur.lines = append(cur.lines, l)
	}
	flush()
	return blocks
}

// ---- compactação -------------------------------------------------------------------------------------------

// compactBuildOutput devolve o texto comprimido, a nota de rodapé e ok=false quando não há o que compactar
// (comando fora da lista, saída já curta, ganho pequeno ou compactação abortada pela checagem de segurança).
func compactBuildOutput(label string, exitCode int, stdout, stderr string) (string, string, bool) {
	tgt, found := detectBuildTarget(label)
	if !found || outputFilterOff() {
		return "", "", false
	}
	var parts []string
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, strings.TrimRight(stdout, "\r\n"))
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, strings.TrimRight(stderr, "\r\n"))
	}
	if len(parts) == 0 {
		return "", "", false
	}
	raw := strings.Join(parts, "\n")
	beforeLines := nonEmptyLines(raw)
	if beforeLines < 3 && exitCode == 0 {
		return "", "", false
	}

	fam := tgt.fam
	blocks := splitBuildBlocks(fam, raw)
	var kept []string
	droppedLines := 0
	// avisos repetidos (o mesmo texto em cada TFM) e o aviso de SourceLink/Git viram contagem; o que sai entra em
	// `covered` para a checagem de segurança saber que não foi esquecido.
	covered := map[string]bool{}
	seenDiag := map[string]bool{}
	sourceLink, dupDiag := 0, 0
	for _, b := range blocks {
		// O bloco é a unidade de leitura, não a de descarte: cai só a linha de RUÍDO (uma por pacote que passou,
		// barra de progresso, banner, restore). Erro, aviso, resumo e linha neutra ficam — e é nas neutras que
		// vive o contexto do erro (o '-->' do rust, a seta e o code frame do jest, o '|' do vazamento).
		for _, l := range b.lines {
			if l.err {
				if fam == buildDotnet {
					trimmed := strings.TrimSpace(l.text)
					if sourceLinkWarnRe.MatchString(trimmed) {
						sourceLink++
						covered[trimmed] = true
						continue
					}
					if strings.Contains(strings.ToLower(trimmed), "warning") && !strings.Contains(strings.ToLower(trimmed), "error") {
						key := diagTailRe.ReplaceAllString(trimmed, "")
						if seenDiag[key] {
							dupDiag++
							covered[trimmed] = true
							continue
						}
						seenDiag[key] = true
					}
				}
				kept = append(kept, l.text)
				continue
			}
			if l.noise || (l.summary && exitCode == 0 && summarySkipRe.MatchString(strings.TrimSpace(l.text))) {
				droppedLines++
				continue
			}
			kept = append(kept, l.text)
		}
	}

	if sourceLink > 0 {
		kept = append(kept, fmt.Sprintf("(%d SourceLink/Git warnings collapsed: this folder is not a git repository; harmless for the build)", sourceLink))
	}
	if dupDiag > 0 {
		kept = append(kept, fmt.Sprintf("(%d repeated warnings collapsed: same text for another target framework)", dupDiag))
	}
	// Segurança 1: falha sem nenhum sinal de erro reconhecido não é compactada (nada é escondido às cegas).
	if exitCode != 0 && len(kept) == 0 {
		return "", "", false
	}
	// Segurança 2: nenhuma linha de erro do original pode faltar no texto comprimido.
	if !keptCoversErrors(blocks, kept, covered) {
		return "", "", false
	}

	outLines := kept
	// Sucesso total (ou sucesso com só ruído em volta): o resumo do comando, quando ele não o imprime sozinho.
	if exitCode == 0 {
		if summary := successSummary(tgt, raw); summary != "" && !strings.Contains(strings.Join(outLines, "\n"), summary) {
			outLines = append(outLines, summary)
		}
	}
	if len(outLines) == 0 {
		return "", "", false
	}

	// Ganho pequeno: mexer no texto só para tirar uma ou duas linhas não paga o risco de esconder algo.
	if beforeLines-len(outLines) < 2 {
		return "", "", false
	}
	body := strings.Join(outLines, "\n")
	afterLines := len(outLines)
	note := fmt.Sprintf("[FastExec] output compacted (%d lines → %d); use --raw for the full text.", beforeLines, afterLines)
	if droppedLines > 0 {
		note = fmt.Sprintf("[FastExec] output compacted (%d lines → %d, %d lines of build progress with no error or warning in them left out); use --raw for the full text.",
			beforeLines, afterLines, droppedLines)
	}
	telemetry.NoteSavingDetail(telemetry.CatClean, len(raw), len(body),
		fmt.Sprintf("%s output compacted: %d lines → %d (errors and warnings kept, build progress left out); --raw returns the full text",
			tgt.label(), beforeLines, afterLines))
	return body, note, true
}

// keptCoversErrors confere que todo bloco marcado como erro no original tem as suas linhas presentes no texto
// comprimido. É a garantia mecânica do "nenhum erro some": se falhar, quem chama devolve o texto original.
func keptCoversErrors(blocks []buildBlock, kept []string, covered map[string]bool) bool {
	present := make(map[string]int, len(kept))
	for _, l := range kept {
		present[strings.TrimSpace(l)]++
	}
	for l := range covered {
		present[l]++
	}
	for _, b := range blocks {
		if !b.err {
			continue
		}
		for _, l := range b.lines {
			if l.err && present[strings.TrimSpace(l.text)] == 0 {
				return false
			}
		}
	}
	return true
}

// successSummary devolve a linha de resumo de um comando que passou. Para o `go` (que não tem linha de resumo) ela
// é sintetizada das contagens; nas outras famílias o resumo é o próprio texto do comando, juntado numa linha.
func successSummary(tgt buildTarget, raw string) string {
	switch tgt.fam {
	case buildGo:
		if tgt.sub != "test" {
			return ""
		}
		pkgs, noTests, passes, dur := 0, 0, 0, 0.0
		for _, l := range strings.Split(raw, "\n") {
			t := strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(t, "ok ") || strings.HasPrefix(t, "ok\t"):
				fields := strings.Fields(t)
				pkgs++
				if len(fields) > 2 {
					if d, err := parseGoDuration(fields[len(fields)-1]); err == nil {
						dur += d
					}
				}
			case strings.HasPrefix(t, "? ") || strings.HasPrefix(t, "?\t"):
				noTests++
			case strings.HasPrefix(t, "--- PASS:"):
				passes++
			}
		}
		if pkgs+noTests == 0 {
			return ""
		}
		s := fmt.Sprintf("go test: %d packages ok, %d without tests, 0 failures", pkgs, noTests)
		if passes > 0 {
			s += fmt.Sprintf(", %d tests passed", passes)
		}
		if dur > 0 {
			s += fmt.Sprintf(", %.1fs total", dur)
		}
		return s + "."
	default:
		var found []string
		for _, l := range strings.Split(raw, "\n") {
			t := strings.TrimSpace(l)
			if summaryRe[tgt.fam].MatchString(t) && !summarySkipRe.MatchString(t) {
				found = append(found, t)
			}
		}
		if len(found) == 0 {
			return ""
		}
		if len(found) > 4 { // o veredito ('Build succeeded.') e as contagens: o resto do resumo é eco
			found = found[len(found)-4:]
		}
		s := tgt.label() + ": " + strings.Join(found, " | ")
		if len(s) > 400 {
			s = s[:400]
		}
		return s
	}
}

func parseGoDuration(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "ms") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return v / 1000, err
	}
	if strings.HasSuffix(s, "m") && strings.Contains(s, "m") {
		// 1m02.3s
		parts := strings.SplitN(strings.TrimSuffix(s, "s"), "m", 2)
		if len(parts) == 2 {
			m, err1 := strconv.ParseFloat(parts[0], 64)
			sec, err2 := strconv.ParseFloat(parts[1], 64)
			if err1 == nil && err2 == nil {
				return m*60 + sec, nil
			}
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
	return v, err
}
