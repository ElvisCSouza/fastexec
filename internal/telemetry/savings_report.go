package telemetry

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
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
	Series          []int64       `json:"series"` // tokens economizados por intervalo (mais antigo primeiro)
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
	var since string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='savings_since'`).Scan(&since); err == nil && since != "" {
		aw += " AND timestamp >= '" + since + "'"
		sw += " AND timestamp >= '" + since + "'"
	}
	sw += " AND cat <> 'edit'" // economia de edicao nao e mais contada: o edit nativo tambem troca so o trecho, o ganho nao e real
	r := SavingsReport{Period: period, SeriesStart: start, SeriesEnd: end, Generated: time.Now().Format("15:04:05"), Cats: []CatTotal{}, Recent: []SavingEvent{}}

	var sentTok sql.NullInt64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN success=0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(duration_ms),0), SUM(est_tokens_out) FROM audit WHERE `+aw).Scan(&r.Calls, &r.FailedCalls, &r.ActiveMs, &sentTok); err != nil {
		return r, err
	}
	r.SentTokens = sentTok.Int64

	rows, err := db.Query(`SELECT cat, COALESCE(SUM(bytes),0), COUNT(*), COALESCE(MIN(exact),1), COALESCE(SUM(trips),0), COALESCE(SUM(ms),0)
		FROM saving WHERE ` + sw + ` GROUP BY cat ORDER BY SUM(bytes) DESC`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var c CatTotal
		var exact, trips, ms int64
		if err := rows.Scan(&c.Cat, &c.Bytes, &c.Events, &exact, &trips, &ms); err != nil {
			rows.Close()
			return r, err
		}
		c.Exact = exact == 1
		c.Tokens = tokens(c.Bytes)
		r.SavedTokens += c.Tokens
		r.TripsAvoided += trips
		r.SavedMs += ms
		if c.Cat == CatRetry || c.Cat == CatTimeout {
			r.FailuresAvoided += c.Events
		}
		r.Cats = append(r.Cats, c)
	}
	rows.Close()

	r.WithoutTokens = r.SentTokens + r.SavedTokens
	if r.WithoutTokens > 0 {
		r.Percent = float64(r.SavedTokens) / float64(r.WithoutTokens) * 100
	}
	usd := func(t int64) float64 { return float64(t) * DefaultUSDPerMTok / 1e6 }
	r.CostSavedUSD, r.CostWithoutUSD, r.CostWithUSD = usd(r.SavedTokens), usd(r.WithoutTokens), usd(r.SentTokens)

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
				e.Target = e.FullTarget
				if len(e.Target) > 90 {
					e.Target = e.Target[:90] + "…"
				}
				if len(e.FullTarget) > 4000 {
					e.FullTarget = e.FullTarget[:4000] + "…"
				}
				if len(e.Err) > 600 {
					e.Err = e.Err[:600] + "…"
				}
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
