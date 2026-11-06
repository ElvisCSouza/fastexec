package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Identity identifica o binário de um processo: caminho e (tamanho, mtime) do executável quando ele subiu.
// Duas cópias do mesmo build (mesmo tamanho e mtime) são o mesmo binário, mesmo em caminhos diferentes.
type Identity struct {
	PID int    `json:"pid"`
	Exe string `json:"exe"`
	ID  string `json:"id"`
}

var self = currentIdentity()

func currentIdentity() Identity {
	exe, err := os.Executable()
	if err != nil {
		return Identity{PID: os.Getpid()}
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return Identity{PID: os.Getpid(), Exe: exe, ID: FileID(exe)}
}

// FileID é a identidade de um executável em disco ("" se não existe): tamanho e mtime.
func FileID(path string) string {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.Size(), st.ModTime().UnixNano())
}

// Self é a identidade deste processo.
func Self() Identity { return self }

// Running devolve a identidade do daemon em execução (erro se não há daemon). Daemon de versão antiga responde
// só o PID: ID vazio.
func Running() (Identity, error) {
	conn, err := net.DialTimeout("tcp", Addr, DialTimeout)
	if err != nil {
		return Identity{}, err
	}
	defer conn.Close()
	req, _ := json.Marshal(DaemonRequest{JSONRPC: "2.0", ID: 1, Method: "ping"})
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return Identity{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Identity{}, err
	}
	var resp struct {
		Status Identity `json:"status_result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return Identity{}, err
	}
	return resp.Status, nil
}

// EnsureCurrent troca o daemon em execução quando ele é de outro binário (o executável foi atualizado depois que
// ele subiu, ou é um build diferente): para o antigo e deixa a próxima chamada subir o deste executável.
// Chamado uma vez quando o servidor MCP inicia; o CLI não paga o custo. Devolve se trocou.
// Atenção: parar o daemon encerra comandos em andamento nele e os jobs locais que ele mantém.
func EnsureCurrent() (bool, error) {
	cur, err := Running()
	if err != nil {
		return false, nil // sem daemon: sobe sob demanda
	}
	if cur.ID != "" && cur.ID == self.ID {
		return false, nil
	}
	if err := StopDaemon(); err != nil {
		return false, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := Running(); err != nil {
			return true, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return true, fmt.Errorf("old daemon (PID %d) did not stop", cur.PID)
}
