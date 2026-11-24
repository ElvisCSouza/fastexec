package telemetry

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"homelab/fastexec/internal/corpus"
)

// Relatório de economia para a janela de métricas. Tudo vem do banco local; nada sai da máquina.
//
// O que é medido: tamanhos (bytes) antes e depois de cada etapa do FastExec. O que é estimativa:
// tokens = bytes ÷ 3,5 (o mesmo cálculo do `fastexec stats`), custo = tokens × preço de entrada e
// tempo/idas ao modelo evitadas (cada retentativa evitada vale uma ida ao modelo).

const (
	BytesPerToken     = 3.5
	DefaultUSDPerMTok = 3.0
)

type CatTotal struct {
	Cat    string `json:"cat"`
	Bytes  int64  `json:"bytes"`
	Tokens int64  `json:"tokens"`
	Events int64  `json:"events"`
	Exact  bool   `json:"exact"` // false: parte do valor é estimativa (proxy documentado)
}

type SavingEvent struct {
	ID         int64       `json:"id"`
	Time       string      `json:"time"`
	Date       string      `json:"date"`
	Tool       string      `json:"tool"`
	Target     string      `json:"target"`
	FullTarget string      `json:"full_target"`
	SentBytes  int64       `json:"sent_bytes"`
	SavedBytes int64       `json:"saved_bytes"`
	Cats       []string    `json:"cats"`
	Trips      int64       `json:"trips"`
	Parts      []EventPart `json:"parts"`
	Host       string      `json:"host"`
	DurationMs int64       `json:"duration_ms"`
	OK         bool        `json:"ok"`
	ExitCode   int64       `json:"exit_code"`
	Err        string      `json:"err"`
	BytesIn    int64       `json:"bytes_in"`
	TokensOut  int64       `json:"tokens_out"`
	Client     string      `json:"client"`
	Marks      string      `json:"marks"`
	Truncated  bool        `json:"truncated"`
}

// EventPart é a parte da economia de uma chamada que veio de uma categoria.
type EventPart struct {
	Cat    string `json:"cat"`
	Bytes  int64  `json:"bytes"`
	Trips  int64  `json:"trips"`
	Ms     int64  `json:"ms"`
	Exact  bool   `json:"exact"`
	Before int64  `json:"before"`
	After  int64  `json:"after"`
	Note   string `json:"note"`
}

type SavingsReport struct {
	Period          string        `json:"period"`
	Calls           int64         `json:"calls"`
	FailedCalls     int64         `json:"failed_calls"`
	ActiveMs        int64         `json:"active_ms"`
	SentTokens      int64         `json:"sent_tokens"`
	SavedTokens     int64         `json:"saved_tokens"`
	WithoutTokens   int64         `json:"without_tokens"`
	Percent         float64       `json:"percent"`
	CostSavedUSD    float64       `json:"cost_saved_usd"`
	CostWithoutUSD  float64       `json:"cost_without_usd"`
	CostWithUSD     float64       `json:"cost_with_usd"`
	TripsAvoided    int64         `json:"trips_avoided"`
	SavedMs         int64         `json:"saved_ms"`
	FailuresAvoided int64         `json:"failures_avoided"`
	Cats            []CatTotal    `json:"cats"`
	Series          []int64       `json:"series"`      // tokens economizados por intervalo (mais antigo primeiro)
	SentSeries      []int64       `json:"sent_series"` // tokens lidos (audit) por intervalo, mesmo bucket de Series; a curva "With" anda a cada chamada real
	SeriesStart     string        `json:"series_start"`
	SeriesEnd       string        `json:"series_end"`
	Recent          []SavingEvent `json:"recent"`
	LastID          int64         `json:"last_id"`
	Since           string        `json:"since"` // quando a medição começou (hora local); antes disso não há tamanho original
	Generated       string        `json:"generated"`
}

func periodClauses(period string) (auditWhere, savingWhere, bucket string, n int, start, end string) {
	switch period {
	case "week":
		w := "timestamp >= datetime('now','-7 days')"
		return w, w, "CAST(julianday(date('now','localtime')) - julianday(date(timestamp,'localtime')) AS INTEGER)", 7, "6 days ago", "today"
	case "all":
		return "1=1", "1=1", "MIN(11, CAST((julianday(date('now','localtime')) - julianday(date(timestamp,'localtime'))) / 7 AS INTEGER))", 12, "12 weeks ago", "this week"
	default:
		w := "date(timestamp,'localtime') = date('now','localtime')"
		return w, w, "23 - CAST(strftime('%H', timestamp,'localtime') AS INTEGER)", 24, "00:00", "now"
	}
}

