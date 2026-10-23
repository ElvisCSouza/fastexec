package env

import (
	"strings"
	"testing"
)

func TestEnsureRemovesDuplicatesCaseInsensitive(t *testing.T) {
	env := map[string]string{
		"PATH": "/usr/bin",
	}

	Ensure(env, "Path", "/usr/local/bin")

	if len(env) != 1 {
		t.Fatalf("esperava 1 entrada, got %d", len(env))
	}

	if env["Path"] != "/usr/local/bin" {
		t.Errorf("esperava Path=/usr/local/bin, got %v", env)
	}

	if _, ok := env["PATH"]; ok {
		t.Error("PATH (maiúscula) não deveria existir após Ensure com Path (minúscula)")
	}
}

func TestBuildEnvMergeOrder(t *testing.T) {
	inherited := []string{
		"GOPATH=C:\\Users\\elvis\\go",
		"GRADLE_USER_HOME=C:\\gradle_old",
		"Path=C:\\Windows\\System32",
	}

	registry := &RegistryEnv{
		Machine: map[string]string{
			"GRADLE_USER_HOME": "C:\\gradle",
			"JAVA_HOME":        "C:\\Program Files\\Java",
		},
		User: map[string]string{
			"GOMODCACHE": "C:\\Users\\elvis\\go\\pkg\\mod",
		},
	}

	env := BuildEnv(inherited, registry)

	// GRADLE_USER_HOME deve vir do registro (Machine > herdado)
	if env["GRADLE_USER_HOME"] != "C:\\gradle" {
		t.Errorf("GRADLE_USER_HOME: esperava C:\\gradle, got %s", env["GRADLE_USER_HOME"])
	}

	// GOPATH deve permanecer do herdado (não em registry)
	if env["GOPATH"] != "C:\\Users\\elvis\\go" {
		t.Errorf("GOPATH: esperava C:\\Users\\elvis\\go, got %s", env["GOPATH"])
	}

	// JAVA_HOME deve vir de Machine
	if env["JAVA_HOME"] != "C:\\Program Files\\Java" {
		t.Errorf("JAVA_HOME: esperava C:\\Program Files\\Java, got %s", env["JAVA_HOME"])
	}

	// GOMODCACHE deve vir de User
	if env["GOMODCACHE"] != "C:\\Users\\elvis\\go\\pkg\\mod" {
		t.Errorf("GOMODCACHE: esperava C:\\Users\\elvis\\go\\pkg\\mod, got %s", env["GOMODCACHE"])
	}
}

func TestBuildEnvPathNoDuplicate(t *testing.T) {
	inherited := []string{
		"Path=C:\\herdado1;C:\\herdado2",
	}

	registry := &RegistryEnv{
		Machine: map[string]string{
			"Path": "C:\\machine1;C:\\machine2;C:\\herdado1", // herdado1 vem de Machine
		},
		User: map[string]string{
			"Path": "C:\\user1;C:\\user2",
		},
	}

	env := BuildEnv(inherited, registry)

	pathStr := env["Path"]
	parts := strings.Split(pathStr, ";")

	// Verifica se não há duplicatas
	seen := make(map[string]int)
	for _, p := range parts {
		lower := strings.ToLower(p)
		seen[lower]++
		if seen[lower] > 1 {
			t.Errorf("Path duplicada (case-insensitive): %s", p)
		}
	}

	// Verifica ordem: machine, user, extras do herdado
	expectedOrder := []string{"C:\\machine1", "C:\\machine2", "C:\\herdado1", "C:\\user1", "C:\\user2", "C:\\herdado2"}
	if pathStr != strings.Join(expectedOrder, ";") {
		t.Errorf("Path order incorreta.\nEsperado: %s\nGot:      %s", strings.Join(expectedOrder, ";"), pathStr)
	}
}

func TestBuildEnvPathCaseInsensitiveHeader(t *testing.T) {
	inherited := []string{
		"PATH=C:\\herdado", // PATH (maiúscula)
	}

	registry := &RegistryEnv{
		Machine: map[string]string{
			"path": "C:\\machine", // path (minúscula)
		},
		User: map[string]string{},
	}

	env := BuildEnv(inherited, registry)

	// Deve haver exatamente uma entrada de Path, com a chave correta (a última processada)
	pathCount := 0
	var pathKey string
	for k := range env {
		if strings.EqualFold(k, "path") {
			pathCount++
			pathKey = k
		}
	}

	if pathCount != 1 {
		t.Errorf("esperava exatamente 1 entrada Path, got %d", pathCount)
	}

	if !strings.Contains(env[pathKey], "C:\\machine") || !strings.Contains(env[pathKey], "C:\\herdado") {
		t.Errorf("Path não contém expected values: %s", env[pathKey])
	}
}

func TestBuildEnvFiltersSystemOnly(t *testing.T) {
	inherited := []string{}

	registry := &RegistryEnv{
		Machine: map[string]string{
			"USERNAME":  "SYSTEM", // SO_DO_SISTEMA
			"JAVA_HOME": "C:\\Java",
		},
		User: map[string]string{},
	}

	env := BuildEnv(inherited, registry)

	if _, ok := env["USERNAME"]; ok {
		t.Error("USERNAME (SYSTEM) não deveria estar no env")
	}

	if env["JAVA_HOME"] != "C:\\Java" {
		t.Errorf("JAVA_HOME deveria estar no env, got %v", env)
	}
}

func TestToSlice(t *testing.T) {
	envMap := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}

	slice := ToSlice(envMap)

	if len(slice) != 2 {
		t.Fatalf("esperava 2 entradas, got %d", len(slice))
	}

	sliceStr := strings.Join(slice, ";")
	if !strings.Contains(sliceStr, "FOO=bar") || !strings.Contains(sliceStr, "BAZ=qux") {
		t.Errorf("slice não contém expected entries: %v", slice)
	}
}

func TestBuildEnvEmptyRegistry(t *testing.T) {
	inherited := []string{
		"MYVAR=myvalue",
	}

	env := BuildEnv(inherited, nil)

	if env["MYVAR"] != "myvalue" {
		t.Error("env com registry=nil deveria manter o herdado")
	}
}

func TestBuildEnvEmptyInheritedRegistry(t *testing.T) {
	registry := &RegistryEnv{
		Machine: map[string]string{
			"JAVA_HOME": "C:\\Java",
		},
		User: map[string]string{
			"GOPATH": "C:\\go",
		},
	}

	env := BuildEnv([]string{}, registry)

	if env["JAVA_HOME"] != "C:\\Java" {
		t.Errorf("JAVA_HOME deveria vir de Machine, got %v", env)
	}

	if env["GOPATH"] != "C:\\go" {
		t.Errorf("GOPATH deveria vir de User, got %v", env)
	}
}

func TestBuildEnvPathFromAllSources(t *testing.T) {
	inherited := []string{
		"Path=C:\\herdado",
	}

	registry := &RegistryEnv{
		Machine: map[string]string{
			"Path": "C:\\machine",
		},
		User: map[string]string{
			"Path": "C:\\user",
		},
	}

	env := BuildEnv(inherited, registry)

	pathStr := env["Path"]
	parts := strings.Split(pathStr, ";")

	if len(parts) != 3 {
		t.Fatalf("esperava 3 partes em Path, got %d: %v", len(parts), parts)
	}

	if parts[0] != "C:\\machine" || parts[1] != "C:\\user" || parts[2] != "C:\\herdado" {
		t.Errorf("Path order incorreta: %v", parts)
	}
}
