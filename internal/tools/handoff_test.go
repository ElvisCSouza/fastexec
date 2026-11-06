package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandoffLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Save
	saveRes, err := handleHandoffSave(tmpDir, HandoffArgs{
		Action:  "save",
		Topic:   "teste-unidade",
		Notes:   "Implementação de testes do módulo handoff",
		Pending: "Adicionar benchmarks e deploy",
	})
	if err != nil {
		t.Fatalf("handleHandoffSave falhou: %v", err)
	}

	if !strings.Contains(saveRes, "latest.md") || !strings.Contains(saveRes, "teste-unidade") {
		t.Errorf("resposta inesperada do save: %s", saveRes)
	}

	latestPath := filepath.Join(tmpDir, "latest.md")
	data, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatalf("latest.md não foi criado: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Implementação de testes do módulo handoff") {
		t.Errorf("conteúdo de latest.md incorreto: %s", content)
	}
	if !strings.Contains(content, "Adicionar benchmarks e deploy") {
		t.Errorf("seção pendente não encontrada em latest.md: %s", content)
	}

	// 2. Latest
	latestRes, err := handleHandoffLatest(tmpDir)
	if err != nil {
		t.Fatalf("handleHandoffLatest falhou: %v", err)
	}
	if latestRes != content {
		t.Errorf("latestRes não bate com o arquivo gravado")
	}

	// 3. Search
	searchRes, err := handleHandoffSearch(tmpDir, "benchmarks", 5)
	if err != nil {
		t.Fatalf("handleHandoffSearch falhou: %v", err)
	}
	if !strings.Contains(searchRes, "SEARCH RESULTS") || !strings.Contains(searchRes, "Adicionar benchmarks") {
		t.Errorf("search não encontrou a palavra-chave: %s", searchRes)
	}

	// 4. Search sem resultado
	searchNotFound, err := handleHandoffSearch(tmpDir, "palavrainexistente12345", 5)
	if err != nil {
		t.Fatalf("searchNotFound retornou erro inesperado: %v", err)
	}
	if !strings.Contains(searchNotFound, "No match") {
		t.Errorf("esperava mensagem amigável de não encontrado: %s", searchNotFound)
	}

	// 5. List
	listRes, err := handleHandoffList(tmpDir, 5)
	if err != nil {
		t.Fatalf("handleHandoffList falhou: %v", err)
	}
	if !strings.Contains(listRes, "teste-unidade") {
		t.Errorf("list não contém o arquivo recém-criado: %s", listRes)
	}
}

func TestHandoffUnmarshalJSON(t *testing.T) {
	raw := []byte(`{
		"op": "generate",
		"title": "meu-projeto",
		"summary": "resumo do projeto",
		"todos": "finalizar documentacao",
		"q": "termo"
	}`)

	var args HandoffArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("falha ao deserializar HandoffArgs: %v", err)
	}

	if args.Action != "generate" {
		t.Errorf("esperava Action 'generate', obteve '%s'", args.Action)
	}
	if args.Topic != "meu-projeto" {
		t.Errorf("esperava Topic 'meu-projeto', obteve '%s'", args.Topic)
	}
	if args.Notes != "resumo do projeto" {
		t.Errorf("esperava Notes 'resumo do projeto', obteve '%s'", args.Notes)
	}
	if args.Pending != "finalizar documentacao" {
		t.Errorf("esperava Pending 'finalizar documentacao', obteve '%s'", args.Pending)
	}
	if args.Query != "termo" {
		t.Errorf("esperava Query 'termo', obteve '%s'", args.Query)
	}
}
