package tools

import "testing"

func TestStyleHintGoFormat(t *testing.T) {
	bad := "package a\n\nfunc f() {\n    x := 1\n    _ = x\n}\n"
	if styleHint("a.go", bad) == "" {
		t.Fatal("gofmt: indentacao com espacos deveria acusar")
	}
	good := "package a\n\nfunc f() {\n\tx := 1\n\t_ = x\n}\n"
	if styleHint("a.go", good) != "" {
		t.Fatalf("gofmt: arquivo formatado nao deveria acusar: %q", styleHint("a.go", good))
	}
}

func TestJSONDupKey(t *testing.T) {
	if k, l := jsonDupKey("{\"a\":1,\"b\":2}"); k != "" {
		t.Fatalf("sem duplicata: %q linha %d", k, l)
	}
	if k, _ := jsonDupKey("{\"a\":1,\"a\":2}"); k != "a" {
		t.Fatalf("duplicata simples: %q", k)
	}
	if k, _ := jsonDupKey("{\"x\":{\"y\":1,\"y\":2}}"); k != "y" {
		t.Fatalf("duplicata aninhada: %q", k)
	}
	if k, _ := jsonDupKey("{\"x\":{\"a\":1},\"y\":{\"a\":2}}"); k != "" {
		t.Fatalf("objetos distintos nao sao duplicata: %q", k)
	}
	if s := styleHint("a.json", "{\"a\":1,\"a\":2}"); s == "" {
		t.Fatal("styleHint json deveria acusar chave repetida")
	}
}
