package lsp

import "testing"

func TestPublishIsFresh(t *testing.T) {
	cases := []struct {
		name          string
		seq, before   uint64
		ver, want     int
		sent, expects bool
	}{
		{"nunca publicou", 0, 0, -1, 1, true, false},
		{"versão antiga é descartada", 5, 4, 2, 3, true, false},
		{"versão igual vale", 5, 4, 3, 3, true, true},
		{"versão mais nova vale", 5, 4, 4, 3, true, true},
		{"sem versão, publish posterior ao envio vale", 5, 4, -1, 3, true, true},
		{"sem versão, nenhum publish depois do envio não vale", 4, 4, -1, 3, true, false},
		{"sem versão e nada enviado: o último publish descreve os mesmos bytes", 4, 4, -1, 3, false, true},
		{"com versão, nada enviado e publish da versão atual", 4, 4, 3, 3, false, true},
	}
	for _, c := range cases {
		if got := publishIsFresh(c.seq, c.before, c.ver, c.want, c.sent); got != c.expects {
			t.Errorf("%s: got %v, want %v", c.name, got, c.expects)
		}
	}
}
