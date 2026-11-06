package transport

import (
	"os"
	"sort"
	"testing"
	"time"

	"homelab/fastexec/internal/config"
)

// TestMedicaoAuth mede o custo do token contra um daemon real (só com FASTEXEC_BENCH=1):
// FASTEXEC_TEST_ADDR = daemon sem token; FASTEXEC_TEST_AUTH_ADDR + FASTEXEC_TEST_AUTH_TOKEN = daemon com token.
// Mede conexão nova a cada chamada (o pior caso do token) e conexão do pool. Comando: argv ["true"] via SX.
func TestMedicaoAuth(t *testing.T) {
	if os.Getenv("FASTEXEC_BENCH") == "" {
		t.Skip("defina FASTEXEC_BENCH=1")
	}
	open, authAddr, tok := os.Getenv("FASTEXEC_TEST_ADDR"), os.Getenv("FASTEXEC_TEST_AUTH_ADDR"), os.Getenv("FASTEXEC_TEST_AUTH_TOKEN")
	if open == "" || authAddr == "" || tok == "" {
		t.Skip("FASTEXEC_TEST_ADDR, FASTEXEC_TEST_AUTH_ADDR e FASTEXEC_TEST_AUTH_TOKEN")
	}
	t.Cleanup(config.Set(&config.Config{Hosts: map[string]config.Host{
		"open": {Addr: open},
		"auth": {Addr: authAddr, Token: tok},
	}}))

	drop := func(addr string) {
		poolMu.Lock()
		for _, ic := range poolIdle[addr] {
			_ = ic.c.Close()
		}
		delete(poolIdle, addr)
		poolMu.Unlock()
	}
	run := func(name, addr string, fresh bool) {
		const n = 200
		ts := make([]float64, 0, n)
		for i := 0; i < n; i++ {
			if fresh {
				drop(addr)
			}
			t0 := time.Now()
			r, ok, err := ExecDaemonX(addr, XRequest{Argv: []string{"true"}}, 10*time.Second)
			if err != nil || !ok || r.ExitCode != 0 {
				t.Fatalf("%s: %v ok=%v %+v", name, err, ok, r)
			}
			ts = append(ts, float64(time.Since(t0).Microseconds())/1000)
		}
		sort.Float64s(ts)
		t.Logf("%-28s mediana %.2f ms  p95 %.2f ms", name, ts[n/2], ts[n*95/100])
	}
	run("sem token, conexão nova", open, true)
	run("com token, conexão nova", authAddr, true)
	run("sem token, pool", open, false)
	run("com token, pool", authAddr, false)
}
