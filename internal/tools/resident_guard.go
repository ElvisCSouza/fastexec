package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var reBuildCommand = regexp.MustCompile(`(?i)\b(?:go\s+build|go\s+install|cargo\s+build|dotnet\s+build|npm\s+run\s+build|pnpm\s+(?:run\s+)?build|yarn\s+build|npx\s+tsc|zig\s+build|make\b|ninja\b|deploy\.ps1)`)

// checkResidentService verifica após um build bem-sucedido se há um processo residente
// rodando com o mesmo nome do binário compilado que seja anterior ao build atual.
func checkResidentService(cwd, command, host string) string {
	if host != "" && host != "local" {
		return ""
	}
	if !reBuildCommand.MatchString(command) {
		return ""
	}

	root := cwd
	if root == "" {
		// Tenta extrair diretório de comandos encadeados com "cd" ou "cd /d"
		if cdMatch := regexp.MustCompile(`(?i)\bcd\s+(?:/d\s+)?([^\s&;]+)`).FindStringSubmatch(command); len(cdMatch) > 1 {
			root = strings.Trim(cdMatch[1], `"'`)
		} else if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if root == "" {
		return ""
	}

	// 1. Coleta candidatos a binários recém-criados ou modificados nos últimos 15 segundos
	candidates := findRecentBinaries(root, 15*time.Second)

	// Também deduz do comando se foi algo como `go build ./cmd/xyz` ou `-o output.exe`
	if inferred := inferBinaryFromCommand(command); inferred != "" {
		candidates = appendUnique(candidates, inferred)
	}

	if len(candidates) == 0 {
		return ""
	}

	var warnings []string
	now := time.Now()

	for _, cand := range candidates {
		baseName := filepath.Base(cand)
		cleanName := strings.TrimSuffix(baseName, ".exe")

		procs := findRunningProcessesByName(cleanName)
		for _, p := range procs {
			// Se o processo começou antes de agora menos 2 segundos (ou seja, já estava rodando antes do build)
			if !p.StartTime.IsZero() && now.Sub(p.StartTime) > 2*time.Second {
				startTimeStr := p.StartTime.Format("15:04:05")
				warnings = append(warnings, fmt.Sprintf(
					"[FastExec Guard] Resident process '%s' (PID %d) is active (started at %s, before this build).\nRestart or deploy the process before testing, otherwise manual trials and tests will target the stale instance.",
					p.Name, p.PID, startTimeStr,
				))
			}
		}
	}

	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n\n")
}

func findRecentBinaries(dir string, maxAge time.Duration) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var res []string
	now := time.Now()

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		name := e.Name()
		isExe := strings.HasSuffix(strings.ToLower(name), ".exe") || (info.Mode()&0111 != 0)
		if isExe && now.Sub(info.ModTime()) <= maxAge {
			res = append(res, filepath.Join(dir, name))
		}
	}
	return res
}

func inferBinaryFromCommand(cmd string) string {
	parts := strings.Fields(cmd)
	for i, p := range parts {
		if (p == "-o" || p == "-output") && i+1 < len(parts) {
			return filepath.Base(parts[i+1])
		}
		if strings.HasPrefix(p, "-o=") {
			return filepath.Base(strings.TrimPrefix(p, "-o="))
		}
		if strings.Contains(p, "cmd/") || strings.Contains(p, `cmd\`) {
			clean := filepath.Base(p)
			if clean != "." && clean != "cmd" {
				return clean
			}
		}
	}
	return ""
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if strings.EqualFold(filepath.Base(s), filepath.Base(item)) {
			return slice
		}
	}
	return append(slice, item)
}
