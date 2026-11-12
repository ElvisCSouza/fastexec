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
	name := parts[len(parts)-1]
	if IsCSharpName(name) && csNoisePath(parts[:len(parts)-1], name) {
		return true
	}
	return IsNoiseFile(name)
}

// Ruído do .NET: saída de build (bin/obj) e arquivos gerados pelas ferramentas. O filtro vale só para .cs/.csproj/.sln
// porque "bin" e "obj" são nomes comuns em outras linguagens (bin/cli.js) e não podem sair do índice delas.
var csNoiseDirs = map[string]bool{"bin": true, "obj": true}

var csNoiseSuffix = []string{".g.cs", ".g.i.cs", ".designer.cs", ".assemblyinfo.cs"}

// IsCSharpName: extensão de arquivo C#/.NET (.cs de código, .csproj/.sln de projeto).
func IsCSharpName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".cs", ".csproj", ".sln":
		return true
	}
	return false
}

// IsNoiseCSharp: arquivo C#/.NET gerado ou dentro de bin/obj. Aceita caminho com '/' ou '\' (o da varredura de árvore
// é absoluto do sistema), daí não usar IsNoisePath direto.
func IsNoiseCSharp(path string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 || !IsCSharpName(parts[len(parts)-1]) {
		return false
	}
	return csNoisePath(parts[:len(parts)-1], parts[len(parts)-1])
}

func csNoisePath(dirs []string, name string) bool {
	for _, d := range dirs {
		if csNoiseDirs[strings.ToLower(d)] {
			return true
		}
	}
	lower := strings.ToLower(name)
	for _, s := range csNoiseSuffix {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}
