package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/transport"
)

const FileChunkSize = 256 * 1024 // 256 KB por chunk

type ReadFileArgs struct {
	Host      string `json:"host"`
	Path      string `json:"path"`
	Offset    int64  `json:"offset,omitempty"`
	Limit     int64  `json:"limit,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Ranges    string `json:"ranges,omitempty"`
	Numbered  bool   `json:"numbered,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	Context   int    `json:"context,omitempty"`
	Caller    string `json:"_caller,omitempty"`       // canal MCP (injetado pelo proxy, não vem da LLM)
	Conv      string `json:"_conversation,omitempty"` // id de conversa do cliente, quando houver
}

type EditFileArgs struct {
	Host        string   `json:"host,omitempty"`
	Path        string   `json:"path"`
	Target      string   `json:"target"`
	Replacement string   `json:"replacement"`
	Edits       []EditOp `json:"edits,omitempty"`
	Caller      string   `json:"_caller,omitempty"`       // canal MCP (injetado pelo proxy)
	Conv        string   `json:"_conversation,omitempty"` // id de conversa do cliente, quando houver
}

// EditOp é uma troca de 'edits': aplicada em ordem sobre o resultado da anterior; todas ou nenhuma.
type EditOp struct {
	Target      string `json:"target"`
	Replacement string `json:"replacement"`
}

// UnmarshalJSON aceita os mesmos sinônimos de EditFileArgs (old_string/new_string etc.).
func (o *EditOp) UnmarshalJSON(data []byte) error {
	var a EditFileArgs
	if err := a.UnmarshalJSON(data); err != nil {
		return err
	}
	o.Target, o.Replacement = a.Target, a.Replacement
	return nil
}