func tokens(bytes int64) int64 { return int64(float64(bytes) / BytesPerToken) }

// SavingsTotals agrupa os números contábeis da economia de uma janela: chamadas, tokens lidos e economizados,
// percentual, custo e idas evitadas. É a fonte única do painel e do `fastexec stats` — os dois consomem a mesma
// soma para que os números nunca diverjam (invariante de docs/savings.md).
type SavingsTotals struct {
	Calls           int64
	FailedCalls     int64
	ActiveMs        int64
	SentTokens      int64
	SavedTokens     int64
	WithoutTokens   int64
	Percent         float64
	CostSavedUSD    float64
	CostWithoutUSD  float64
	CostWithUSD     float64
	TripsAvoided    int64
	SavedMs         int64
	FailuresAvoided int64
	Cats            []CatTotal
}

// savingsSince aplica às janelas brutas o corte savings_since (antes dele não há tamanho original) e a exclusão
// de cat='edit' (a economia de edição não é mais contada: o edit nativo também troca só o trecho).
func savingsSince(db *sql.DB, auditWhere, savingWhere string) (string, string) {
	var since string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='savings_since'`).Scan(&since); err == nil && since != "" {
		auditWhere += " AND timestamp >= '" + since + "'"
		savingWhere += " AND timestamp >= '" + since + "'"
	}
	return auditWhere, savingWhere + " AND cat <> 'edit'"
}

// savingsTotals soma os totais da janela: uma linha na audit (chamadas/tokens lidos) e um GROUP BY na saving
// (economia por categoria). Em erro devolve o que deu para somar junto com o erro.
func savingsTotals(db *sql.DB, auditWhere, savingWhere string) (SavingsTotals, error) {
	st := SavingsTotals{Cats: []CatTotal{}}
	var sentTok sql.NullInt64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(duration_ms),0), SUM(est_tokens_out) FROM audit WHERE `+auditWhere).Scan(&st.Calls, &st.FailedCalls, &st.ActiveMs, &sentTok); err != nil {
		return st, err
	}
	st.SentTokens = sentTok.Int64

	rows, err := db.Query(`SELECT cat, COALESCE(SUM(bytes),0), COUNT(*), COALESCE(MIN(exact),1), COALESCE(SUM(trips),0), COALESCE(SUM(ms),0)
		FROM saving WHERE ` + savingWhere + ` GROUP BY cat ORDER BY SUM(bytes) DESC`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CatTotal
		var exact, trips, ms int64
		if err := rows.Scan(&c.Cat, &c.Bytes, &c.Events, &exact, &trips, &ms); err != nil {
			return st, err
		}
		c.Exact = exact == 1
		c.Tokens = tokens(c.Bytes)
		st.SavedTokens += c.Tokens
		st.TripsAvoided += trips
		st.SavedMs += ms
		if c.Cat == CatRetry || c.Cat == CatTimeout {
			st.FailuresAvoided += c.Events
		}
		st.Cats = append(st.Cats, c)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	st.WithoutTokens = st.SentTokens + st.SavedTokens
	if st.WithoutTokens > 0 {
		st.Percent = float64(st.SavedTokens) / float64(st.WithoutTokens) * 100
	}
	usd := func(t int64) float64 { return float64(t) * DefaultUSDPerMTok / 1e6 }
	st.CostSavedUSD, st.CostWithoutUSD, st.CostWithUSD = usd(st.SavedTokens), usd(st.WithoutTokens), usd(st.SentTokens)
	return st, nil
}

// savingsTotalsFor devolve os totais do painel ("today", "week" ou "all") — a mesma soma de buildSavingsReport,
// sem séries nem atividade recente.
func savingsTotalsFor(db *sql.DB, period string) (SavingsTotals, error) {
	if period != "week" && period != "all" {
		period = "today"
	}
	aw, sw, _, _, _, _ := periodClauses(period)
	aw, sw = savingsSince(db, aw, sw)
	return savingsTotals(db, aw, sw)
}

