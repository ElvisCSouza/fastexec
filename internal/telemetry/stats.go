package telemetry

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	_ "modernc.org/sqlite"
)

type StatsFilter struct {
	Today      bool
	PeriodH    int // janela em horas (0 = histórico total); ignorado quando Today
	OnlyErrors bool
	OnlyTools  bool
	ResetDB    bool
}

type GlobalSummary struct {
	TotalCalls     int
	SuccessCalls   int
	ErrorCalls     int
	SuccessRate    float64
	AvgDurationMs  float64
	P95DurationMs  int64
	TotalBytesIn   int64
	TotalBytesOut  int64
	TotalTokensIn  int64
	TotalTokensOut int64
}

type ToolBreakdown struct {
	Tool        string
	Calls       int
	Errors      int
	AvgDuration float64
	TokensIn    int64
	TokensOut   int64
}

type ErrorRecord struct {
	Timestamp      string
	Tool           string
	Host           string
	CommandSummary string
	ErrorMessage   string
}

func GetDB() (*sql.DB, error) {
	Init()
	if dbInstance == nil {
		return nil, fmt.Errorf("não foi possível inicializar banco de telemetria")
	}
	return dbInstance, nil
}

func ResetTelemetry() error {
	dbPath := getDBPath()
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	return nil
}

func RenderStats(filter StatsFilter) (string, error) {
	if filter.ResetDB {
		if err := ResetTelemetry(); err != nil {
			return "", fmt.Errorf("erro limpando telemetria: %w", err)
		}
		return "Telemetria reinicializada com sucesso.", nil
	}

	// leitura nunca cria banco: um arquivo vazio criado aqui esconderia o banco real (caso do Claude Desktop MSIX,
	// cujo AppData é redirecionado para dentro do pacote)
	path := filepath.Join(dataDirPath(), "fastexec.db")
	if _, err := os.Stat(path); err != nil {
		msg := fmt.Sprintf("No telemetry database at %s: nothing recorded on this machine yet, or the client keeps it "+
			"elsewhere (a packaged Claude Desktop writes under AppData\\Local\\Packages\\<app>\\LocalCache\\Local).", path)
		if r := failureReport(); r != "" {
			msg += "\n" + r
		}
		return msg, nil
	}

	db, err := GetDB()
	if err != nil {
		return "", err
	}
	out, err := renderStats(db, filter)
	if err != nil {
		return "", err
	}
	var total int64
	_ = db.QueryRow(`SELECT COUNT(*) FROM audit`).Scan(&total)
	out = fmt.Sprintf("Database: %s (%d records)\n", path, total) + out
	if r := failureReport(); r != "" {
		out += "\n\n" + r
	}
	return out, nil
}

