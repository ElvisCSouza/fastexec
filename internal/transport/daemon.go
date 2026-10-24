package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"homelab/fastexec/internal/logger"
)

const (
	Magic          uint16 = 0x5345            // Alias para retrocompatibilidade
	MagicExec      uint16 = 0x5345            // 'SE'
	MagicPut       uint16 = 0x5350            // 'SP'
	MagicGet       uint16 = 0x5347            // 'SG'
	DefaultTimeout        = 200 * time.Second // 40 s de margem antes do corte de ~240 s do cliente MCP; mais que isso, use a tool job
	DialTimeout           = 3 * time.Second
	MaxCmdLen             = 1024 * 1024      // 1 MB
	MaxOutputLen          = 16 * 1024 * 1024 // 16 MB
)

type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int64  `json:"duration_ms"`
	// TimedOut: o comando estourou o timeout e a árvore de processos foi encerrada (só o host local preenche).
	TimedOut bool `json:"timed_out,omitempty"`
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

// PutFileDaemon streams a local file directly over TCP socket to the remote daemon
func PutFileDaemon(tcpAddr string, localPath string, remotePath string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("falha ao abrir arquivo local '%s': %w", localPath, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("falha ao obter metadados do arquivo local: %w", err)
	}
	fileSize := uint64(stat.Size())

	conn, err := net.DialTimeout("tcp", tcpAddr, DialTimeout)
	if err != nil {
		return fmt.Errorf("host inacessível (%s): %w", tcpAddr, err)
	}
	defer conn.Close()

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	remotePathBytes := []byte(filepath.ToSlash(remotePath))
	pathLen := uint16(len(remotePathBytes))

	// Header: 2 bytes magic (0x5350) + 2 bytes path_len + 8 bytes file_size = 12 bytes
	header := make([]byte, 12)
	binary.LittleEndian.PutUint16(header[0:2], MagicPut)
	binary.LittleEndian.PutUint16(header[2:4], pathLen)
	binary.LittleEndian.PutUint64(header[4:12], fileSize)

	if _, err := conn.Write(header); err != nil {
		return fmt.Errorf("falha ao enviar cabeçalho de put para %s: %w", tcpAddr, err)
	}
	if _, err := conn.Write(remotePathBytes); err != nil {
		return fmt.Errorf("falha ao enviar caminho para %s: %w", tcpAddr, err)
	}

	// Stream raw file bytes directly to socket (64KB buffer)
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(conn, file, buf); err != nil {
		return fmt.Errorf("falha ao transmitir arquivo para %s: %w", tcpAddr, err)
	}

	// Read response: 2 bytes magic (0x5350) + 4 bytes status + 4 bytes msg_len = 10 bytes
	respHeader := make([]byte, 10)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return fmt.Errorf("falha ao ler resposta de put de %s: %w", tcpAddr, err)
	}

	magic := binary.LittleEndian.Uint16(respHeader[0:2])
	if magic != MagicPut {
		return fmt.Errorf("protocolo inválido de %s: magic esperado 0x%x, recebido 0x%x", tcpAddr, MagicPut, magic)
	}

	status := int32(binary.LittleEndian.Uint32(respHeader[2:6]))
	msgLen := binary.LittleEndian.Uint32(respHeader[6:10])

	msgBuf := make([]byte, msgLen)
	if msgLen > 0 {
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return fmt.Errorf("falha ao ler mensagem de resposta de %s: %w", tcpAddr, err)
		}
	}

	if status != 0 {
		return fmt.Errorf("falha remota no daemon (%s): %s", tcpAddr, string(msgBuf))
	}

	return nil
}

// GetFileDaemon streams a remote file directly from TCP socket to a local file
func GetFileDaemon(tcpAddr string, remotePath string, localPath string, timeout time.Duration) (int64, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	conn, err := net.DialTimeout("tcp", tcpAddr, DialTimeout)
	if err != nil {
		return 0, fmt.Errorf("host inacessível (%s): %w", tcpAddr, err)
	}
	defer conn.Close()

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	remotePathBytes := []byte(filepath.ToSlash(remotePath))
	pathLen := uint16(len(remotePathBytes))

	// Header: 2 bytes magic (0x5347) + 2 bytes path_len = 4 bytes
	header := make([]byte, 4)
	binary.LittleEndian.PutUint16(header[0:2], MagicGet)
	binary.LittleEndian.PutUint16(header[2:4], pathLen)

	if _, err := conn.Write(header); err != nil {
		return 0, fmt.Errorf("falha ao enviar cabeçalho de get para %s: %w", tcpAddr, err)
	}
	if _, err := conn.Write(remotePathBytes); err != nil {
		return 0, fmt.Errorf("falha ao enviar caminho para %s: %w", tcpAddr, err)
	}

	// Read response: 2 bytes magic (0x5347) + 4 bytes status + 8 bytes file_size + 4 bytes msg_len = 18 bytes
	respHeader := make([]byte, 18)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return 0, fmt.Errorf("falha ao ler resposta de get de %s: %w", tcpAddr, err)
	}

	magic := binary.LittleEndian.Uint16(respHeader[0:2])
	if magic != MagicGet {
		return 0, fmt.Errorf("protocolo inválido de %s: magic esperado 0x%x, recebido 0x%x", tcpAddr, MagicGet, magic)
	}

	status := int32(binary.LittleEndian.Uint32(respHeader[2:6]))
	fileSize := binary.LittleEndian.Uint64(respHeader[6:14])
	msgLen := binary.LittleEndian.Uint32(respHeader[14:18])

	msgBuf := make([]byte, msgLen)
	if msgLen > 0 {
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return 0, fmt.Errorf("falha ao ler mensagem de resposta de %s: %w", tcpAddr, err)
		}
	}

	if status != 0 {
		return 0, fmt.Errorf("falha remota no daemon (%s): %s", tcpAddr, string(msgBuf))
	}

	localDir := filepath.Dir(localPath)
	if localDir != "" && localDir != "." {
		_ = os.MkdirAll(localDir, 0755)
	}

	outFile, err := os.Create(localPath)
	if err != nil {
		return 0, fmt.Errorf("falha ao criar arquivo local '%s': %w", localPath, err)
	}
	defer outFile.Close()

	buf := make([]byte, 64*1024)
	written, err := io.CopyBuffer(outFile, io.LimitReader(conn, int64(fileSize)), buf)
	if err != nil {
		return written, fmt.Errorf("falha ao receber dados de %s: %w", tcpAddr, err)
	}

	return written, nil
}
