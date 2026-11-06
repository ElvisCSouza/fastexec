// Package config carrega a configuração determinística do FastExec: hosts, host padrão e pastas de dados.
//
// Arquivo: FASTEXEC_CONFIG, senão <home do FastExec>/config.json (home = FASTEXEC_HOME, senão ~/.fastexec).
// Sem arquivo o FastExec conhece só o host "local" e guarda tudo em ~/.fastexec. Caminhos relativos no arquivo
// resolvem a partir da pasta dele. Variáveis de ambiente ganham do arquivo: FASTEXEC_DEFAULT_HOST,
// FASTEXEC_DATA_DIR, FASTEXEC_TELEMETRY (0/off/false desliga).
//
// ~/.fastexec fica fora de AppData de propósito: o Claude Desktop (MSIX) virtualiza AppData dos processos filhos,
// e o que um cliente grava lá o outro não vê.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DefaultPort é a porta do daemon server-exec quando o endereço do host não traz porta.
const DefaultPort = "9142"

// Host é um alvo nomeado. Sem Addr e sem SSH, só o nome "local" (e seus aliases) é válido.
type Host struct {
	Addr    string   `json:"addr,omitempty"`    // "ip[:porta]" do daemon server-exec
	SSH     string   `json:"ssh,omitempty"`     // alias do ~/.ssh/config (SSH BatchMode)
	Cwd     string   `json:"cwd,omitempty"`     // diretório de trabalho padrão no host
	Aliases []string `json:"aliases,omitempty"` // outros nomes aceitos para o mesmo host
	Hidden  bool     `json:"hidden,omitempty"`  // fora das descrições das tools (menos tokens); continua acessível
	Token   string   `json:"token,omitempty"`   // token do daemon (server-exec --token-file); só com addr
}

// Config é o conteúdo do arquivo, já com os padrões e as variáveis de ambiente aplicados.
type Config struct {
	DefaultHost string          `json:"default_host,omitempty"`
	Hosts       map[string]Host `json:"hosts,omitempty"`
	DataDir     string          `json:"data_dir,omitempty"`
	HandoffDir  string          `json:"handoff_dir,omitempty"`
	Telemetry   *bool           `json:"telemetry,omitempty"`

	path string // arquivo lido ("" se não existe)
	err  error  // erro de leitura ou validação: o FastExec segue com os padrões e o `fastexec config` mostra
}

var (
	mu      sync.Mutex
	current *Config
)

// Get devolve a configuração do processo (carregada na primeira chamada).
func Get() *Config {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		current = Load()
	}
	return current
}

// Set troca a configuração do processo e devolve a função que restaura a anterior (testes).
func Set(c *Config) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	prev := current
	current = c.normalized("")
	return func() {
		mu.Lock()
		current = prev
		mu.Unlock()
	}
}

// Home é a pasta do FastExec: FASTEXEC_HOME, senão ~/.fastexec.
func Home() string {
	if h := os.Getenv("FASTEXEC_HOME"); h != "" {
		return h
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".fastexec")
	}
	return filepath.Join(os.TempDir(), "fastexec")
}

// FilePath é o arquivo de configuração esperado (exista ou não).
func FilePath() string {
	if p := os.Getenv("FASTEXEC_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Home(), "config.json")
}

// Load lê o arquivo (se existir) e aplica padrões e variáveis de ambiente. Nunca falha: erro fica em Err().
func Load() *Config {
	path := FilePath()
	c := &Config{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if derr := dec.Decode(c); derr != nil {
			c = &Config{err: fmt.Errorf("%s: %w", path, derr)}
		}
	case os.IsNotExist(err):
		path = ""
	default:
		c.err = err
	}
	out := c.normalized(path)
	out.applyEnv()
	return out
}

func (c *Config) normalized(path string) *Config {
	if c == nil {
		c = &Config{}
	}
	n := *c
	n.path = path
	n.Hosts = map[string]Host{}
	for name, h := range c.Hosts {
		n.Hosts[strings.ToLower(strings.TrimSpace(name))] = h
	}
	n.DefaultHost = strings.ToLower(strings.TrimSpace(n.DefaultHost))
	base := Home()
	if path != "" {
		base = filepath.Dir(path)
	}
	n.DataDir = resolvePath(n.DataDir, base)
	n.HandoffDir = resolvePath(n.HandoffDir, base)
	if verr := n.validate(); verr != nil && n.err == nil {
		n.err = verr
	}
	return &n
}

func (c *Config) applyEnv() {
	if v := os.Getenv("FASTEXEC_DEFAULT_HOST"); v != "" {
		c.DefaultHost = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("FASTEXEC_DATA_DIR"); v != "" {
		c.DataDir = resolvePath(v, Home())
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("FASTEXEC_TELEMETRY"))); v != "" {
		on := !(v == "0" || v == "off" || v == "false" || v == "no")
		c.Telemetry = &on
	}
}

// validate: nomes e aliases únicos, host remoto com Addr ou SSH (não os dois), default existente.
func (c *Config) validate() error {
	seen := map[string]string{"localhost": "local", "127.0.0.1": "local"}
	names := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := c.Hosts[name]
		if name == "" {
			return fmt.Errorf("host with empty name")
		}
		if name == "local" && (h.Addr != "" || h.SSH != "") {
			return fmt.Errorf("host 'local' takes only aliases")
		}
		if name != "local" && (h.Addr == "") == (h.SSH == "") {
			return fmt.Errorf("host '%s' needs exactly one of 'addr' or 'ssh'", name)
		}
		if h.Token != "" && (h.Addr == "" || len(h.Token) > 256) {
			return fmt.Errorf("host '%s': 'token' needs 'addr' and at most 256 characters", name)
		}
		for _, n := range append([]string{name}, h.Aliases...) {
			n = strings.ToLower(strings.TrimSpace(n))
			if other, dup := seen[n]; dup && other != name {
				return fmt.Errorf("name '%s' is used by both '%s' and '%s'", n, other, name)
			}
			seen[n] = name
		}
	}
	if c.DefaultHost != "" && c.DefaultHost != "local" {
		if _, ok := seen[c.DefaultHost]; !ok {
			return fmt.Errorf("default_host '%s' is not a configured host", c.DefaultHost)
		}
	}
	return nil
}