// ParsePeriod converte "24h", "7d" etc. em horas. Retorna false para formatos não reconhecidos.
func ParsePeriod(arg string) (int, bool) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if len(arg) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(arg[:len(arg)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	switch arg[len(arg)-1] {
	case 'h':
		return n, true
	case 'd':
		return n * 24, true
	}
	return 0, false
}

// periodWhere monta o filtro temporal da consulta; "" quando não há janela.
func periodWhere(filter StatsFilter) string {
	switch {
	case filter.Today:
		return "WHERE date(timestamp, 'localtime') = date('now', 'localtime')"
	case filter.PeriodH > 0:
		return fmt.Sprintf("WHERE timestamp >= datetime('now', '-%d hours')", filter.PeriodH)
	}
	return ""
}

// periodDescription descreve a janela para cabeçalhos e mensagens.
func periodDescription(filter StatsFilter) (label string, short string) {
	switch {
	case filter.Today:
		return fmt.Sprintf("Hoje (%s)", time.Now().Format("02/01/2006")), "hoje"
	case filter.PeriodH > 0 && filter.PeriodH%24 == 0:
		d := filter.PeriodH / 24
		return fmt.Sprintf("Últimos %d dias", d), fmt.Sprintf("nos últimos %d dias", d)
	case filter.PeriodH == 1:
		return "Última 1 hora", "na última hora"
	case filter.PeriodH > 0:
		return fmt.Sprintf("Últimas %d horas", filter.PeriodH), fmt.Sprintf("nas últimas %d horas", filter.PeriodH)
	}
	return "Histórico Total", "no histórico geral"
}

func renderStats(db *sql.DB, filter StatsFilter) (string, error) {
	whereClause := periodWhere(filter)

	// 1. Resumo Global
	summary, durations, err := querySummary(db, whereClause)
	if err != nil {
		return "", fmt.Errorf("erro consultando resumo: %w", err)
	}

	if summary.TotalCalls == 0 {
		_, period := periodDescription(filter)
		return fmt.Sprintf("Nenhum registro de telemetria encontrado (%s).", period), nil
	}

	summary.P95DurationMs = calculateP95(durations)

	// 2. Breakdown por Ferramenta
	tools, err := queryTools(db, whereClause)
	if err != nil {
		return "", fmt.Errorf("erro consultando ferramentas: %w", err)
	}

	toolBytes, err := queryToolBytes(db, whereClause)
	if err != nil {
		return "", fmt.Errorf("erro consultando bytes por ferramenta: %w", err)
	}

	topReturns, err := queryTopReturns(db, whereClause, 5)
	if err != nil {
		return "", fmt.Errorf("erro consultando maiores retornos: %w", err)
	}

	// 3. Registros de Erro
	var errors []ErrorRecord
	if filter.OnlyErrors || summary.ErrorCalls > 0 {
		errors, err = queryErrors(db, whereClause, 15)
		if err != nil {
			return "", fmt.Errorf("erro consultando erros: %w", err)
		}
	}

	// Montagem do Relatório
	var sb strings.Builder
	periodLabel, _ := periodDescription(filter)

	sb.WriteString(fmt.Sprintf("================================================================================\n"))
	sb.WriteString(fmt.Sprintf("                    TELEMETRIA & AUDITORIA FASTEXEC (%s)\n", periodLabel))
	sb.WriteString(fmt.Sprintf("================================================================================\n\n"))

	if !filter.OnlyErrors && !filter.OnlyTools {
		sb.WriteString(fmt.Sprintf("Chamadas Totais:   %-8d | Taxa de Sucesso: %5.1f%% (%d ok, %d erro)\n",
			summary.TotalCalls, summary.SuccessRate, summary.SuccessCalls, summary.ErrorCalls))
		sb.WriteString(fmt.Sprintf("Latência Média:    %-8.1fms | Latência P95:    %dms\n",
			summary.AvgDurationMs, summary.P95DurationMs))
		sb.WriteString(fmt.Sprintf("Tráfego de Dados:  Entrada %s, Saída %s\n",
			formatBytes(summary.TotalBytesIn), formatBytes(summary.TotalBytesOut)))
		sb.WriteString(fmt.Sprintf("Tokens Estimados:  Entrada ~%d tok, Saída ~%d tok (Total: ~%d tok)\n\n",
			summary.TotalTokensIn, summary.TotalTokensOut, summary.TotalTokensIn+summary.TotalTokensOut))
	}

	if !filter.OnlyErrors {
		sb.WriteString("--------------------------------------------------------------------------------\n")
		sb.WriteString("CONSUMO & PERFORMANCE POR FERRAMENTA\n")
		sb.WriteString("--------------------------------------------------------------------------------\n")
		tw := tabwriter.NewWriter(&sb, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "FERRAMENTA\tCHAMADAS\tERROS\tLATÊNCIA MÉD\tTOKENS IN\tTOKENS OUT\n")
		for _, t := range tools {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%.1fms\t~%d\t~%d\n",
				t.Tool, t.Calls, t.Errors, t.AvgDuration, t.TokensIn, t.TokensOut)
		}
		tw.Flush()
		sb.WriteString("\n")

		sb.WriteString("--------------------------------------------------------------------------------\n")
		sb.WriteString("RETORNO POR FERRAMENTA (bytes devolvidos ao modelo)\n")
		sb.WriteString("--------------------------------------------------------------------------------\n")
		tw = tabwriter.NewWriter(&sb, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "FERRAMENTA\tP50\tP90\tP99\tMÁXIMO\tTRUNCADOS\tSEM RESULTADO\n")
		for _, t := range toolBytes {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\n",
				t.Tool, formatBytes(t.P50), formatBytes(t.P90), formatBytes(t.P99), formatBytes(t.Max), t.Truncated, t.NoResult)
		}
		tw.Flush()
		sb.WriteString("\n")

		sb.WriteString("--------------------------------------------------------------------------------\n")
		sb.WriteString("MAIORES RETORNOS DO PERÍODO\n")
		sb.WriteString("--------------------------------------------------------------------------------\n")
		tw = tabwriter.NewWriter(&sb, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "DATA/HORA\tTOOL\tBYTES\tCOMANDO / ALVO\n")
		for _, r := range topReturns {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Timestamp, r.Tool, formatBytes(r.Bytes), truncRunes(r.CommandSummary, 60))
		}
		tw.Flush()
		sb.WriteString("\n")
	}

	if len(errors) > 0 {
		sb.WriteString("--------------------------------------------------------------------------------\n")
		sb.WriteString(fmt.Sprintf("ÚLTIMOS ERROS REGISTRADOS (%d)\n", len(errors)))
		sb.WriteString("--------------------------------------------------------------------------------\n")
		tw := tabwriter.NewWriter(&sb, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "DATA/HORA\tTOOL\tHOST\tCOMANDO / ALVO\tERRO\n")
		for _, e := range errors {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				e.Timestamp, e.Tool, e.Host, truncate(e.CommandSummary, 35), truncate(e.ErrorMessage, 45))
		}
		tw.Flush()
		sb.WriteString("\n")
	} else if filter.OnlyErrors {
		sb.WriteString("Nenhum erro registrado no período selecionado.\n\n")
	}

	return sb.String(), nil
}

