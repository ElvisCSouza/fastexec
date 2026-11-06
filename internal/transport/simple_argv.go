package transport

import (
	"os"
	"strings"
)

// SimpleArgv decide se uma linha de shell é só "programa arg arg ..." e, se for, devolve o argv.
// É função pura: o mesmo texto dá sempre a mesma resposta. Só aceita texto em que o bash não faria nada além de
// separar por espaço e executar: nenhum metacaractere (aspas, $, `, |, &, ;, <, >, (, ), *, ?, [, ], {, }, ~, !, #,
// \, quebra de linha), primeiro termo que não é comando interno nem palavra reservada do shell e que não é uma
// atribuição (VAR=x). Na dúvida devolve false e a linha vai pelo bash, como sempre foi.
//
// Desliga-se com FASTEXEC_NO_DIRECT=1.
func SimpleArgv(cmd string) ([]string, bool) {
	if os.Getenv("FASTEXEC_NO_DIRECT") != "" {
		return nil, false
	}
	if cmd == "" || len(cmd) > 8192 {
		return nil, false
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == ' ':
		case strings.IndexByte("_./:=,+-@%^", c) >= 0:
		default:
			return nil, false
		}
	}
	argv := strings.Fields(cmd)
	if len(argv) == 0 {
		return nil, false
	}
	first := argv[0]
	if strings.ContainsRune(first, '=') || first[0] == '-' {
		return nil, false
	}
	if shellWords[first] {
		return nil, false
	}
	return argv, true
}

// shellWords: comandos internos do bash e palavras reservadas. Têm de passar pelo bash.
var shellWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`. : [ alias bg bind break builtin caller cd command compgen complete compopt
		continue declare dirs disown echo enable eval exec exit export false fc fg getopts hash help history jobs
		kill let local logout mapfile popd printf pushd pwd read readarray readonly return set shift shopt source
		suspend test times trap true type typeset ulimit umask unalias unset wait
		if then else elif fi case esac for select while until do done in function time { } [[ ]] ! coproc`) {
		m[w] = true
	}
	return m
}()
