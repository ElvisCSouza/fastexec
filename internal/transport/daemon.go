package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"homelab/commander/internal/logger"
)

const (
	Magic            uint16 = 0x5345
	DefaultTimeout          = 120 * time.Second
	DialTimeout             = 3 * time.Second
	MaxCmdLen               = 1024 * 1024      // 1 MB
	MaxOutputLen            = 16 * 1024 * 1024 // 16 MB
)

type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int64  `json:"duration_ms"`
}

func ExecDaemon(tcpAddr string, cmd string, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", tcpAddr, DialTimeout)
	if err != nil {
		logger.Error("Falha de conexão com %s: %v", tcpAddr, err)
		return nil, fmt.Errorf("host inacessível (%s): %w", tcpAddr, err)
	}
	defer conn.Close()

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}

	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)

	cmdBytes := []byte(cmd)
	if len(cmdBytes) > MaxCmdLen {
		return nil, fmt.Errorf("comando excede tamanho máximo permitido (1MB)")
	}

	// Header: 2 bytes magic + 4 bytes cmd_len
	reqHeader := make([]byte, 6)
	binary.LittleEndian.PutUint16(reqHeader[0:2], Magic)
	binary.LittleEndian.PutUint32(reqHeader[2:6], uint32(len(cmdBytes)))

	if _, err := conn.Write(append(reqHeader, cmdBytes...)); err != nil {
		return nil, fmt.Errorf("falha ao enviar comando para %s: %w", tcpAddr, err)
	}

	// Resposta Header 1: 2 bytes magic + 4 bytes exit_code + 4 bytes stdout_len = 10 bytes
	respHeader := make([]byte, 10)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return nil, fmt.Errorf("falha ao ler cabeçalho da resposta de %s: %w", tcpAddr, err)
	}

	magic := binary.LittleEndian.Uint16(respHeader[0:2])
	if magic != Magic {
		return nil, fmt.Errorf("protocolo inválido de %s: magic esperado 0x%x, recebido 0x%x", tcpAddr, Magic, magic)
	}

	exitCode := int(int32(binary.LittleEndian.Uint32(respHeader[2:6])))
	stdoutLen := binary.LittleEndian.Uint32(respHeader[6:10])
	if stdoutLen > MaxOutputLen {
		return nil, fmt.Errorf("stdout excedeu limite físico do daemon (%d bytes)", stdoutLen)
	}

	stdoutBuf := make([]byte, stdoutLen)
	if stdoutLen > 0 {
		if _, err := io.ReadFull(conn, stdoutBuf); err != nil {
			return nil, fmt.Errorf("falha ao ler stdout de %s: %w", tcpAddr, err)
		}
	}

	// Resposta Header 2: 4 bytes stderr_len
	stderrLenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, stderrLenBuf); err != nil {
		return nil, fmt.Errorf("falha ao ler tamanho de stderr de %s: %w", tcpAddr, err)
	}
	stderrLen := binary.LittleEndian.Uint32(stderrLenBuf)
	if stderrLen > MaxOutputLen {
		return nil, fmt.Errorf("stderr excedeu limite físico do daemon (%d bytes)", stderrLen)
	}

	stderrBuf := make([]byte, stderrLen)
	if stderrLen > 0 {
		if _, err := io.ReadFull(conn, stderrBuf); err != nil {
			return nil, fmt.Errorf("falha ao ler stderr de %s: %w", tcpAddr, err)
		}
	}

	durationMs := time.Since(start).Milliseconds()
	logger.Debug("[%s] cmd: %.40s... -> exit %d (%dms)", tcpAddr, cmd, exitCode, durationMs)

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   string(stdoutBuf),
		Stderr:   string(stderrBuf),
		Duration: durationMs,
	}, nil
}
