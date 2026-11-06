package config

import "os"

// SampleJSON é o exemplo comentado na documentação e gravado por `fastexec config init`. Endereços 192.0.2.x são
// da faixa de documentação (RFC 5737). Os testes usam a mesma config via Sample().
const SampleJSON = `{
  "default_host": "server",
  "hosts": {
    "server": { "addr": "192.0.2.10:9142", "cwd": "/home/user/workspace", "aliases": ["srv"] },
    "router": { "addr": "192.0.2.1" },
    "devbox": { "ssh": "devbox", "hidden": true }
  },
  "telemetry": true
}
`

// Isolate prepara um processo de teste: FASTEXEC_HOME numa pasta temporária (telemetria, jobs, sessões e log não
// tocam a pasta real) e a config de exemplo. Devolve a pasta e a limpeza (chamar antes de os.Exit).
func Isolate() (dir string, cleanup func()) {
	dir, err := os.MkdirTemp("", "fastexec-test-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("FASTEXEC_HOME", dir)
	for _, k := range []string{"FASTEXEC_CONFIG", "FASTEXEC_DEFAULT_HOST", "FASTEXEC_DATA_DIR", "FASTEXEC_TELEMETRY"} {
		_ = os.Unsetenv(k)
	}
	Set(Sample())
	return dir, func() { _ = os.RemoveAll(dir) }
}

// Sample devolve SampleJSON já carregado.
func Sample() *Config {
	t := true
	return (&Config{
		DefaultHost: "server",
		Hosts: map[string]Host{
			"server": {Addr: "192.0.2.10:9142", Cwd: "/home/user/workspace", Aliases: []string{"srv"}},
			"router": {Addr: "192.0.2.1"},
			"devbox": {SSH: "devbox", Hidden: true},
		},
		Telemetry: &t,
	}).normalized("")
}
