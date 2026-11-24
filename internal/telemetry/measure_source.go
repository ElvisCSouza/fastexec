package telemetry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/corpus"
	"homelab/fastexec/internal/measure"
)

// sqlSource implementa measure.Source sobre o banco de telemetria.
type sqlSource struct{ db *sql.DB }

// windowWhere traduz a janela do domínio para o filtro da coluna timestamp.
func windowWhere(w measure.Window) string {
	switch {
	case w.Today:
		return "date(timestamp,'localtime') = date('now','localtime')"
	case w.Hours > 0:
		return fmt.Sprintf("timestamp >= datetime('now','-%d hours')", w.Hours)
	}
	return "1=1"
}

func (s sqlSource) Calls(w measure.Window) ([]measure.Call, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(NULLIF(client,''), session_id, ''), tool, COALESCE(command_summary,''),
		args_hash, marks, truncated, COALESCE(success,1), COALESCE(conversation,''), COALESCE(est_tokens_out,0),
		COALESCE(duration_ms,0), cmd_ms, CAST(strftime('%s', timestamp) AS INTEGER)
		FROM audit WHERE ` + windowWhere(w) + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []measure.Call
	for rows.Next() {
		var c measure.Call
		var trunc, ok int
		var epoch int64
		if err := rows.Scan(&c.ID, &c.Channel, &c.Tool, &c.Summary, &c.ArgsHash, &c.Marks, &trunc, &ok, &c.Conversation,
			&c.TokensOut, &c.DurMs, &c.CmdMs, &epoch); err != nil {
			return nil, err
		}
		// linhas antigas podem ter texto não higienizado: mascara antes do domínio ver
		c.Channel = corpus.Sanitize(c.Channel)
		c.Tool = corpus.Sanitize(c.Tool)
		c.Summary = corpus.Sanitize(c.Summary)
		c.Marks = corpus.Sanitize(c.Marks)
		c.Conversation = corpus.Sanitize(c.Conversation)
		c.Truncated, c.OK, c.HasConversation = trunc != 0, ok != 0, c.Conversation != ""
		if epoch > 0 {
			c.At = time.Unix(epoch, 0)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s sqlSource) Savings(w measure.Window) ([]measure.SavingRow, error) {
	aw, sw := windowWhere(w), windowWhere(w)
	_, sw = savingsSince(s.db, aw, sw) // antes do marco savings_since não há tamanho original
	rows, err := s.db.Query(`SELECT cat, bytes, trips FROM saving WHERE ` + sw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []measure.SavingRow
	for rows.Next() {
		var r measure.SavingRow
		if err := rows.Scan(&r.Cat, &r.Bytes, &r.Trips); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqlSource) MarkTemplates() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT mark, template FROM mark_catalog`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, tpl string
		if err := rows.Scan(&id, &tpl); err != nil {
			return nil, err
		}
		// só o texto do template é mascarado; a chave do mark fica como está
		out[id] = corpus.Sanitize(tpl)
	}
	return out, rows.Err()
}

// measureWindow converte o filtro do `stats` na janela do domínio.
func measureWindow(f StatsFilter) measure.Window {
	return measure.Window{Today: f.Today, Hours: f.PeriodH}
}

// RenderMeasure monta o relatório de medição (fastexec stats --measure). Leitura nunca cria banco.
func RenderMeasure(filter StatsFilter) (string, error) {
	path := filepath.Join(dataDirPath(), "fastexec.db")
	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("No telemetry database at %s: nothing recorded yet (or telemetry is off).", path), nil
	}
	db, err := GetDB()
	if err != nil {
		return "", err
	}
	r, err := measure.BuildWithAB(sqlSource{db}, measureWindow(filter), measure.DefaultPolicy(), measure.DefaultDetectors(), config.Get().AB)
	if err != nil {
		return "", err
	}
	out := r.Render()
	// Desfecho explícito da mesma janela (só aparece com linhas outcome_schema=1). Não altera o Source.
	if oc, oerr := queryOutcomes(db, "WHERE "+windowWhere(measureWindow(filter))); oerr == nil {
		out += renderOutcomes(oc)
	}
	return out, nil
}
