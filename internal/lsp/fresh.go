package lsp

import (
	"context"
	"encoding/json"
	"time"
)

// DiagnosticsFresh devolve os diagnósticos de UMA versão conhecida do arquivo, ou diz que não sabe.
//
// É a regra "versão certa ou nada". Diagnostics (ops.go) aceita qualquer publish posterior ao pedido e, estourado o
// prazo, devolve "o que tem (normalmente nada)" como se fosse resposta: um servidor que leva 800 ms para reanalisar
// entrega o resultado da versão ANTERIOR, e o chamador lê isso como "o arquivo está limpo" (o mesmo bug dos
// diagnósticos velhos do Claude Code LSP, issue 17979). Aqui:
//
//   - pull (textDocument/diagnostic): a resposta é da versão que o servidor tem; verified = true.
//   - push com versão no publish: só vale publishDiagnostics com version >= a enviada no didOpen/didChange.
//   - push sem versão: só vale publish posterior ao envio (e o envio é sempre do disco, que é o que o FastExec
//     acabou de gravar). Sem nada enviado, o último publish já descreve esses mesmos bytes.
//   - prazo estourado: verified = false e NENHUM item. Diagnóstico que não passa nessa checagem é descartado, nunca
//     mostrado.
func (c *Client) DiagnosticsFresh(ctx context.Context, path string, settle time.Duration) ([]Diagnostic, bool, error) {
	abs, err := absOf(path)
	if err != nil {
		return nil, false, err
	}
	uri := PathToURI(abs)

	if c.capabilities().Diagnostics {
		raw, err := c.requestWithDoc(ctx, path, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
		if err == nil {
			var report struct {
				Items []Diagnostic `json:"items"`
			}
			if json.Unmarshal(raw, &report) == nil {
				c.mu.Lock()
				c.pulled[uri] = report.Items
				c.mu.Unlock()
				return report.Items, true, nil
			}
		}
		// Cai para o push em vez de falhar, como o Diagnostics.
	}

	key := uriKey(uri)
	c.mu.Lock()
	before := c.diagSeq[key]
	c.mu.Unlock()

	sent, err := c.ensureOpenSent(ctx, path)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	want := 0
	if d := c.docs[key]; d != nil {
		want = d.version
	}
	c.mu.Unlock()

	if settle <= 0 {
		settle = 3 * time.Second
	}
	deadline := time.Now().Add(settle)
	for {
		c.mu.Lock()
		seq, ver := c.diagSeq[key], c.diagVer[key]
		items := append([]Diagnostic(nil), c.diags[key]...)
		c.mu.Unlock()
		if publishIsFresh(seq, before, ver, want, sent) {
			return items, true, nil
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// DiagSeq é o contador de publishes que o servidor já fez para o arquivo. Guardado ANTES de uma edição, é o que
// permite perguntar depois "houve publish novo?" para um arquivo que a edição não tocou (um chamador, D8).
func (c *Client) DiagSeq(path string) uint64 {
	abs, err := absOf(path)
	if err != nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diagSeq[uriKey(PathToURI(abs))]
}

// DiagnosticsSince devolve os diagnósticos de um arquivo que NÃO foi editado mas pode ter sido afetado por uma edição
// em outro (o chamador de uma assinatura que mudou). A versão do documento não mudou, então a versão do publish não
// prova nada: o que prova é um publish NOVO (seq > before), feito depois que o servidor viu a edição. Pull responde
// direto. Sem publish novo no prazo: verified = false e nenhum item.
func (c *Client) DiagnosticsSince(ctx context.Context, path string, before uint64, settle time.Duration) ([]Diagnostic, bool, error) {
	abs, err := absOf(path)
	if err != nil {
		return nil, false, err
	}
	uri := PathToURI(abs)
	if c.capabilities().Diagnostics {
		raw, err := c.requestWithDoc(ctx, path, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
		if err == nil {
			var report struct {
				Items []Diagnostic `json:"items"`
			}
			if json.Unmarshal(raw, &report) == nil {
				c.mu.Lock()
				c.pulled[uri] = report.Items
				c.mu.Unlock()
				return report.Items, true, nil
			}
		}
	}
	key := uriKey(uri)
	if err := c.ensureOpen(ctx, path); err != nil {
		return nil, false, err
	}
	if settle <= 0 {
		settle = 3 * time.Second
	}
	deadline := time.Now().Add(settle)
	for {
		c.mu.Lock()
		seq := c.diagSeq[key]
		items := append([]Diagnostic(nil), c.diags[key]...)
		c.mu.Unlock()
		if seq > before {
			return items, true, nil
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// FileCreated avisa o servidor de que o arquivo passou a existir (workspace/didChangeWatchedFiles, Created). Sem isso
// um servidor de projeto (csharp-ls, tsserver) pode tratar o arquivo novo como solto, fora do projeto, e inventar
// "tipo não encontrado" em tudo que ele usa. Falha de envio é ignorada: o aviso só melhora a análise.
func (c *Client) FileCreated(ctx context.Context, path string) {
	abs, err := absOf(path)
	if err != nil {
		return
	}
	_ = c.notify(ctx, "workspace/didChangeWatchedFiles", map[string]any{
		"changes": []map[string]any{{"uri": PathToURI(abs), "type": 1}},
	})
}

// publishIsFresh é a decisão de aceitar ou descartar um publish. seq/before são o contador de publishes agora e antes
// do envio; ver é a versão do último publish (-1 quando o servidor não informa); want é a versão enviada; sent diz
// se este chamador enviou o documento.
func publishIsFresh(seq, before uint64, ver, want int, sent bool) bool {
	if seq == 0 {
		return false // o servidor nunca olhou este arquivo
	}
	if ver >= 0 {
		return ver >= want
	}
	if sent {
		return seq > before
	}
	return true
}
