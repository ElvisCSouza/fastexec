package mcp

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"homelab/fastexec/internal/telemetry"
)

// A perda da resposta acontece depois de uma espera real: a linha tem de registrar essa espera, não zero.
func TestTransportLostDurationCoversWait(t *testing.T) {
	rawArgs := json.RawMessage(`{"host":"local","argv":["cmd","/c","echo x"]}`)
	start := time.Now() // o dispatchTool captura o instante antes de chamar o Forwarder
	time.Sleep(40 * time.Millisecond)
	e := transportLostEntry("exec", rawArgs, &AfterSendError{Err: errors.New("i/o timeout")}, start)

	if e.DurationMs < 30 {
		t.Fatalf("duration_ms = %d, want >= 30 (a espera antes da perda foi perdida)", e.DurationMs)
	}
	if e.Outcome == nil || e.Outcome.TransportOK != 0 || e.Outcome.ErrorType != telemetry.ErrTransportLost ||
		e.Outcome.EffectUnknown != 1 || e.Outcome.ToolOK != -1 {
		t.Fatalf("outcome = %+v, want transport_lost (transport_ok=0, tool_ok=-1, effect_unknown=1)", e.Outcome)
	}
	if e.Success || e.ErrorMessage != "transport_lost: i/o timeout" {
		t.Fatalf("success/error = %v %q", e.Success, e.ErrorMessage)
	}
}
