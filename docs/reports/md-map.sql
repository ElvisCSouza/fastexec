-- Mapa de leitura de .md (FastExec). Uso:
--   sqlite3 -readonly -header -column W:\Data\telemetry\fastexec.db < docs\reports\md-map.sql
-- Janela: edite o 'days' abaixo. Identidade = conversa quando o cliente manda (Antigravity),
-- senão o canal MCP (Claude Desktop mistura conversas do mesmo canal).
CREATE TEMP TABLE p AS SELECT '-14 days' AS days;
CREATE TEMP VIEW md AS
SELECT a.id, a.timestamp, a.tool, a.host, a.doc, a.doc_from, a.doc_how, a.est_tokens_out tok,
       CASE WHEN a.tool='exec' OR a.command_summary LIKE '%[whole]' THEN 'inteiro' ELSE 'trecho' END sel,
       COALESCE(NULLIF(a.conversation,''), a.client) ident
FROM audit a, p WHERE a.doc <> '' AND a.timestamp >= datetime('now', p.days);

.print === 1. Leituras por .md
SELECT doc, COUNT(*) leituras, SUM(sel='inteiro') inteiro, SUM(sel='trecho') trecho, SUM(tok) tokens,
       COUNT(DISTINCT ident) identidades FROM md GROUP BY doc ORDER BY leituras DESC LIMIT 40;

.print === 2. Caminhos: de onde o agente veio (referencia confirmada no texto lido)
SELECT doc_from AS de, doc AS para, doc_how AS como, COUNT(*) vezes FROM md
WHERE doc_from <> '' GROUP BY 1,2,3 ORDER BY vezes DESC LIMIT 40;

.print === 3. Portas de entrada e chegadas sem referencia (first = 1o doc da identidade; none = nenhum doc lido antes cita)
SELECT doc_how, doc, COUNT(*) n FROM md WHERE doc_how IN ('first','none') GROUP BY 1,2 ORDER BY 1, n DESC LIMIT 40;

.print === 4. Releitura do mesmo .md pela mesma identidade em ate 2 h (texto nao retido, longo ou confuso)
WITH r AS (SELECT doc, ident, timestamp, LAG(timestamp) OVER (PARTITION BY ident, doc ORDER BY id) prev FROM md)
SELECT doc, COUNT(*) releituras FROM r WHERE prev IS NOT NULL AND (julianday(timestamp)-julianday(prev))*24 <= 2
GROUP BY doc ORDER BY releituras DESC LIMIT 20;

.print === 5. Leitura seguida de busca nas 3 chamadas seguintes da mesma identidade (o .md nao respondeu?)
WITH nx AS (
  SELECT m.doc, m.id, (SELECT COUNT(*) FROM (SELECT b.tool, b.command_summary FROM audit b
      WHERE COALESCE(NULLIF(b.conversation,''), b.client) = m.ident AND b.id > m.id ORDER BY b.id LIMIT 3)
    WHERE (tool='exec' AND (command_summary LIKE 'grep%' OR command_summary LIKE 'rg %' OR command_summary LIKE 'findstr%'
           OR command_summary LIKE '%Select-String%' OR command_summary LIKE 'find %'))
       OR (tool='read_file' AND command_summary LIKE '%pattern=%')
       OR tool IN ('code_outline','code_graph')) buscas
  FROM md m)
SELECT doc, COUNT(*) leituras, SUM(buscas>0) seguidas_de_busca, ROUND(100.0*SUM(buscas>0)/COUNT(*)) pct
FROM nx GROUP BY doc HAVING COUNT(*) >= 2 ORDER BY pct DESC, leituras DESC LIMIT 20;

.print === 6. Citados mas nunca lidos (candidatos a link morto ou doc inutil); confira se o arquivo existe
SELECT l.src AS citado_em, l.dst AS citado FROM doc_links l
WHERE NOT EXISTS (SELECT 1 FROM audit a WHERE a.doc <> '' AND lower(a.doc) = lower(l.dst))
ORDER BY l.src, l.dst LIMIT 60;
