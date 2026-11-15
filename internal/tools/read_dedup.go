package tools

// read_dedup.go: leitura idêntica repetida na mesma identidade (conversa, senão canal), no mesmo host, arquivo e
// seleção, com conteúdo inalterado, é devolvida como aviso curto em vez do texto inteiro — igual ao dedup de exec.
// A 3a leitura idêntica devolve o texto de novo, para o agente nunca ficar preso. Regras que retiram conteúdo
// (leitura) são presas por identidade de propósito: só quem já leu é que recebe o stub.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/telemetry"
)

const (
	readDedupMinBytes = 1500
	readDedupTTL      = 30 * time.Minute
)

type readSeen struct {
	hash    string
	at      time.Time
	stubbed bool
}

var (
	readDedupMu   sync.Mutex
	readDedupSeen = map[string]*readSeen{}
)

// dedupReadOutput devolve out, ou um stub quando ele repete a leitura idêntica anterior (mesma identidade, host,
// arquivo e seleção) dentro do TTL e com o mesmo conteúdo.
func dedupReadOutput(args ReadFileArgs, out string) string {
	id := identity(args.Caller, args.Conv)
	if id == "" || abOff("dedup", id) || len(out) < readDedupMinBytes {
		return out // sem identidade (testes) nunca deduplica
	}
	sum := sha256.Sum256([]byte(out))
	h := hex.EncodeToString(sum[:])
	key := strings.ToLower(id + "|" + args.Host + "|" + args.Path + "|" + readSignature(args))
	now := time.Now()

	readDedupMu.Lock()
	defer readDedupMu.Unlock()
	if len(readDedupSeen) > 500 {
		readDedupSeen = map[string]*readSeen{}
	}
	e := readDedupSeen[key]
	if e == nil || e.hash != h || now.Sub(e.at) > readDedupTTL {
		readDedupSeen[key] = &readSeen{hash: h, at: now}
		return out
	}
	prev := now.Sub(e.at)
	e.at = now
	if e.stubbed {
		e.stubbed = false
		return out
	}
	e.stubbed = true
	lines := strings.Count(out, "\n") + 1
	stub := fmt.Sprintf("[FastExec] Identical to the same read %ds ago (%d lines, ~%d tokens, sha %s): the text is unchanged, so it was not repeated. Use what you already have; repeat the call once more only if you need the full text again.",
		int(prev.Seconds()), lines, len(out)/4, h[:8])
	telemetry.NoteSavingDetail(telemetry.CatDedup, len(out), len(stub),
		fmt.Sprintf("identical read %ds ago: %d lines not repeated", int(prev.Seconds()), lines))
	return stub
}