// savingsTotalsForStats devolve os totais para a janela pedida no `fastexec stats` (today, N horas/dias ou tudo).
// 168h usa as cláusulas de "week" do painel, e "all"/"today" as cláusulas próprias — nesses períodos os números
// ficam idênticos ao relatório do painel; as demais janelas usam o mesmo corte de timestamp da visão geral.
func savingsTotalsForStats(db *sql.DB, filter StatsFilter) (SavingsTotals, error) {
	var aw, sw string
	switch {
	case filter.Today:
		aw, sw, _, _, _, _ = periodClauses("today")
	case filter.PeriodH == 168:
		aw, sw, _, _, _, _ = periodClauses("week")
	case filter.PeriodH > 0:
		w := fmt.Sprintf("timestamp >= datetime('now', '-%d hours')", filter.PeriodH)
		aw, sw = w, w
	default:
		aw, sw = "1=1", "1=1"
	}
	aw, sw = savingsSince(db, aw, sw)
	return savingsTotals(db, aw, sw)
}

// BuildSavingsReport lê o banco e monta o relatório do período ("today", "week" ou "all").
func BuildSavingsReport(period string) (SavingsReport, error) {
	db, err := GetDB()
	if err != nil {
		return SavingsReport{}, err
	}
	return buildSavingsReport(db, period)
}