func querySummary(db *sql.DB, whereClause string) (GlobalSummary, []int64, error) {
	q := fmt.Sprintf(`
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(duration_ms), 0.0),
			COALESCE(SUM(bytes_in), 0),
			COALESCE(SUM(bytes_out), 0),
			COALESCE(SUM(est_tokens_in), 0),
			COALESCE(SUM(est_tokens_out), 0)
		FROM audit %s;
	`, whereClause)

	var s GlobalSummary
	err := db.QueryRow(q).Scan(
		&s.TotalCalls,
		&s.SuccessCalls,
		&s.ErrorCalls,
		&s.AvgDurationMs,
		&s.TotalBytesIn,
		&s.TotalBytesOut,
		&s.TotalTokensIn,
		&s.TotalTokensOut,
	)
	if err != nil {
		return s, nil, err
	}

	if s.TotalCalls > 0 {
		s.SuccessRate = (float64(s.SuccessCalls) / float64(s.TotalCalls)) * 100.0
	}

	// Busca durações para cálculo de P95
	dq := fmt.Sprintf("SELECT duration_ms FROM audit %s ORDER BY duration_ms ASC;", whereClause)
	rows, err := db.Query(dq)
	if err != nil {
		return s, nil, err
	}
	defer rows.Close()

	var durations []int64
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err == nil {
			durations = append(durations, d)
		}
	}

	return s, durations, nil
}

