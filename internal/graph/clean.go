package graph

// clean.go: governança do graph.db. Clean apaga o índice de um repositório (as três tabelas, por repo_root) e
// compacta o banco com VACUUM quando sobra muito espaço livre; não exige .git, para limpar repositório apagado ou
// movido. Repos lista o que está indexado.

import "fmt"

// CleanStats conta o que Clean removeu.
type CleanStats struct {
	Files, Symbols, Edges int64
	Vacuumed              bool
}

// Clean remove o índice do repositório e, se o espaço livre passar de 25% do banco, roda VACUUM.
func Clean(root string) (CleanStats, error) {
	var st CleanStats
	if !exists() {
		return st, nil
	}
	d, err := store()
	if err != nil {
		return st, err
	}
	key := repoKey(root)
	tx, err := d.Begin()
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	for _, q := range []struct {
		dst *int64
		sql string
	}{
		{&st.Edges, `DELETE FROM code_edges WHERE repo_root = ?`},
		{&st.Symbols, `DELETE FROM code_symbols WHERE repo_root = ?`},
		{&st.Files, `DELETE FROM code_files WHERE repo_root = ?`},
	} {
		res, err := tx.Exec(q.sql, key)
		if err != nil {
			return st, err
		}
		*q.dst, _ = res.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}

	var free, pages int64
	_ = d.QueryRow(`PRAGMA freelist_count`).Scan(&free)
	_ = d.QueryRow(`PRAGMA page_count`).Scan(&pages)
	if pages > 0 && free*4 >= pages {
		if _, err := d.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return st, err
		}
		if _, err := d.Exec(`VACUUM`); err != nil {
			return st, err
		}
		st.Vacuumed = true
	}
	return st, nil
}

// Repos lista os repositórios indexados com o número de arquivos de cada um.
func Repos() ([]string, error) {
	if !exists() {
		return nil, nil
	}
	d, err := store()
	if err != nil {
		return nil, err
	}
	rows, err := d.Query(`SELECT repo_root, COUNT(*) FROM code_files GROUP BY repo_root ORDER BY repo_root`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		var n int
		if err := rows.Scan(&r, &n); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s (%d files)", r, n))
	}
	return out, nil
}
