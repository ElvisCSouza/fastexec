package codescan

import (
	"path/filepath"
	"strings"
)

// NoiseDirs e NoiseFiles: dependências, builds, caches e artefatos que nenhuma busca de código deve varrer.
var (
	NoiseDirs  = []string{".git", "node_modules", "dist", "build", "vendor", ".next", ".agent", "tmp", "cache", "__pycache__"}
	NoiseFiles = []string{"*.min.js", "*.min.css", "*.map", "package-lock.json", "pnpm-lock.yaml"}
)

// IsNoiseDir: diretório de ruído ou oculto (.idea, .vscode...).
func IsNoiseDir(name string) bool {
	for _, d := range NoiseDirs {
		if strings.EqualFold(name, d) {
			return true
		}
	}
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

// IsNoiseFile: arquivo gerado ou lockfile.
func IsNoiseFile(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range NoiseFiles {
		if ok, _ := filepath.Match(pat, lower); ok {
			return true
		}
	}
	return false
}

// IsNoisePath: algum diretório do caminho relativo (com '/') é ruído, ou o arquivo é.
func IsNoisePath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if IsNoiseDir(p) {
			return true
		}
	}
	return IsNoiseFile(parts[len(parts)-1])
}
