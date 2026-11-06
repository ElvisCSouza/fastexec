package transport

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/config"
)

// fakeDaemon imita o server-exec no SA: com want != "", exige o token na primeira mensagem; depois ecoa 5 bytes.
// Devolve o endereço e um canal com o que o daemon recebeu antes do eco (para conferir que o SA veio colado).
func fakeDaemon(t *testing.T, want string) (string, <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		var hdr [4]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		var seen bytes.Buffer
		if binary.LittleEndian.Uint16(hdr[0:2]) == MagicAuth {
			tok := make([]byte, binary.LittleEndian.Uint16(hdr[2:4]))
			_, _ = io.ReadFull(c, tok)
			status := uint16(0)
			if want != "" && string(tok) != want {
				status = 1
			}
			var r [4]byte
			binary.LittleEndian.PutUint16(r[0:2], MagicAuth)
			binary.LittleEndian.PutUint16(r[2:4], status)
			_, _ = c.Write(r[:])
			if status != 0 {
				got <- nil
				return
			}
		} else {
			if want != "" { // sem SA com token exigido: SA status 2 e fecha
				var r [4]byte
				binary.LittleEndian.PutUint16(r[0:2], MagicAuth)
				binary.LittleEndian.PutUint16(r[2:4], 2)
				_, _ = c.Write(r[:])
				got <- nil
				return
			}
			seen.Write(hdr[:])
		}
		req := make([]byte, 5-seen.Len())
		_, _ = io.ReadFull(c, req)
		seen.Write(req)
		got <- seen.Bytes()
		_, _ = c.Write(seen.Bytes())
	}()
	return ln.Addr().String(), got
}

func useHost(t *testing.T, addr, token string) {
	t.Helper()
	t.Cleanup(config.Set(&config.Config{Hosts: map[string]config.Host{"box": {Addr: addr, Token: token}}}))
}

func echoTrip(addr string) (string, error) {
	c, err := dialDaemon(addr)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("hello")); err != nil {
		return "", err
	}
	buf := make([]byte, 5)
	_, err = io.ReadFull(c, buf)
	return string(buf), err
}

func TestAuthTokenAccepted(t *testing.T) {
	addr, got := fakeDaemon(t, "s3cret")
	useHost(t, addr, "s3cret")
	out, err := echoTrip(addr)
	if err != nil || out != "hello" {
		t.Fatalf("round trip: %q %v", out, err)
	}
	if string(<-got) != "hello" {
		t.Fatal("request after SA was not delivered intact")
	}
}

func TestAuthWrongToken(t *testing.T) {
	addr, _ := fakeDaemon(t, "s3cret")
	useHost(t, addr, "nope")
	_, err := echoTrip(addr)
	if err == nil || !strings.Contains(err.Error(), "wrong token") || !errors.Is(err, ErrAuth) {
		t.Fatalf("expected wrong-token error, got %v", err)
	}
}

func TestAuthDaemonRequiresTokenClientHasNone(t *testing.T) {
	addr, _ := fakeDaemon(t, "s3cret")
	useHost(t, addr, "")
	if _, err := echoTrip(addr); err == nil {
		t.Fatal("expected the daemon to close the connection")
	}
}

func TestNoTokenSendsNothingExtra(t *testing.T) {
	addr, got := fakeDaemon(t, "")
	useHost(t, addr, "")
	out, err := echoTrip(addr)
	if err != nil || out != "hello" || string(<-got) != "hello" {
		t.Fatalf("plain round trip: %q %v", out, err)
	}
}

// Token recusado na sonda do SX: erro visível e nada guardado (não vira "daemon antigo, usa SE").
func TestProbeWrongTokenNotCachedAsOldDaemon(t *testing.T) {
	addr, _ := fakeDaemon(t, "s3cret")
	useHost(t, addr, "nope")
	ok, err := DaemonSupportsX(addr)
	if ok || !errors.Is(err, ErrAuth) {
		t.Fatalf("probe: ok=%v err=%v", ok, err)
	}
	poolMu.Lock()
	known := xKnown[addr]
	poolMu.Unlock()
	if known {
		t.Fatal("auth failure was cached as an SX answer")
	}
}

// Daemon com token, cliente sem: a sonda diz que falta o token (e não conclui "daemon antigo").
func TestProbeMissingToken(t *testing.T) {
	addr, _ := fakeDaemon(t, "s3cret")
	useHost(t, addr, "")
	ok, err := DaemonSupportsX(addr)
	if ok || !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "requires a token") {
		t.Fatalf("probe: ok=%v err=%v", ok, err)
	}
}

func TestNewToken(t *testing.T) {
	a, b := NewToken(), NewToken()
	if len(a) != 64 || a == b {
		t.Fatalf("tokens: %q %q", a, b)
	}
}