func queryTools(db *sql.DB, whereClause string) ([]ToolBreakdown, error) {
	q := fmt.Sprintf(`
		SELECT 
			tool,
			COUNT(*),
			COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(duration_ms), 0.0),
			COALESCE(SUM(est_tokens_in), 0),
			COALESCE(SUM(est_tokens_out), 0)
		FROM audit %s
		GROUP BY tool
		ORDER BY COUNT(*) DESC;
	`, whereClause)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ToolBreakdown
	for rows.Next() {
		var tb ToolBreakdown
		if err := rows.Scan(&tb.Tool, &tb.Calls, &tb.Errors, &tb.AvgDuration, &tb.TokensIn, &tb.TokensOut); err == nil {
			list = append(list, tb)
		}
	}
	return list, nil
}

func queryErrors(db *sql.DB, whereClause string, limit int) ([]ErrorRecord, error) {
	sep := "WHERE"
	if whereClause != "" {
		sep = whereClause + " AND"
	}
	q := fmt.Sprintf(`
		SELECT 
			strftime('%%d/%%m %%H:%%M:%%S', timestamp, 'localtime'),
			tool,
			COALESCE(host, '-'),
			COALESCE(command_summary, '-'),
			COALESCE(error_message, '-')
		FROM audit 
		%s success = 0
		ORDER BY id DESC
		LIMIT %d;
	`, sep, limit)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ErrorRecord
	for rows.Next() {
		var er ErrorRecord
		if err := rows.Scan(&er.Timestamp, &er.Tool, &er.Host, &er.CommandSummary, &er.ErrorMessage); err == nil {
			list = append(list, er)
		}
	}
	return list, nil
}

type ToolBytes struct {
	Tool      string
	P50       int64
	P90       int64
	P99       int64
	Max       int64
	Truncated int
	NoResult  int
}

type TopReturn struct {
	Timestamp      string
	Tool           string
	CommandSummary string
	Bytes          int64
}

func queryToolBytes(db *sql.DB, whereClause string) ([]ToolBytes, error) {
	q := fmt.Sprintf(`
		SELECT tool, bytes_out, truncated, no_result
		FROM audit %s
		ORDER BY tool, bytes_out;
	`, whereClause)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		values    []int64
		truncated int
		noResult  int
	}
	byTool := map[string]*acc{}
	var order []string
	for rows.Next() {
		var tool string
		var b int64
		var tr, nr int
		if err := rows.Scan(&tool, &b, &tr, &nr); err != nil {
			continue
		}
		a, ok := byTool[tool]
		if !ok {
			a = &acc{}
			byTool[tool] = a
			order = append(order, tool)
		}
		a.values = append(a.values, b)
		a.truncated += tr
		a.noResult += nr
	}

	list := make([]ToolBytes, 0, len(order))
	for _, tool := range order {
		a := byTool[tool]
		list = append(list, ToolBytes{
			Tool:      tool,
			P50:       percentile(a.values, 0.50),
			P90:       percentile(a.values, 0.90),
			P99:       percentile(a.values, 0.99),
			Max:       a.values[len(a.values)-1],
			Truncated: a.truncated,
			NoResult:  a.noResult,
		})
	}
	return list, nil
}

func queryTopReturns(db *sql.DB, whereClause string, limit int) ([]TopReturn, error) {
	q := fmt.Sprintf(`
		SELECT strftime('%%d/%%m %%H:%%M:%%S', timestamp, 'localtime'), tool, COALESCE(command_summary, '-'), bytes_out
		FROM audit %s
		ORDER BY bytes_out DESC
		LIMIT ?;
	`, whereClause)

	rows, err := db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TopReturn
	for rows.Next() {
		var r TopReturn
		if err := rows.Scan(&r.Timestamp, &r.Tool, &r.CommandSummary, &r.Bytes); err == nil {
			list = append(list, r)
		}
	}
	return list, nil
}

// percentile usa o método nearest-rank sobre valores já ordenados crescentemente.
func percentile(sorted []int64, p float64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

func truncRunes(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-3]) + "..."
}

func calculateP95(durations []int64) int64 {
	n := len(durations)
	if n == 0 {
		return 0
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	idx := int(float64(n) * 0.95)
	if idx >= n {
		idx = n - 1
	}
	return durations[idx]
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