func resolvePath(p, base string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) {
		return filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// Path é o arquivo lido ("" quando não há arquivo).
func (c *Config) Path() string { return c.path }

// Err é o erro de leitura ou validação do arquivo (nil se ok).
func (c *Config) Err() error { return c.err }

// Default é o host usado quando a chamada não diz qual: default_host, senão "local".
func (c *Config) Default() string {
	if c.DefaultHost == "" || c.err != nil {
		return "local"
	}
	return c.DefaultHost
}

// TelemetryOn: ligada salvo telemetry=false no arquivo ou FASTEXEC_TELEMETRY=0.
func (c *Config) TelemetryOn() bool { return c.Telemetry == nil || *c.Telemetry }

// Data é a pasta base de dados (telemetria, índice de código, jobs, sessões, handoffs, log).
func (c *Config) Data() string {
	if c.DataDir != "" {
		return c.DataDir
	}
	return Home()
}

// Dir devolve <Data>/<sub>, criada se não existir.
func (c *Config) Dir(sub string) string {
	d := filepath.Join(c.Data(), sub)
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Handoff é a pasta dos handoffs: handoff_dir, senão <Data>/handoff.
func (c *Config) Handoff() string {
	d := c.HandoffDir
	if d == "" {
		d = filepath.Join(c.Data(), "handoff")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Lookup acha um host pelo nome ou alias (sem diferenciar maiúsculas). canonical é o nome da entrada.
func (c *Config) Lookup(name string) (canonical string, h Host, ok bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "localhost" || n == "127.0.0.1" {
		return "local", Host{}, true
	}
	if c.err == nil {
		if h, ok := c.Hosts[n]; ok {
			return n, h, true
		}
		for cn, h := range c.Hosts {
			for _, a := range h.Aliases {
				if strings.EqualFold(strings.TrimSpace(a), n) {
					return cn, h, true
				}
			}
		}
	}
	if n == "local" {
		return "local", Host{}, true
	}
	return "", Host{}, false
}

// Remote lista os hosts remotos em ordem alfabética; visibleOnly tira os marcados hidden.
func (c *Config) Remote(visibleOnly bool) []string {
	var out []string
	if c.err != nil {
		return out
	}
	for name, h := range c.Hosts {
		if name == "local" || (visibleOnly && h.Hidden) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// HostChoices descreve os alvos para o schema das tools: o host padrão primeiro, com o cwd dele, e depois os
// outros visíveis. Ex.: "'server' (default; cwd ~/workspace), 'local'". Sem config: "'local' (default)".
// cdNote acrescenta ao cwd o aviso contra o prefixo 'cd <cwd> &&' (só na tool exec).
func (c *Config) HostChoices(cdNote bool) string {
	def := c.Default()
	note := c.cwdNote(def)
	if cdNote && note != "" {
		_, h, _ := c.Lookup(def)
		note += fmt.Sprintf(", never prefix 'cd %s &&'", DisplayCwd(h.Cwd))
	}
	parts := []string{fmt.Sprintf("'%s' (default%s)", def, note)}
	if def != "local" {
		parts = append(parts, "'local'")
	}
	for _, name := range c.Remote(true) {
		if name != def {
			parts = append(parts, fmt.Sprintf("'%s'", name))
		}
	}
	return strings.Join(parts, ", ")
}

func (c *Config) cwdNote(name string) string {
	_, h, _ := c.Lookup(name)
	if h.Cwd == "" {
		return ""
	}
	return "; cwd " + DisplayCwd(h.Cwd)
}

// DisplayCwd encurta /home/<usuário>/x e /root/x para ~/x.
func DisplayCwd(cwd string) string {
	if strings.HasPrefix(cwd, "/home/") {
		rest := strings.TrimPrefix(cwd, "/home/")
		if i := strings.Index(rest, "/"); i >= 0 {
			return "~" + rest[i:]
		}
		return "~"
	}
	if cwd == "/root" || strings.HasPrefix(cwd, "/root/") {
		return "~" + strings.TrimPrefix(cwd, "/root")
	}
	return cwd
}
