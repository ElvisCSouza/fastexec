package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"homelab/fastexec/internal/transport"
)

type FileOpsArgs struct {
	Host   string `json:"host,omitempty"`
	Action string `json:"action"` // "copy", "move", "delete", "mkdir", "list"
	Path   string `json:"path"`
	Target string `json:"target,omitempty"`
}

type FileItem struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

func HandleFileOps(args FileOpsArgs) (string, error) {
	if args.Action == "" {
		return "", fmt.Errorf("parâmetro 'action' é obrigatório (copy, move, delete, mkdir, list)")
	}
	if args.Path == "" {
		return "", fmt.Errorf("parâmetro 'path' é obrigatório")
	}

	host := args.Host
	if host == "" {
		host = "local"
	}

	if host == "local" {
		return handleLocalFileOp(args)
	}

	return handleRemoteFileOp(host, args)
}

func handleLocalFileOp(args FileOpsArgs) (string, error) {
	switch args.Action {
	case "copy":
		if args.Target == "" {
			return "", fmt.Errorf("parâmetro 'target' é obrigatório para ação 'copy'")
		}
		n, err := copyRecursive(args.Path, args.Target)
		if err != nil {
			return "", fmt.Errorf("falha ao copiar: %w", err)
		}
		return fmt.Sprintf(`{"status":"ok","action":"copy","copied_bytes":%d,"target":"%s"}`, n, args.Target), nil

	case "move":
		if args.Target == "" {
			return "", fmt.Errorf("parâmetro 'target' é obrigatório para ação 'move'")
		}
		if err := os.MkdirAll(filepath.Dir(args.Target), 0755); err != nil {
			return "", fmt.Errorf("falha criando diretório de destino: %w", err)
		}
		// Tenta renomear direto (mesmo volume)
		if err := os.Rename(args.Path, args.Target); err != nil {
			// Fallback: cross-volume copy + delete
			if _, cpErr := copyRecursive(args.Path, args.Target); cpErr != nil {
				return "", fmt.Errorf("falha ao mover entre unidades: %w", cpErr)
			}
			_ = os.RemoveAll(args.Path)
		}
		return fmt.Sprintf(`{"status":"ok","action":"move","target":"%s"}`, args.Target), nil

	case "delete":
		if err := os.RemoveAll(args.Path); err != nil {
			return "", fmt.Errorf("falha ao deletar: %w", err)
		}
		return fmt.Sprintf(`{"status":"ok","action":"delete","path":"%s"}`, args.Path), nil

	case "mkdir":
		if err := os.MkdirAll(args.Path, 0755); err != nil {
			return "", fmt.Errorf("falha ao criar diretório: %w", err)
		}
		return fmt.Sprintf(`{"status":"ok","action":"mkdir","path":"%s"}`, args.Path), nil

	case "list":
		entries, err := os.ReadDir(args.Path)
		if err != nil {
			return "", fmt.Errorf("falha ao listar diretório: %w", err)
		}
		var items []FileItem
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			items = append(items, FileItem{
				Name:    e.Name(),
				Size:    info.Size(),
				IsDir:   e.IsDir(),
				ModTime: info.ModTime(),
			})
		}
		b, _ := json.Marshal(items)
		return string(b), nil

	default:
		return "", fmt.Errorf("ação desconhecida: '%s' (use: copy, move, delete, mkdir, list)", args.Action)
	}
}

func handleRemoteFileOp(host string, args FileOpsArgs) (string, error) {
	var cmd string
	switch args.Action {
	case "copy":
		if args.Target == "" {
			return "", fmt.Errorf("parâmetro 'target' é obrigatório para ação 'copy'")
		}
		cmd = fmt.Sprintf("cp -r '%s' '%s' && echo ok", args.Path, args.Target)
	case "move":
		if args.Target == "" {
			return "", fmt.Errorf("parâmetro 'target' é obrigatório para ação 'move'")
		}
		cmd = fmt.Sprintf("mkdir -p \"$(dirname '%s')\" && mv '%s' '%s' && echo ok", args.Target, args.Path, args.Target)
	case "delete":
		cmd = fmt.Sprintf("rm -rf '%s' && echo ok", args.Path)
	case "mkdir":
		cmd = fmt.Sprintf("mkdir -p '%s' && echo ok", args.Path)
	case "list":
		cmd = fmt.Sprintf("python3 -c \"import os, json, sys; p='%s'; print(json.dumps([{'name': e.name, 'is_dir': e.is_dir(), 'size': e.stat().st_size if not e.is_dir() else 0} for e in os.scandir(p)]))\"", args.Path)
	default:
		return "", fmt.Errorf("ação desconhecida: '%s'", args.Action)
	}

	res, err := transport.Dispatch(host, cmd, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("erro na operação remota [%s]: %s", args.Action, res.Stderr)
	}
	if args.Action == "list" {
		return res.Stdout, nil
	}
	return fmt.Sprintf(`{"status":"ok","action":"%s","host":"%s"}`, args.Action, host), nil
}

func copyRecursive(src, dst string) (int64, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !srcInfo.IsDir() {
		return copySingleFile(src, dst)
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, err
	}

	var totalBytes int64
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		n, err := copyRecursive(s, d)
		if err != nil {
			return totalBytes, err
		}
		totalBytes += n
	}
	return totalBytes, nil
}

func copySingleFile(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return 0, err
	}

	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	return io.Copy(out, in)
}