// UnmarshalJSON aceita sinônimos frequentes de 'target' (old_string, search, find)
// e 'replacement' (new_string, replace, content) comuns em LLMs e editores.
func (a *EditFileArgs) UnmarshalJSON(data []byte) error {
	type Alias EditFileArgs
	var aux struct {
		Alias
		OldString string `json:"old_string"`
		OldStr    string `json:"old_str"`
		Search    string `json:"search"`
		Find      string `json:"find"`
		NewString string `json:"new_string"`
		NewStr    string `json:"new_str"`
		Replace   string `json:"replace"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*a = EditFileArgs(aux.Alias)
	if a.Target == "" {
		if aux.OldString != "" {
			a.Target = aux.OldString
		} else if aux.OldStr != "" {
			a.Target = aux.OldStr
		} else if aux.Search != "" {
			a.Target = aux.Search
		} else if aux.Find != "" {
			a.Target = aux.Find
		}
	}
	if a.Replacement == "" {
		if aux.NewString != "" {
			a.Replacement = aux.NewString
		} else if aux.NewStr != "" {
			a.Replacement = aux.NewStr
		} else if aux.Replace != "" {
			a.Replacement = aux.Replace
		} else if aux.Content != "" {
			a.Replacement = aux.Content
		}
	}
	return nil
}

type WriteFileArgs struct {
	Host    string `json:"host"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Atomic  bool   `json:"atomic,omitempty"`
	// Overwrite libera regravar um arquivo que já existe e não está vazio; sem ele, o write_file recusa e aponta o
	// edit_file (reescrever o arquivo inteiro gera o conteúdo todo como saída do modelo, o token mais caro).
	Overwrite bool `json:"overwrite,omitempty"`
}

type GetFileArgs struct {
	Host       string `json:"host"`
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path,omitempty"`
}

func (a *GetFileArgs) UnmarshalJSON(data []byte) error {
	type Alias GetFileArgs
	var aux struct {
		Alias
		Path        string `json:"path"`
		Src         string `json:"src"`
		Source      string `json:"source"`
		Remote      string `json:"remote"`
		File        string `json:"file"`
		Dest        string `json:"dest"`
		Destination string `json:"destination"`
		Target      string `json:"target"`
		Out         string `json:"out"`
		Output      string `json:"output"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*a = GetFileArgs(aux.Alias)
	if a.RemotePath == "" {
		for _, s := range []string{aux.Path, aux.Src, aux.Source, aux.Remote, aux.File} {
			if s != "" {
				a.RemotePath = s
				break
			}
		}
	}
	if a.LocalPath == "" {
		for _, s := range []string{aux.Dest, aux.Destination, aux.Target, aux.Out, aux.Output} {
			if s != "" {
				a.LocalPath = s
				break
			}
		}
	}
	return nil
}

type PutFileArgs struct {
	Host       string `json:"host"`
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
}

func (a *PutFileArgs) UnmarshalJSON(data []byte) error {
	type Alias PutFileArgs
	var aux struct {
		Alias
		Src    string `json:"src"`
		Source string `json:"source"`
		Path   string `json:"path"`
		File   string `json:"file"`
		Dest   string `json:"dest"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*a = PutFileArgs(aux.Alias)
	if a.LocalPath == "" {
		for _, s := range []string{aux.Src, aux.Source, aux.Path, aux.File} {
			if s != "" {
				a.LocalPath = s
				break
			}
		}
	}
	if a.RemotePath == "" {
		for _, s := range []string{aux.Dest, aux.Target} {
			if s != "" {
				a.RemotePath = s
				break
			}
		}
	}
	return nil
}

// SplitHostPath analisa strings que podem conter a sintaxe "host:path" (ex: "server:/tmp/foo" ou "router:/etc/config").
// No Windows, letras de unidade como "C:\", "D:\", "D:/" NÃO são tratadas como host.
func SplitHostPath(raw string) (host string, path string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	// Letra de unidade Windows (ex: C:\, W:/)
	if len(raw) >= 2 && raw[1] == ':' && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) {
		return "local", raw
	}
	// Prefixo UNC (ex: \\server\share)
	if strings.HasPrefix(raw, `\\`) {
		return "local", raw
	}
	if idx := strings.Index(raw, ":"); idx > 1 {
		potentialHost := strings.ToLower(raw[:idx])
		if potentialHost != "http" && potentialHost != "https" && potentialHost != "file" && potentialHost != "ws" && potentialHost != "wss" {
			return potentialHost, raw[idx+1:]
		}
	}
	return "", raw
}

// ResolveTargetHost deduz o host para operações de arquivos:
// se host não informado e path tem volume Windows (ex: C:\, D:\) ou prefixo UNC (\\), usa "local";
// caso contrário delega a transport.ResolveHost (o host padrão da config).
func ResolveTargetHost(host, path string) (transport.HostTarget, error) {
	if h, p := SplitHostPath(path); h != "" {
		if transport.IsDefaultName(host) {
			host = h
		}
		path = p
	}
	if host == "" {
		if runtime.GOOS != "windows" {
			host = "local"
		} else if filepath.VolumeName(path) != "" || strings.HasPrefix(path, `\\`) {
			host = "local"
		}
	}
	t, err := transport.ResolveHost(host)
	if err == nil && !t.IsLocal && isWindowsPath(path) {
		// os hosts remotos são Linux/OpenWrt: lá 'W:\x' viraria uma pasta chamada 'W:' (ou um nome com barras invertidas)
		return t, fmt.Errorf("path '%s' is a Windows path, but host '%s' is Linux: nothing was read or written. "+
			"For a file on this Windows computer use host='local'; for %s use its Linux path (e.g. /home/...)", path, t.Name, t.Name)
	}
	return t, err
}

// isWindowsPath: letra de unidade seguida de separador ou fim (C:, W:\x, d:/x) ou prefixo UNC (\\server).
func isWindowsPath(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0] | 0x20
	return c >= 'a' && c <= 'z' && (len(p) == 2 || p[2] == '\\' || p[2] == '/')
}

// HandleReadFile is the model-facing read. On top of handleReadFileInner it
// applies, in order: graph first for big source files (outline instead of text),
// the size cap for other big reads, and the graph hint on the 3rd read of a
// file. Repeating the exact same call returns the full text (no lost
// functionality). Internal callers (edit_file, code_outline) use
// handleReadFileInner and are never affected.
func HandleReadFile(args ReadFileArgs) (string, error) {
	out, err := handleReadFileInner(args)
	if err != nil {
		return out, err
	}
	noSlice := args.Offset == 0 && args.Limit == 0 && args.Ranges == "" && args.Pattern == ""
	whole := noSlice && args.StartLine == 0 && args.EndLine == 0
	lineRange := noSlice && !whole
	// chaves do escape "repita para o texto completo" só com o pedido, nunca com a identidade (graph_first.go)
	switch {
	case whole:
		if g, ok, note := graphFirstRead(args, out, 0, 0); ok {
			out = g
		} else if note != "" {
			out = strings.TrimRight(out, "\r\n") + "\n\n" + note
		} else {
			out = capReadText(out, args.Path, "the whole file", "cap|"+strings.ToLower(args.Host+"|"+args.Path))
		}
	case lineRange && (args.EndLine == 0 || args.EndLine-max(args.StartLine, 1)+1 > graphMinLines):
		if full, ferr := handleReadFileInner(ReadFileArgs{Host: args.Host, Path: args.Path}); ferr == nil {
			if g, ok, note := graphFirstRead(args, full, args.StartLine, args.EndLine); ok {
				out = g
				break
			} else if note != "" {
				out = strings.TrimRight(out, "\r\n") + "\n\n" + note
				break
			}
		}
		label := fmt.Sprintf("lines %d-%d", max(args.StartLine, 1), args.EndLine)
		out = capReadText(out, args.Path, label, fmt.Sprintf("cap|%s|%d-%d", strings.ToLower(args.Host+"|"+args.Path), args.StartLine, args.EndLine))
	}
	if noteRead(identity(args.Caller, args.Conv), args.Host, args.Path) == rereadHintAt {
		out = strings.TrimRight(out, "\r\n") + "\n\n[FastExec Hint] This file was read several times recently. To locate code without re-reading: code_outline(path, symbol='name') gives the line; code_graph(action='callers') lists who calls it; read_file(pattern='regex', context=3) returns only matching lines."
	}
	return out, err
}

