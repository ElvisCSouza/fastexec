package graph

import "testing"

func TestCallerLabel(t *testing.T) {
	cases := map[string]string{
		"handle\x1ffunc (s *servidor) handle(w http.ResponseWriter)": "servidor.handle",
		"handle\x1ffunc (servidor) handle()":                         "servidor.handle",
		"get\x1ffunc (c Cache[K, V]) get(k K) V":                     "Cache.get",
		"main\x1ffunc main()":                                        "main",
		"Run\x1fdef Run(self):":                                      "Run",
		"onlyName":                                                   "onlyName",
		"x\x1ffunc (s *a) y(f func(int) int)":                        "a.x",
	}
	for in, want := range cases {
		if got := callerLabel(in); got != want {
			t.Errorf("callerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
