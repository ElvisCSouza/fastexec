package tools

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/transport"
)

const FileChunkSize = 256 * 1024 // 256 KB por chunk

type ReadFileArgs struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
}

type WriteFileArgs struct {
	Host    string `json:"host"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Atomic  bool   `json:"atomic,omitempty"`
}

type GetFileArgs struct {
	Host       string `json:"host"`
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path,omitempty"`
}

type PutFileArgs struct {
	Host       string `json:"host"`
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
}

func HandleReadFile(args ReadFileArgs) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("parâmetro 'path' é obrigatório")
	}

	target, err := transport.ResolveHost(args.Host)
	if err != nil {
		return "", err
	}

	filePath := args.Path
	if target.IsLocal {
		if strings.HasPrefix(filePath, "~/") || strings.HasPrefix(filePath, "~\\") {
			if home, err := os.UserHomeDir(); err == nil {
				filePath = filepath.Join(home, filePath[2:])
			}
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		if args.Offset > 0 || args.Limit > 0 {
			start := int(args.Offset)
			if start > len(data) {
				return "", nil
			}
			end := len(data)
			if args.Limit > 0 && start+int(args.Limit) < end {
				end = start + int(args.Limit)
			}
			return string(data[start:end]), nil
		}
		return string(data), nil
	}

	// Remoto via daemon: expande ~ no bash para $HOME do usuário remoto
	cmd := fmt.Sprintf("TARGET='%s'; TARGET=\"${TARGET/#\\~/$HOME}\"; if [ ! -f \"$TARGET\" ]; then echo '__FILE_NOT_FOUND__' >&2; exit 1; fi; cat \"$TARGET\"", filePath)
	res, err := transport.ExecDaemon(target.TCPAddr, cmd, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("erro lendo arquivo remoto: %s", strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

func HandleWriteFile(args WriteFileArgs) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("parâmetro 'path' é obrigatório")
	}

	target, err := transport.ResolveHost(args.Host)
	if err != nil {
		return "", err
	}

	contentBytes := []byte(args.Content)

	if target.IsLocal {
		dir := filepath.Dir(args.Path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", err
		}
		if args.Atomic {
			tmpPath := fmt.Sprintf("%s.tmp.%d", args.Path, os.Getpid())
			if err := os.WriteFile(tmpPath, contentBytes, 0644); err != nil {
				return "", err
			}
			_ = os.Remove(args.Path)
			if err := os.Rename(tmpPath, args.Path); err != nil {
				return "", err
			}
		} else {
			if err := os.WriteFile(args.Path, contentBytes, 0644); err != nil {
				return "", err
			}
		}
		return okReply(int64(len(contentBytes))), nil
	}

	// Remoto via daemon chunked
	remoteDir := filepath.ToSlash(filepath.Dir(args.Path))
	normalizedPath := filepath.ToSlash(args.Path)
	tmpRemote := fmt.Sprintf("%s.tmp.%d", normalizedPath, os.Getpid())
	targetFile := normalizedPath
	if args.Atomic {
		targetFile = tmpRemote
	}

	// Criar diretório remoto
	_, err = transport.ExecDaemon(target.TCPAddr, fmt.Sprintf("mkdir -p '%s'", remoteDir), transport.DefaultTimeout)
	if err != nil {
		return "", err
	}

	if len(contentBytes) == 0 {
		_, _ = transport.ExecDaemon(target.TCPAddr, fmt.Sprintf(": > '%s'", targetFile), transport.DefaultTimeout)
	} else {
		numChunks := (len(contentBytes) + FileChunkSize - 1) / FileChunkSize
		for i := 0; i < numChunks; i++ {
			start := i * FileChunkSize
			end := start + FileChunkSize
			if end > len(contentBytes) {
				end = len(contentBytes)
			}
			b64Chunk := base64.StdEncoding.EncodeToString(contentBytes[start:end])
			op := ">"
			if i > 0 {
				op = ">>"
			}
			chunkCmd := fmt.Sprintf("echo '%s' | base64 -d %s '%s'", b64Chunk, op, targetFile)
			res, err := transport.ExecDaemon(target.TCPAddr, chunkCmd, transport.DefaultTimeout)
			if err != nil || res.ExitCode != 0 {
				return "", fmt.Errorf("falha ao gravar chunk %d/%d: %v", i+1, numChunks, err)
			}
		}
	}

	if args.Atomic {
		renameCmd := fmt.Sprintf("mv -f '%s' '%s'", tmpRemote, normalizedPath)
		res, err := transport.ExecDaemon(target.TCPAddr, renameCmd, transport.DefaultTimeout)
		if err != nil || res.ExitCode != 0 {
			return "", fmt.Errorf("falha no rename atômico remoto: %v", err)
		}
	}

	return okReply(int64(len(contentBytes))), nil
}

func HandleGetFile(args GetFileArgs) (string, error) {
	if args.RemotePath == "" {
		return "", fmt.Errorf("parâmetro 'remote_path' é obrigatório")
	}

	target, err := transport.ResolveHost(args.Host)
	if err != nil {
		return "", err
	}
	if target.IsLocal {
		return "", fmt.Errorf("get_file requer um host remoto (noteserver, ax6000, ax3000)")
	}

	localPath := args.LocalPath
	if localPath == "" {
		localPath = filepath.Base(args.RemotePath)
	}

	written, err := transport.GetFileDaemon(target.TCPAddr, args.RemotePath, localPath, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}

	return okReply(written), nil
}

func HandlePutFile(args PutFileArgs) (string, error) {
	if args.LocalPath == "" || args.RemotePath == "" {
		return "", fmt.Errorf("parâmetros 'local_path' e 'remote_path' são obrigatórios")
	}

	target, err := transport.ResolveHost(args.Host)
	if err != nil {
		return "", err
	}

	fileInfo, err := os.Stat(args.LocalPath)
	if err != nil {
		return "", fmt.Errorf("arquivo local não encontrado: %w", err)
	}

	if target.IsLocal {
		data, err := os.ReadFile(args.LocalPath)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(args.RemotePath, data, 0644); err != nil {
			return "", err
		}
		return okReply(fileInfo.Size()), nil
	}

	// Remoto via Streaming Binário Direto (Zero-Copy Socket)
	if err := transport.PutFileDaemon(target.TCPAddr, args.LocalPath, args.RemotePath, transport.DefaultTimeout); err != nil {
		return "", err
	}

	return okReply(fileInfo.Size()), nil
}

func okReply(n int64) string {
	return fmt.Sprintf("ok %d bytes", n)
}
