package lsp

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Projetos com <TargetFrameworks> (a maioria das bibliotecas NuGet) deixam o csharp-ls 0.16 com um workspace vazio:
// a solução "carrega", nenhum documento entra e toda consulta volta sem resposta. Medido no Serilog e no
// FluentValidation: com um único destino o mesmo servidor responde hover, símbolos e 157 referências.
//
// A saída sem tocar no repositório é a variável de ambiente TargetFramework no processo do servidor. O MSBuild a
// lê como propriedade; um projeto que declara o próprio <TargetFramework> a sobrescreve (arquivo vence ambiente),
// e só os projetos de vários destinos passam a avaliar um destino, o que tira o build de "cross-targeting".

var multiTFMRe = regexp.MustCompile(`(?is)<TargetFrameworks>\s*([^<]+?)\s*</TargetFrameworks>`)

// multiTargetFramework escolhe o destino único para os projetos de vários destinos sob root, ou "" quando não há
// nenhum. sdkMajor é o SDK que vai avaliar o projeto: um destino acima dele não tem pacote de referência.
func multiTargetFramework(root string, sdkMajor int) string {
	seen := map[string]bool{}
	multi := false
	for _, p := range walkProjects(root) {
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		m := multiTFMRe.FindSubmatch(body)
		if m == nil {
			continue
		}
		multi = true
		for _, tfm := range strings.Split(string(m[1]), ";") {
			if tfm = strings.ToLower(strings.TrimSpace(tfm)); tfm != "" && !strings.Contains(tfm, "$(") {
				seen[tfm] = true
			}
		}
	}
	if !multi {
		return ""
	}
	return pickTFM(seen, sdkMajor)
}

// pickTFM prefere o netN.0 moderno mais alto que o SDK sabe compilar; sem ele, netcoreapp3.1 e depois
// netstandard2.1/2.0. Só destinos genéricos (sem sufixo de plataforma como -windows) entram.
func pickTFM(seen map[string]bool, sdkMajor int) string {
	best := 0
	for tfm := range seen {
		if n, ok := modernNet(tfm); ok && n <= sdkMajor && n > best {
			best = n
		}
	}
	if best > 0 {
		return "net" + strconv.Itoa(best) + ".0"
	}
	for _, c := range []string{"netcoreapp3.1", "netstandard2.1", "netstandard2.0"} {
		if seen[c] {
			return c
		}
	}
	return ""
}

// modernNet reconhece net5.0 em diante (netN.0, sem plataforma).
func modernNet(tfm string) (int, bool) {
	if !strings.HasPrefix(tfm, "net") || !strings.HasSuffix(tfm, ".0") || strings.ContainsAny(tfm, "-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(tfm, "net"), ".0"))
	if err != nil || n < 5 {
		return 0, false
	}
	return n, true
}

// resolvedSDKMajor é o SDK que o resolvedor usa para root: o que o global.json fixa (com roll-forward ele cai no
// mesmo major) ou, sem pino, o mais novo instalado.
func resolvedSDKMajor(root, sdkRoot string) int {
	if v, _ := pinnedSDK(root); v != "" {
		if n, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0]); err == nil {
			return n
		}
	}
	list := installedSDKs(sdkRoot)
	if len(list) == 0 {
		return 0
	}
	if n, err := strconv.Atoi(strings.SplitN(list[len(list)-1], ".", 2)[0]); err == nil {
		return n
	}
	return 0
}
