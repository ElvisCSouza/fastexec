package telemetry

// outcome_stats.go: bloco OUTCOMES do `fastexec stats`. Separa falha de infraestrutura, uso/validação, código de
// saída não-zero do comando e o que não se classifica. Linhas antigas (outcome_schema=0) entram só como contagem
// de legado: não são reclassificadas. O bloco aparece apenas quando há linhas com outcome_schema=1.

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// OutcomeSummary: contagens do período, por categoria.
type OutcomeSummary struct {
	NewRows       int64           // linhas com outcome_schema=1
	Success       int64           // error_type vazio
	Infra         int64           // transport_lost, tool_internal, timeout
	Usage         int64           // invalid_params, guard_rejected, not_found
	CommandFailed int64           // command_failed: comando rodou e saiu ≠ 0
	Unknown       int64           // error_type = unknown (ou vazio com falha)
	ExitCodes     map[int64]int64 // distribuição dos códigos de saída em command_failed
	LegacyRows    int64           // outcome_schema=0 (sem desfecho explícito)
	LegacyFails   int64           // legado com success=0
}

// withAnd acrescenta uma condição a uma cláusula WHERE existente (ou cria uma).
func withAnd(whereClause, cond string) string {
	if whereClause == "" {
		return "WHERE " + cond
	}
	return whereClause + " AND " + cond
}

func queryOutcomes(db *sql.DB, whereClause string) (OutcomeSummary, error) {
	s := OutcomeSummary{ExitCodes: map[int64]int64{}}

	rows, err := db.Query(`SELECT outcome_schema, COALESCE(error_type,''), success, COUNT(*)
		FROM audit ` + whereClause + ` GROUP BY outcome_schema, COALESCE(error_type,''), success`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var schema, success, n int64
		var et string
		if err := rows.Scan(&schema, &et, &success, &n); err != nil {
			return s, err
		}
		if schema == 0 {
			s.LegacyRows += n
			if success == 0 {
				s.LegacyFails += n
			}
			continue
		}
		s.NewRows += n
		switch et {
		case "":
			s.Success += n
		case ErrTransportLost, ErrToolInternal, ErrTimeout:
			s.Infra += n
		case ErrInvalidParams, ErrGuardRejected, ErrNotFound:
			s.Usage += n
		case ErrCommandFailed:
			s.CommandFailed += n
		default:
			s.Unknown += n
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	if s.NewRows == 0 {
		return s, nil
	}

	xq := `SELECT command_exit_code, COUNT(*) FROM audit ` +
		withAnd(whereClause, "outcome_schema=1 AND error_type='"+ErrCommandFailed+"'") +
		` GROUP BY command_exit_code`
	xr, err := db.Query(xq)
	if err != nil {
		return s, err
	}
	defer xr.Close()
	for xr.Next() {
		var code, n int64
		if err := xr.Scan(&code, &n); err != nil {
			return s, err
		}
		s.ExitCodes[code] = n
	}
	return s, xr.Err()
}

// renderOutcomes monta o bloco. Retorna "" quando não há linhas novas (o relatório fica como antes).
func renderOutcomes(s OutcomeSummary) string {
	if s.NewRows == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("OUTCOMES (rows with explicit outcome; legacy rows are not reclassified)\n")
	sb.WriteString("--------------------------------------------------------------------------------\n")
	fmt.Fprintf(&sb, "Success:                %d\n", s.Success)
	fmt.Fprintf(&sb, "Infrastructure failure: %d (transport_lost, tool_internal, timeout)\n", s.Infra)
	fmt.Fprintf(&sb, "Usage / validation:     %d (invalid_params, guard_rejected, not_found)\n", s.Usage)
	fmt.Fprintf(&sb, "Command non-zero exit:  %d", s.CommandFailed)
	if len(s.ExitCodes) > 0 {
		codes := make([]int64, 0, len(s.ExitCodes))
		for code := range s.ExitCodes {
			codes = append(codes, code)
		}
		sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
		parts := make([]string, 0, len(codes))
		for _, code := range codes {
			parts = append(parts, fmt.Sprintf("exit %d: %d", code, s.ExitCodes[code]))
		}
		fmt.Fprintf(&sb, " (%s)", strings.Join(parts, ", "))
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "Unknown error class:    %d\n", s.Unknown)
	if s.LegacyRows > 0 {
		fmt.Fprintf(&sb, "Legacy rows (no outcome): %d (%d failed by success flag)\n", s.LegacyRows, s.LegacyFails)
	}
	sb.WriteString("\n")
	return sb.String()
}
