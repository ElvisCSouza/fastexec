package mcp

import (
	"encoding/json"
	"testing"
)

func resetDocs() {
	docMu.Lock()
	docRecent = map[string][]*docSeen{}
	docMu.Unlock()
}

func rf(p string) json.RawMessage { b, _ := json.Marshal(map[string]string{"path": p}); return b }

func TestDocChainLinkDirNameNone(t *testing.T) {
	resetDocs()
	id := "conv:a"
	llm := "# Regras\nVeja [o hub do módulo](modulo_x/claude.md) e `_docs/homelab/`.\nTambém worker.md"
	doc, from, how, links := trackDoc(id, "read_file", "noteserver", rf("/home/elvis/workspace/LLM.md"), llm, true)
	if doc != "~/workspace/LLM.md" || how != "first" || from != "" {
		t.Fatalf("first read: %q %q %q", doc, from, how)
	}
	if len(links) < 2 {
		t.Fatalf("links: %v", links)
	}
	// link: relative to LLM.md's folder, path written differently
	_, from, how, _ = trackDoc(id, "read_file", "noteserver", rf("~/workspace/modulo_x/claude.md"), "Detalhes em hub.md", true)
	if from != "~/workspace/LLM.md" || how != "link" {
		t.Fatalf("link: %q %q", from, how)
	}
	// chain continues from claude.md (newest doc that mentions it)
	_, from, how, _ = trackDoc(id, "exec", "noteserver", json.RawMessage(`{"command":"cat ~/workspace/modulo_x/hub.md"}`), "x", true)
	if from != "~/workspace/modulo_x/claude.md" || how != "link" {
		t.Fatalf("chain: %q %q", from, how)
	}
	// dir: LLM.md points to the folder
	_, from, how, _ = trackDoc(id, "read_file", "noteserver", rf("~/workspace/_docs/homelab/matter-hub.md"), "", true)
	if from != "~/workspace/LLM.md" || how != "dir" {
		t.Fatalf("dir: %q %q", from, how)
	}
	// nobody mentions it
	_, from, how, _ = trackDoc(id, "read_file", "noteserver", rf("~/workspace/outro/solto.md"), "", true)
	if from != "" || how != "none" {
		t.Fatalf("none: %q %q", from, how)
	}
}

func TestDocIdentityHostAndNonMd(t *testing.T) {
	resetDocs()
	trackDoc("conv:a", "read_file", "noteserver", rf("~/w/LLM.md"), "ver [x](x.md)", true)
	if _, from, how, _ := trackDoc("conv:b", "read_file", "noteserver", rf("~/w/x.md"), "", true); from != "" || how != "first" {
		t.Fatalf("other conversation must not link: %q %q", from, how)
	}
	if _, from, _, _ := trackDoc("conv:a", "read_file", "local", rf("~/w/x.md"), "", true); from != "" {
		t.Fatalf("other host must not link: %q", from)
	}
	if doc, _, _, _ := trackDoc("conv:a", "read_file", "local", rf("C:\\a\\b.go"), "", true); doc != "" {
		t.Fatal("non-md must not be tracked")
	}
	if doc, _, _, _ := trackDoc("conv:a", "exec", "", json.RawMessage(`{"command":"cat a.md | head"}`), "", true); doc != "" {
		t.Fatal("pipelines are not reads of one doc")
	}
	if doc, _, _, _ := trackDoc("conv:a", "read_file", "", rf("C:\\R\\README.md"), "", false); doc != "" {
		t.Fatal("failed reads are not tracked")
	}
}

func TestDocWindowsPathsAndNumbered(t *testing.T) {
	resetDocs()
	id := "chan:x#1"
	hub := "  1\t# Hub\n  2\tVer W:\\ReposHomeLab\\AgenteHA\\_docs\\audio.md\n"
	trackDoc(id, "read_file", "", rf(`W:\ReposHomeLab\AgenteHA\_docs\_hub.md`), hub, true)
	_, from, how, _ := trackDoc(id, "read_file", "", rf(`w:/reposhomelab/agenteha/_docs/AUDIO.md`), "", true)
	if from != "W:/ReposHomeLab/AgenteHA/_docs/_hub.md" || how != "link" {
		t.Fatalf("windows link: %q %q", from, how)
	}
}