func buildSavingsReport(db *sql.DB, period string) (SavingsReport, error) {
	if period != "week" && period != "all" {
		period = "today"
	}
	aw, sw, bucket, n, start, end := periodClauses(period)
	aw, sw = savingsSince(db, aw, sw)
	r := SavingsReport{Period: period, SeriesStart: start, SeriesEnd: end, Generated: time.Now().Format("15:04:05"), Cats: []CatTotal{}, Recent: []SavingEvent{}}

	// totais pela mesma soma do CLI (savingsTotals); em erro, o que já foi somado fica no relatório.
	st, terr := savingsTotals(db, aw, sw)
	r.Calls, r.FailedCalls, r.ActiveMs, r.SentTokens = st.Calls, st.FailedCalls, st.ActiveMs, st.SentTokens
	r.SavedTokens, r.TripsAvoided, r.SavedMs, r.FailuresAvoided = st.SavedTokens, st.TripsAvoided, st.SavedMs, st.FailuresAvoided
	r.WithoutTokens, r.Percent = st.WithoutTokens, st.Percent
	r.CostSavedUSD, r.CostWithoutUSD, r.CostWithUSD = st.CostSavedUSD, st.CostWithoutUSD, st.CostWithUSD
	r.Cats = st.Cats
	if terr != nil {
		return r, terr
	}

	// série: tokens economizados por intervalo
	r.Series = make([]int64, n)
	srows, err := db.Query(`SELECT ` + bucket + ` AS b, COALESCE(SUM(bytes),0) FROM saving WHERE ` + sw + ` GROUP BY b`)
	if err == nil {
		for srows.Next() {
			var b, by int64
			if srows.Scan(&b, &by) == nil && b >= 0 && int(b) < n {
				r.Series[n-1-int(b)] += tokens(by)
			}
		}
		srows.Close()
	}

	// série irmã: tokens que o modelo leu (audit) por intervalo, no mesmo bucket.
	// Onde não houve evento de economia a curva de economia fica plana; esta série faz a
	// linha "With" andar a cada chamada real.
	r.SentSeries = make([]int64, n)
	arows, err := db.Query(`SELECT ` + bucket + ` AS b, COALESCE(SUM(est_tokens_out),0) FROM audit WHERE ` + aw + ` GROUP BY b`)
	if err == nil {
		for arows.Next() {
			var b, tk int64
			if arows.Scan(&b, &tk) == nil && b >= 0 && int(b) < n {
				r.SentSeries[n-1-int(b)] += tk
			}
		}
		arows.Close()
	}

	// atividade recente: uma linha por chamada
	erows, err := db.Query(`SELECT MAX(s.id), MAX(s.audit_id), strftime('%H:%M:%S', MIN(s.timestamp),'localtime'),
		strftime('%Y-%m-%d', MIN(s.timestamp),'localtime'), COALESCE(a.tool,'tools/list'),
		COALESCE(a.command_summary,''), COALESCE(a.bytes_out,0), SUM(s.bytes), group_concat(s.cat), SUM(s.trips),
		COALESCE(a.host,''), COALESCE(a.duration_ms,0), COALESCE(a.success,1), COALESCE(a.exit_code,0),
		COALESCE(a.error_message,''), COALESCE(a.bytes_in,0), COALESCE(a.est_tokens_out,0), COALESCE(a.client,''),
		COALESCE(a.marks,''), COALESCE(a.truncated,0)
		FROM saving s LEFT JOIN audit a ON a.id = s.audit_id
		GROUP BY CASE WHEN s.audit_id = 0 THEN -s.id ELSE s.audit_id END
		ORDER BY MAX(s.id) DESC LIMIT 40`)
	if err == nil {
		var keys []int64
		for erows.Next() {
			var e SavingEvent
			var cats string
			var aid, okInt, trunc int64
			if erows.Scan(&e.ID, &aid, &e.Time, &e.Date, &e.Tool, &e.FullTarget, &e.SentBytes, &e.SavedBytes, &cats, &e.Trips,
				&e.Host, &e.DurationMs, &okInt, &e.ExitCode, &e.Err, &e.BytesIn, &e.TokensOut, &e.Client, &e.Marks, &trunc) == nil {
				e.Cats = strings.Split(cats, ",")
				e.Parts = []EventPart{}
				e.OK, e.Truncated = okInt != 0, trunc != 0
				e.FullTarget = corpus.Sanitize(e.FullTarget)
				e.Err = corpus.Sanitize(e.Err)
				e.Host = corpus.Sanitize(e.Host)
				e.Tool = corpus.Sanitize(e.Tool)
				e.Client = corpus.Sanitize(e.Client)
				e.Marks = corpus.Sanitize(e.Marks)
				e.Target = cutRunes(e.FullTarget, 90, "…")
				e.FullTarget = cutRunes(e.FullTarget, 4000, "…")
				e.Err = cutRunes(e.Err, 600, "…")
				key := aid
				if aid == 0 {
					key = -e.ID
				}
				keys = append(keys, key)
				r.Recent = append(r.Recent, e)
			}
		}
		erows.Close()
		// partes por categoria de cada chamada (com antes/depois e a nota)
		if len(keys) > 0 {
			var args []any
			var aidQ, idQ []string
			for _, k := range keys {
				if k > 0 {
					aidQ = append(aidQ, "?")
				} else {
					idQ = append(idQ, "?")
				}
			}
			for _, k := range keys {
				if k > 0 {
					args = append(args, k)
				}
			}
			for _, k := range keys {
				if k < 0 {
					args = append(args, -k)
				}
			}
			cond := "0"
			if len(aidQ) > 0 {
				cond = "audit_id IN (" + strings.Join(aidQ, ",") + ")"
			}
			if len(idQ) > 0 {
				cond += " OR (audit_id = 0 AND id IN (" + strings.Join(idQ, ",") + "))"
			}
			by := map[int64][]EventPart{}
			if prow, perr := db.Query(`SELECT audit_id, id, cat, bytes, trips, ms, exact, size_before, size_after, note FROM saving WHERE `+cond+` ORDER BY id`, args...); perr == nil {
				for prow.Next() {
					var aid, id, ex int64
					var p EventPart
					if prow.Scan(&aid, &id, &p.Cat, &p.Bytes, &p.Trips, &p.Ms, &ex, &p.Before, &p.After, &p.Note) == nil {
						p.Exact = ex == 1
						p.Note = corpus.Sanitize(p.Note)
						k := aid
						if aid == 0 {
							k = -id
						}
						by[k] = append(by[k], p)
					}
				}
				prow.Close()
			}
			for i := range r.Recent {
				if ps, ok := by[keys[i]]; ok {
					r.Recent[i].Parts = ps
				}
			}
		}
	}
	_ = db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM saving`).Scan(&r.LastID)
	_ = db.QueryRow(`SELECT strftime('%Y-%m-%d %H:%M', value, 'localtime') FROM meta WHERE key='savings_since'`).Scan(&r.Since)
	return r, nil
}

// LastSavingID devolve o maior id da tabela saving (a janela usa para saber se há novidade).
func LastSavingID() int64 {
	db, err := GetDB()
	if err != nil {
		return 0
	}
	var id int64
	_ = db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM saving`).Scan(&id)
	return id
}

func (r SavingsReport) String() string {
	return fmt.Sprintf("%s: %d calls, %d tokens saved (%.0f%%), %d round trips avoided", r.Period, r.Calls, r.SavedTokens, r.Percent, r.TripsAvoided)
}
