package mcp

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorCause(t *testing.T) {
	err := errors.New("comando falhou com código 2")
	msg, code := errorCause(err, "[exit 2] grep: nao_existe.txt: No such file or directory\nsegunda linha")
	if msg != "comando falhou com código 2: grep: nao_existe.txt: No such file or directory" || code != 2 {
		t.Errorf("exec com stderr: %q %d", msg, code)
	}

	msg, code = errorCause(err, "[exit -1] \n\n  'rg' is not recognized as an internal or external command")
	if !strings.HasSuffix(msg, ": 'rg' is not recognized as an internal or external command") || code != -1 {
		t.Errorf("código negativo e linhas vazias: %q %d", msg, code)
	}

	guard := errors.New("[FastExec Guard] Recursive scan of 'C:\\' blocked")
	msg, code = errorCause(guard, "")
	if msg != guard.Error() || code != 1 {
		t.Errorf("sem texto: %q %d", msg, code)
	}
	if msg, _ = errorCause(guard, guard.Error()); msg != guard.Error() {
		t.Errorf("texto igual ao erro não duplica: %q", msg)
	}

	long := strings.Repeat("x", 500)
	if msg, _ = errorCause(err, "[exit 1] "+long); len([]rune(msg)) > len([]rune(err.Error()))+2+301 {
		t.Errorf("causa deveria ser cortada em 300: %d runas", len([]rune(msg)))
	}
}
