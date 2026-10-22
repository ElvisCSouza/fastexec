package telemetry

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	_ "modernc.org/sqlite"
)

type StatsFilter struct {
	Today       bool
	OnlyErrors  bool
	OnlyTools   bool
	ResetDB     bool
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

	db, err := GetDB()
	if err != nil {
		return "", err
	}

	whereClause := ""
	if filter.Today {
		whereClause = "WHERE date(timestamp, 'localtime') = date('now', 'localtime')"
	}

	// 1. Resumo Global
	summary, durations, err := querySummary(db, whereClause)
	if err != nil {
		return "", fmt.Errorf("erro consultando resumo: %w", err)
	}

	if summary.TotalCalls == 0 {
		period := "no histórico geral"
		if filter.Today {
			period = "hoje"
		}
		return fmt.Sprintf("Nenhum registro de telemetria encontrado (%s).", period), nil
	}

	summary.P95DurationMs = calculateP95(durations)

	// 2. Breakdown por Ferramenta
	tools, err := queryTools(db, whereClause)
	if err != nil {
		return "", fmt.Errorf("erro consultando ferramentas: %w", err)
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
	periodLabel := "Histórico Total"
	if filter.Today {
		periodLabel = fmt.Sprintf("Hoje (%s)", time.Now().Format("02/01/2006"))
	}

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
