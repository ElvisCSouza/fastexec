package tools

// sql_local.go: consulta SQLite no host local roda in-process, com o driver embutido (o mesmo modernc.org/sqlite da
// telemetria), sem depender de sqlite3 e base64 existirem no Windows. Antes a linha `echo | base64 -d | sqlite3 -json`
// ia pelo shell local e morria sem pista: stdout vazio com exit 0 virava `[]` para qualquer SELECT. Aqui arquivo
// ausente, falha de abertura e SQL inválido devolvem erro claro; só SELECT sem linhas devolve `[]`.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// querySQLiteLocal abre o arquivo, roda a consulta e devolve o resultado no formato JSON do `sqlite3 -json` (mesmo
// contrato do caminho remoto): `[{...},\n {...}]`, uma linha por objeto, chaves na ordem das colunas; sem linhas → `[]`.
func querySQLiteLocal(dbPath, query string) (string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("database file not found: %s", dbPath)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", fmt.Errorf("cannot open database %s: %v", dbPath, err)
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		return "", fmt.Errorf("sqlite error: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("sqlite error: %v", err)
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range ptrs {
		ptrs[i] = &vals[i]
	}

	var sb strings.Builder
	sb.WriteByte('[')
	first := true
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", fmt.Errorf("sqlite error: %v", err)
		}
		if !first {
			sb.WriteString(",\n ")
		}
		first = false
		sb.WriteByte('{')
		for i, c := range cols {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(sqlJSONString(c))
			sb.WriteByte(':')
			sb.WriteString(sqlJSONString(vals[i]))
		}
		sb.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("sqlite error: %v", err)
	}
	sb.WriteString("]")
	return sb.String(), nil
}

// sqlJSONString codifica um valor de célula no jeito do `sqlite3 -json`: INTEGER/REAL como número, TEXT/BLOB como
// string JSON e NULL como null. Qualquer tipo inesperado do driver vira string.
func sqlJSONString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		b, _ := json.Marshal(t)
		return string(b)
	case []byte:
		b, _ := json.Marshal(string(t))
		return string(b)
	case string:
		b, _ := json.Marshal(t)
		return string(b)
	default:
		b, _ := json.Marshal(fmt.Sprintf("%v", t))
		return string(b)
	}
}
