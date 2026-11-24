# edit_file — spec

> Para o chat conversar e escrever diretiva sem abrir o código. Linguagem natural, sem colar código.
> Quem mantém: o desenvolvedor que muda o comportamento, o contrato ou um invariante deste módulo.

## Responsabilidade
Troca texto exato em um arquivo existente (`target` → `replacement`, ou lote em `edits`). Não cria arquivo (isso é
`write_file`), não faz diff livre e não adivinha o alvo: quando o alvo é ambíguo ou o arquivo mudou, recusa.

## Fluxo
1. Lê o arquivo (`readForEdit`). Arquivo ausente vira `file_not_found`.
2. Se `expected_sha256` veio, confere o hash do texto decodificado (`checkExpectedSHA`); divergente vira `stale_precondition`.
3. Normaliza LF/CRLF e conta as ocorrências do alvo.
4. 0 ocorrências: `target_not_found` (com o trecho mais próximo, como antes).
5. Mais de 1 ocorrência: `ambiguous_target` com até 5 linhas de início.
6. 1 ocorrência em todas as trocas do lote: monta o conteúdo novo e grava atomicamente (`handleWriteFile`).

## Contratos
- Recusas: texto começa com `[edit_file <classe>]` e diz `nothing was written`. Classes: `file_not_found`,
  `target_not_found`, `ambiguous_target`, `stale_precondition`.
- `stale_precondition` mostra só o hash atual, nunca o conteúdo.
- `ambiguous_target` em lote informa `edits[i].target`.
- Argumento `expected_sha256` (hex, sha256 do texto decodificado). Fora do schema (ver Decisões).
- A telemetria guarda o erro pelo enum fechado (`error_type`); a classe fica só no texto da recusa.

## Invariantes
- Tudo ou nada: qualquer recusa, em qualquer item do lote, não grava nada.
- Alvo vazio é recusado (não insere texto em toda parte).
- Ambíguo nunca troca a primeira ocorrência por conta própria.
- Edição com hash velho nunca é aplicada.
- Escrita atômica e guardas existentes não mudam.

## Decisões
- Ambiguidade virou recusa (antes: trocava a primeira e avisava `only the first was changed`). Motivo: troca errada
  silenciosa é pior que uma volta extra do modelo. Data: 2026-11-24.
- `expected_sha256` fica fora do schema de `tools/list`. Motivo: o orçamento de tamanho de `tools/list` (teste
  `tools_list_size_test.go`) já estava no limite e não há medição de necessidade. O argumento continua funcionando
  quando enviado.
- Erros de classe não entraram no enum da telemetria: o enum é fechado e o texto basta para o log.

## Limites e dívidas conhecidas
- Sem hash na resposta de sucesso: o cliente só obtém o hash por uma recusa `stale_precondition` ou calculando-o. Um
  campo de revisão no sucesso exigiria mudar a resposta, fora do escopo atual.
- `file_not_found` depende de a mensagem do erro de leitura conter a causa; o texto de erro de host remoto pode variar.
- Os limites de contagem (5 linhas citadas) são fixos.

## Mapa
- `internal/tools/files.go` — `handleEditFile`, pipeline do edit e o caminho do lote.
- `internal/tools/edit_classes.go` — classes de recusa, `checkExpectedSHA`, `ambiguousRefusal`.
- `internal/tools/edit_classes_test.go` — uma recusa por classe, lote ambíguo, hash velho sem vazar conteúdo.
- `internal/tools/near_miss.go` — trecho mais próximo do alvo (inalterado).
- `internal/mcp/tool_schemas.go` — schema de `edit_file` (sem `expected_sha256`).
- `docs/tools.md` — seção `edit_file`, "Refusals: error classes".