func handleReadFileInner(args ReadFileArgs) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("parameter 'path' is required")
	}

	target, err := ResolveTargetHost(args.Host, args.Path)
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
			return "", notFoundWithSuggestions(filePath, err)
		}
		if sel, handled, serr := selectLines(string(data), args); handled {
			return sel, serr
		}
		if args.StartLine > 0 || args.EndLine > 0 {
			lines := strings.Split(string(data), "\n")
			start := args.StartLine
			if start <= 0 {
				start = 1
			}
			if start > len(lines) {
				return "", nil
			}
			end := args.EndLine
			if end <= 0 || end > len(lines) {
				end = len(lines)
			}
			if start > end {
				return "", nil
			}
			return strings.Join(lines[start-1:end], "\n"), nil
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
	var readCmd string
	if args.StartLine > 0 || args.EndLine > 0 {
		start := args.StartLine
		if start <= 0 {
			start = 1
		}
		endStr := "$"
		if args.EndLine > 0 {
			endStr = fmt.Sprintf("%d", args.EndLine)
		}
		readCmd = fmt.Sprintf("sed -n '%d,%sp' \"$TARGET\"", start, endStr)
	} else {
		readCmd = "cat \"$TARGET\""
	}
	cmd := fmt.Sprintf("TARGET='%s'; TARGET=\"${TARGET/#\\~/$HOME}\"; if [ ! -f \"$TARGET\" ]; then echo '__FILE_NOT_FOUND__' >&2; exit 1; fi; %s", filePath, readCmd)
	res, err := transport.ExecDaemon(target.TCPAddr, cmd, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("could not read the remote file: %s", strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

func HandleWriteFile(args WriteFileArgs) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("parameter 'path' is required")
	}

	target, err := ResolveTargetHost(args.Host, args.Path)
	if err != nil {
		return "", err
	}

	contentBytes := []byte(args.Content)

	if !args.Overwrite {
		if size, exists := existingNonEmpty(target, args.Path); exists {
			return "", overwriteRefused(args.Path, size)
		}
	}

	if target.IsLocal {
		oldText := ""
		if args.Overwrite {
			oldText = oldContentForRewrite(args.Path) // para o aviso de reescrita (rewrite_hint.go)
		}
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
		graph.Touch(args.Path) // índice de código: reindexa em segundo plano se o repositório já é indexado
		if h := rewriteHint(oldText, args.Content); h != "" {
			return okReply(int64(len(contentBytes))) + "\n" + h, nil
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
				return "", fmt.Errorf("could not write chunk %d/%d: %v", i+1, numChunks, err)
			}
		}
	}

	if args.Atomic {
		renameCmd := fmt.Sprintf("mv -f '%s' '%s'", tmpRemote, normalizedPath)
		res, err := transport.ExecDaemon(target.TCPAddr, renameCmd, transport.DefaultTimeout)
		if err != nil || res.ExitCode != 0 {
			return "", fmt.Errorf("remote atomic rename failed: %v", err)
		}
	}

	return okReply(int64(len(contentBytes))), nil
}

func HandleGetFile(args GetFileArgs) (string, error) {
	if args.RemotePath == "" {
		return "", fmt.Errorf("parameter 'remote_path' is required")
	}

	if h, p := SplitHostPath(args.RemotePath); h != "" && h != "local" {
		if transport.IsDefaultName(args.Host) {
			args.Host = h
		}
		args.RemotePath = p
	}

	target, err := ResolveTargetHost(args.Host, args.RemotePath)
	if err != nil {
		return "", err
	}

	localPath := args.LocalPath
	if localPath == "" {
		localPath = filepath.Base(args.RemotePath)
	}

	if target.IsLocal {
		fi, err := os.Stat(args.RemotePath)
		if err != nil {
			return "", fmt.Errorf("local file not found: %w", err)
		}
		data, err := os.ReadFile(args.RemotePath)
		if err != nil {
			return "", err
		}
		if dir := filepath.Dir(localPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("could not create local directory: %w", err)
			}
		}
		if err := os.WriteFile(localPath, data, 0644); err != nil {
			return "", fmt.Errorf("could not write local file: %w", err)
		}
		return okReply(fi.Size()), nil
	}

	written, err := transport.GetFileDaemon(target.TCPAddr, args.RemotePath, localPath, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}

	return okReply(written), nil
}

