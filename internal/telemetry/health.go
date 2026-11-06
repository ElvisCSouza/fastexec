package telemetry

// health.go: falhas da telemetria nunca ficam silenciosas. Abrir o banco, criar/migrar o esquema, gravar uma entrada
// ou descartar por fila cheia conta como falha: aviso único por processo no stderr (o stdout é do protocolo MCP) e
// linha em telemetry-errors.log ao lado do banco, que o stats lê (quem grava é o daemon; o stats roda em outro processo).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	errorsLogName = "telemetry-errors.log"
	errorsLogMax  = 64 << 10 // passou disso, o arquivo recomeça
)

var errQueueFull = errors.New("queue full, entry dropped")

var (
	failures   atomic.Int64
	warnOnce   sync.Once
	errorsLogM sync.Mutex
)

// noteFailure registra uma falha de telemetria; nunca derruba quem chamou.
func noteFailure(op string, err error) {
	if err == nil {
		return
	}
	n := failures.Add(1)
	warnOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "[FastExec] telemetry %s failed: %v (more in %s)\n", op, err, errorsLogPath())
	})
	// primeira falha e depois 1 a cada 100: o log mostra o problema sem crescer a cada chamada
	if n != 1 && n%100 != 0 {
		return
	}
	errorsLogM.Lock()
	defer errorsLogM.Unlock()
	p := errorsLogPath()
	if fi, err := os.Stat(p); err == nil && fi.Size() > errorsLogMax {
		_ = os.Remove(p)
	}
	f, ferr := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pid=%d failures=%d %s: %v\n", time.Now().Format(time.RFC3339), os.Getpid(), n, op, oneLine(err.Error()))
}

func errorsLogPath() string { return filepath.Join(dataDirPath(), errorsLogName) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// failureReport resume o telemetry-errors.log para o stats: "" quando não há falha registrada.
func failureReport() string {
	b, err := os.ReadFile(errorsLogPath())
	if err != nil || len(b) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return fmt.Sprintf("Telemetry write failures recorded: %d line(s) in %s; last: %s", len(lines), errorsLogPath(), lines[len(lines)-1])
}
