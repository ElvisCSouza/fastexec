package lsp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// fx-roslyn: a camada 1 da validação de C#. É o parser do próprio compilador (CSharpSyntaxTree), então a sintaxe
// sai exata em milissegundos, sem projeto carregado e com o csharp-ls ainda frio (~9 s de carga).
//
// O helper é um programa .NET minúsculo (roslyn/Program.cs) embutido no binário e compilado UMA vez, no primeiro uso,
// com o SDK que a máquina já tem. Ele não baixa nada: referencia o Roslyn que o SDK traz em <sdk>/Roslyn/bincore,
// por isso compila offline e casa com o compilador do dev. Sem SDK não há C# nenhum nesta máquina (o csharp-ls
// também precisa dele) e a camada simplesmente não existe — quem chama recebe RoslynUnavailable, nunca um "ok".

//go:embed roslyn/Program.cs roslyn/fx-roslyn.csproj
var roslynSrc embed.FS

// SyntaxIssue é um erro de sintaxe (DiagnosticSeverity.Error; o helper nunca devolve outra severidade).
type SyntaxIssue struct {
	ID   string `json:"id"`
	Line int    `json:"line"` // 1-based
	Col  int    `json:"col"`  // 1-based
	Msg  string `json:"msg"`
}

// RoslynState diz a quem chama o que fazer com a resposta.
type RoslynState int

const (
	RoslynReady       RoslynState = iota // a lista (possivelmente vazia) é a verdade sobre a sintaxe
	RoslynBuilding                       // helper sendo compilado agora; sem resposta desta vez
	RoslynUnavailable                    // sem SDK, build falhou ou helper quebrou; sem resposta
)

const (
	roslynCallBudget = 3 * time.Second
	roslynIdle       = 10 * time.Minute
)

var (
	roslynMu    sync.Mutex // protege tudo abaixo
	roslynProc  *roslynProcess
	roslynBuild struct {
		running bool
		err     error // última falha de build (limpa quando um build novo começa)
		done    chan struct{}
	}
	roslynIdleTimer *time.Timer
)

func roslynDir() string { return filepath.Join(dataLSPDir(), "fx-roslyn") }

// roslynSrcHash identifica a versão embutida: mudou o fonte, recompila.
func roslynSrcHash() string {
	h := sha256.New()
	for _, n := range []string{"roslyn/Program.cs", "roslyn/fx-roslyn.csproj"} {
		b, _ := roslynSrc.ReadFile(n)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func roslynBuilt() bool {
	dir := roslynDir()
	if _, err := os.Stat(filepath.Join(dir, "fx-roslyn.dll")); err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, ".src-hash"))
	return err == nil && strings.TrimSpace(string(b)) == roslynSrcHash()
}

// RoslynInstalled diz se o helper já está compilado e atual (para `lsp doctor`).
func RoslynInstalled() bool { return roslynBuilt() }

// RoslynPrepare compila o helper em segundo plano se preciso (pré-aquecimento). Barato quando já está pronto.
func RoslynPrepare() {
	if roslynBuilt() {
		return
	}
	roslynMu.Lock()
	defer roslynMu.Unlock()
	startBuildLocked()
}

func startBuildLocked() {
	if roslynBuild.running {
		return
	}
	roslynBuild.running = true
	roslynBuild.err = nil
	roslynBuild.done = make(chan struct{})
	done := roslynBuild.done
	go func() {
		err := buildRoslyn()
		roslynMu.Lock()
		roslynBuild.running = false
		roslynBuild.err = err
		roslynMu.Unlock()
		close(done)
	}()
}

// roslynBincore acha <sdk>/sdk/<versão>/Roslyn/bincore (a versão mais nova).
func roslynBincore(sdk string) string {
	var found []string
	matches, _ := filepath.Glob(filepath.Join(sdk, "sdk", "*", "Roslyn", "bincore"))
	for _, m := range matches {
		if _, err := os.Stat(filepath.Join(m, "Microsoft.CodeAnalysis.CSharp.dll")); err == nil {
			found = append(found, m)
		}
	}
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	return found[len(found)-1]
}

func buildRoslyn() error {
	sdk, _ := dotnetSDK()
	if sdk == "" {
		return errors.New("no .NET SDK on this machine")
	}
	bin := roslynBincore(sdk)
	if bin == "" {
		return errors.New("the .NET SDK has no Roslyn folder (sdk/<version>/Roslyn/bincore)")
	}
	base := dataLSPDir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	src, err := os.MkdirTemp(base, ".fx-roslyn-src-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(src)
	for _, n := range []string{"Program.cs", "fx-roslyn.csproj"} {
		b, _ := roslynSrc.ReadFile("roslyn/" + n)
		if err := os.WriteFile(filepath.Join(src, n), b, 0o644); err != nil {
			return err
		}
	}
	out := filepath.Join(base, ".fx-roslyn-new")
	_ = os.RemoveAll(out)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, dotnetExe(sdk), "publish", "-c", "Release", "-nologo", "-v", "q",
		"-p:RoslynDir="+bin, "-o", out)
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1", "DOTNET_ROOT="+sdk)
	cmd.SysProcAttr = sysProcAttr()
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dotnet publish failed: %v: %s", err, tail(string(b), 400))
	}
	if err := os.WriteFile(filepath.Join(out, ".src-hash"), []byte(roslynSrcHash()), 0o644); err != nil {
		return err
	}
	final := roslynDir()
	roslynStop() // o helper antigo segura os arquivos no Windows
	_ = os.RemoveAll(final)
	return os.Rename(out, final)
}