func HandlePutFile(args PutFileArgs) (string, error) {
	if args.LocalPath == "" || args.RemotePath == "" {
		return "", fmt.Errorf("parameters 'local_path' and 'remote_path' are required")
	}

	// Normalização tolerante de prefixo host:path no remote_path
	if h, p := SplitHostPath(args.RemotePath); h != "" && h != "local" {
		if transport.IsDefaultName(args.Host) {
			args.Host = h
		}
		args.RemotePath = p
	}
	// Se os argumentos vieram invertidos (localPath com prefixo remoto e remotePath local)
	if h, p := SplitHostPath(args.LocalPath); h != "" && h != "local" {
		if hDst, _ := SplitHostPath(args.RemotePath); hDst == "" || hDst == "local" {
			args.Host = h
			realRemote := p
			realLocal := args.RemotePath
			args.LocalPath = realLocal
			args.RemotePath = realRemote
		}
	}

	target, err := ResolveTargetHost(args.Host, args.RemotePath)
	if err != nil {
		return "", err
	}

	fileInfo, err := os.Stat(args.LocalPath)
	if err != nil {
		return "", fmt.Errorf("local file not found: %w", err)
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

func HandleEditFile(args EditFileArgs) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("parameter 'path' is required")
	}
	ops := args.Edits
	if args.Target != "" {
		ops = append([]EditOp{{Target: args.Target, Replacement: args.Replacement}}, ops...)
	}
	if len(ops) == 0 {
		return "", fmt.Errorf("parameter 'target' (or 'edits') is required")
	}

	host := args.Host
	if host == "" && (filepath.VolumeName(args.Path) != "" || strings.HasPrefix(args.Path, `\\`)) {
		host = "local"
	}

	content, err := handleReadFileInner(ReadFileArgs{
		Host: host,
		Path: args.Path,
	})
	if err != nil {
		return "", fmt.Errorf("could not read '%s' to edit it: %w", args.Path, err)
	}

	// todas as trocas em memória, em ordem; qualquer target ausente aborta sem gravar nada
	newContent := content
	oldBytes, newBytes := 0, 0
	var notes []string
	for i, op := range ops {
		label := "target"
		if len(ops) > 1 {
			label = fmt.Sprintf("edits[%d].target", i)
		}
		if op.Target == "" {
			return "", fmt.Errorf("%s is empty; nothing was written", label)
		}
		t, r := matchEOL(newContent, op.Target, op.Replacement)
		n := strings.Count(newContent, t)
		if n == 0 {
			if matched, desc, ok := matchRemediation(args.Path, newContent, op.Target, r); ok {
				t = matched
				t, r = matchEOL(newContent, t, r)
				n = strings.Count(newContent, t)
				notes = append(notes, fmt.Sprintf("[FastExec Auto-Remediation] %s matched %s; applied automatically", label, desc))
			}
		}
		if n == 0 {
			msg := fmt.Sprintf("%s not found in '%s'; nothing was written", label, args.Path)
			if h := nearMiss(newContent, op.Target); h != "" {
				msg += "\n" + h
			}
			return "", fmt.Errorf("%s", msg)
		}
		if n > 1 {
			notes = append(notes, fmt.Sprintf("%s matched %d times; only the first was changed", label, n))
		}
		newContent = strings.Replace(newContent, t, r, 1)
		oldBytes += len(t)
		newBytes += len(r)
	}

	_, err = HandleWriteFile(WriteFileArgs{
		Host:      host,
		Path:      args.Path,
		Content:   newContent,
		Atomic:    true,
		Overwrite: true,
	})
	if err != nil {
		return "", fmt.Errorf("could not write '%s': %w", args.Path, err)
	}

	msg := fmt.Sprintf("[edit_file] %d change(s) applied (%d -> %d bytes) in %s.", len(ops), oldBytes, newBytes, args.Path)
	if len(notes) > 0 {
		msg += " Note: " + strings.Join(notes, "; ") + "."
	}
	if h := editBatchHint(identity(args.Caller, args.Conv), args.Path, len(ops)); h != "" {
		msg += "\n" + h
	}
	return msg, nil
}

func okReply(n int64) string {
	return fmt.Sprintf("ok %d bytes", n)
}
