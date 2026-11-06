package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	Magic          uint16 = 0x5345           // Alias para retrocompatibilidade
	MagicExec      uint16 = 0x5345           // 'SE'
	MagicPut       uint16 = 0x5350           // 'SP'
	MagicGet       uint16 = 0x5347           // 'SG'
	DefaultTimeout        = 45 * time.Second // 15 s de margem antes do corte de 60 s do Claude Desktop; mais que isso, use a tool job
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
	// Leftover: processos da árvore que ainda estavam vivos quando o comando terminou e foram encerrados pelo job
	// (só o host local Windows preenche). Sinal de processo solto: deveria ser um job.
	Leftover uint32 `json:"leftover,omitempty"`
	// Local: rodou no próprio host do FastExec (dicas que falam do shell local só valem nesse caso).
	Local bool `json:"local,omitempty"`
}

// PutFileDaemon streams a local file directly over TCP socket to the remote daemon
func PutFileDaemon(tcpAddr string, localPath string, remotePath string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("could not open local file '%s': %w", localPath, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("could not stat the local file: %w", err)
	}
	fileSize := uint64(stat.Size())

	conn, err := net.DialTimeout("tcp", tcpAddr, DialTimeout)
	if err != nil {
		return fmt.Errorf("host unreachable (%s): %w", tcpAddr, err)
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
		return fmt.Errorf("could not send the put header to %s: %w", tcpAddr, err)
	}
	if _, err := conn.Write(remotePathBytes); err != nil {
		return fmt.Errorf("could not send the path to %s: %w", tcpAddr, err)
	}

	// Stream raw file bytes directly to socket (64KB buffer)
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(conn, file, buf); err != nil {
		return fmt.Errorf("could not stream the file to %s: %w", tcpAddr, err)
	}

	// Read response: 2 bytes magic (0x5350) + 4 bytes status + 4 bytes msg_len = 10 bytes
	respHeader := make([]byte, 10)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return fmt.Errorf("could not read the put response from %s: %w", tcpAddr, err)
	}

	magic := binary.LittleEndian.Uint16(respHeader[0:2])
	if magic != MagicPut {
		return fmt.Errorf("invalid protocol from %s: expected magic 0x%x, got 0x%x", tcpAddr, MagicPut, magic)
	}

	status := int32(binary.LittleEndian.Uint32(respHeader[2:6]))
	msgLen := binary.LittleEndian.Uint32(respHeader[6:10])

	msgBuf := make([]byte, msgLen)
	if msgLen > 0 {
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return fmt.Errorf("could not read the response message from %s: %w", tcpAddr, err)
		}
	}

	if status != 0 {
		return fmt.Errorf("remote daemon failure (%s): %s", tcpAddr, string(msgBuf))
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
		return 0, fmt.Errorf("host unreachable (%s): %w", tcpAddr, err)
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
		return 0, fmt.Errorf("could not send the get header to %s: %w", tcpAddr, err)
	}
	if _, err := conn.Write(remotePathBytes); err != nil {
		return 0, fmt.Errorf("could not send the path to %s: %w", tcpAddr, err)
	}

	// Read response: 2 bytes magic (0x5347) + 4 bytes status + 8 bytes file_size + 4 bytes msg_len = 18 bytes
	respHeader := make([]byte, 18)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return 0, fmt.Errorf("could not read the get response from %s: %w", tcpAddr, err)
	}

	magic := binary.LittleEndian.Uint16(respHeader[0:2])
	if magic != MagicGet {
		return 0, fmt.Errorf("invalid protocol from %s: expected magic 0x%x, got 0x%x", tcpAddr, MagicGet, magic)
	}

	status := int32(binary.LittleEndian.Uint32(respHeader[2:6]))
	fileSize := binary.LittleEndian.Uint64(respHeader[6:14])
	msgLen := binary.LittleEndian.Uint32(respHeader[14:18])

	msgBuf := make([]byte, msgLen)
	if msgLen > 0 {
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return 0, fmt.Errorf("could not read the response message from %s: %w", tcpAddr, err)
		}
	}

	if status != 0 {
		return 0, fmt.Errorf("remote daemon failure (%s): %s", tcpAddr, string(msgBuf))
	}

	localDir := filepath.Dir(localPath)
	if localDir != "" && localDir != "." {
		_ = os.MkdirAll(localDir, 0755)
	}

	outFile, err := os.Create(localPath)
	if err != nil {
		return 0, fmt.Errorf("could not create local file '%s': %w", localPath, err)
	}
	defer outFile.Close()

	buf := make([]byte, 64*1024)
	written, err := io.CopyBuffer(outFile, io.LimitReader(conn, int64(fileSize)), buf)
	if err != nil {
		return written, fmt.Errorf("could not receive data from %s: %w", tcpAddr, err)
	}

	return written, nil
}
