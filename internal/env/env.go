package env

import (
	"fmt"
	"os"
	"strings"
)

var soDoSistema = map[string]bool{
	"username": true,
}

// Ensure define uma variável no mapa, eliminando duplicatas case-insensitive.
// Se houver chave diferente com o mesmo nome (case-insensitive), a antiga é removida.
func Ensure(envMap map[string]string, key, value string) {
	// Remove entradas com o mesmo nome mas caixa diferente
	for k := range envMap {
		if strings.EqualFold(k, key) && k != key {
			delete(envMap, k)
		}
	}
	envMap[key] = value
}

// isPath testa se a chave é "path" (case-insensitive)
func isPath(key string) bool {
	return strings.EqualFold(key, "path")
}

// BuildEnv monta o ambiente Windows a partir do registro + herdado.
// Segue a regra dc-launcher.js:
// - Começa com herdado
// - Adiciona Machine (exceto Path e SO_DO_SISTEMA)
// - Adiciona User (exceto Path)
// - Path = Machine + User + herdado sem duplicata
func BuildEnv(inherited []string, registry *RegistryEnv) map[string]string {
	envMap := make(map[string]string)

	// Passa 1: herdado
	for _, pair := range inherited {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if registry == nil {
		return envMap
	}

	// Passa 2: Machine (exceto Path e SO_DO_SISTEMA)
	for k, v := range registry.Machine {
		if !isPath(k) && !soDoSistema[strings.ToLower(k)] {
			Ensure(envMap, k, v)
		}
	}

	// Passa 3: User (exceto Path)
	for k, v := range registry.User {
		if !isPath(k) {
			Ensure(envMap, k, v)
		}
	}

	// Passa 4: Path especial = Machine + User + herdado (sem duplicata)
	pathM := ""
	for k, v := range registry.Machine {
		if isPath(k) {
			pathM = v
			break
		}
	}
	pathU := ""
	for k, v := range registry.User {
		if isPath(k) {
			pathU = v
			break
		}
	}

	doRegistro := []string{}
	if pathM != "" {
		doRegistro = append(doRegistro, strings.Split(pathM, ";")...)
	}
	if pathU != "" {
		doRegistro = append(doRegistro, strings.Split(pathU, ";")...)
	}

	seen := make(map[string]bool)
	for _, p := range doRegistro {
		if p != "" {
			seen[strings.ToLower(p)] = true
		}
	}

	herdadoPath := ""
	for k, v := range envMap {
		if isPath(k) {
			herdadoPath = v
			break
		}
	}

	extras := []string{}
	if herdadoPath != "" {
		for _, p := range strings.Split(herdadoPath, ";") {
			if p != "" && !seen[strings.ToLower(p)] {
				extras = append(extras, p)
				seen[strings.ToLower(p)] = true
			}
		}
	}

	finalPath := []string{}
	for _, p := range doRegistro {
		if p != "" {
			finalPath = append(finalPath, p)
		}
	}
	finalPath = append(finalPath, extras...)

	Ensure(envMap, "Path", strings.Join(finalPath, ";"))

	return envMap
}

// ToSlice converte mapa para []string no formato KEY=VALUE, pronto para exec.Cmd.Env
func ToSlice(envMap map[string]string) []string {
	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

// MountForCurrentProcess lê o registro e monta o ambiente para o processo atual.
// Falha ao ler registro: loga e retorna o herdado (nunca falha).
func MountForCurrentProcess() []string {
	reg, err := ReadRegistry()
	if err != nil {
		// Log, mas não falha: processo segue com o herdado
		fmt.Fprintf(os.Stderr, "[env] registry unreadable, using the inherited environment: %v\n", err)
		return os.Environ()
	}

	envMap := BuildEnv(os.Environ(), reg)
	return ToSlice(envMap)
}