// RoslynSyntax devolve os erros de sintaxe do texto de um arquivo C#. Fora do estado Ready a lista é nula e NÃO
// significa "sem erros".
func RoslynSyntax(text string) ([]SyntaxIssue, RoslynState, error) {
	if !roslynBuilt() {
		roslynMu.Lock()
		startBuildLocked()
		building, err, done := roslynBuild.running, roslynBuild.err, roslynBuild.done
		roslynMu.Unlock()
		if building {
			// Um build já em curso costuma terminar em segundos: vale esperar um pouco antes de desistir.
			select {
			case <-done:
			case <-time.After(roslynCallBudget):
				return nil, RoslynBuilding, nil
			}
			if !roslynBuilt() {
				roslynMu.Lock()
				err = roslynBuild.err
				roslynMu.Unlock()
				return nil, RoslynUnavailable, err
			}
		} else if err != nil {
			return nil, RoslynUnavailable, err
		}
	}
	issues, err := roslynCall(text)
	if err != nil {
		return nil, RoslynUnavailable, err
	}
	return issues, RoslynReady, nil
}

type roslynProcess struct {
	cmd    *exec.Cmd
	in     *bufio.Writer
	stdin  interface{ Close() error }
	out    *bufio.Reader
	nextID int
}

func roslynStart() (*roslynProcess, error) {
	sdk, _ := dotnetSDK()
	if sdk == "" {
		return nil, errors.New("no .NET SDK on this machine")
	}
	cmd := exec.Command(dotnetExe(sdk), filepath.Join(roslynDir(), "fx-roslyn.dll"))
	cmd.Env = append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1", "DOTNET_ROOT="+sdk,
		"DOTNET_TieredPGO=0", "DOTNET_TC_QuickJitForLoops=1", "DOTNET_gcServer=0")
	cmd.SysProcAttr = sysProcAttr()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &roslynProcess{cmd: cmd, in: bufio.NewWriter(in), stdin: in, out: bufio.NewReaderSize(out, 64*1024)}, nil
}

func roslynStop() {
	roslynMu.Lock()
	p := roslynProc
	roslynProc = nil
	if roslynIdleTimer != nil {
		roslynIdleTimer.Stop()
		roslynIdleTimer = nil
	}
	roslynMu.Unlock()
	if p != nil {
		_ = p.stdin.Close()
		killProcessTree(p.cmd)
	}
}

// callMu serializa as chamadas: o protocolo é pedido→resposta numa pipe só.
var callMu sync.Mutex

func roslynCall(text string) ([]SyntaxIssue, error) {
	callMu.Lock()
	defer callMu.Unlock()
	roslynMu.Lock()
	p := roslynProc
	roslynMu.Unlock()
	if p == nil {
		np, err := roslynStart()
		if err != nil {
			return nil, err
		}
		roslynMu.Lock()
		roslynProc = np
		roslynMu.Unlock()
		p = np
	}
	p.nextID++
	id := p.nextID
	req, _ := json.Marshal(map[string]any{"id": id, "text": text})

	type result struct {
		issues []SyntaxIssue
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		if _, err := p.in.Write(append(req, '\n')); err != nil {
			ch <- result{err: err}
			return
		}
		if err := p.in.Flush(); err != nil {
			ch <- result{err: err}
			return
		}
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			ch <- result{err: err}
			return
		}
		var resp struct {
			ID     int           `json:"id"`
			Errors []SyntaxIssue `json:"errors"`
			Fail   string        `json:"fail"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			ch <- result{err: err}
			return
		}
		if resp.Fail != "" {
			ch <- result{err: errors.New(resp.Fail)}
			return
		}
		if resp.ID != id {
			ch <- result{err: fmt.Errorf("fx-roslyn answered request %d, wanted %d", resp.ID, id)}
			return
		}
		ch <- result{issues: resp.Errors}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			roslynStop()
			return nil, r.err
		}
		armIdle()
		return r.issues, nil
	case <-time.After(roslynCallBudget):
		roslynStop() // pipe fora de sincronia: matar é o único jeito de não ler a resposta errada depois
		return nil, errors.New("fx-roslyn did not answer in time")
	}
}

func armIdle() {
	roslynMu.Lock()
	defer roslynMu.Unlock()
	if roslynIdleTimer != nil {
		roslynIdleTimer.Stop()
	}
	roslynIdleTimer = time.AfterFunc(roslynIdle, roslynStop)
}
