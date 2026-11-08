package telemetry

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// RenderDiscover reports where output tokens go and what the exec soft cap
// would save, so filters and caps are tuned from data, not guesses.
func RenderDiscover(filter StatsFilter, softCapTokens int) (string, error) {
	db, err := GetDB()
	if err != nil {
		return "", err
	}
	return renderDiscover(db, filter, softCapTokens)
}

type discAgg struct {
	calls, tokens, max int64
}

func renderDiscover(db *sql.DB, filter StatsFilter, softCapTokens int) (string, error) {
	q := `SELECT tool, COALESCE(command_summary,''), COALESCE(est_tokens_out,0), COALESCE(client,''), COALESCE(conversation,'') FROM audit ` + periodWhere(filter)
	rows, err := db.Query(q)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	byTool := map[string]*discAgg{}
	byClient := map[string]*discAgg{}
	byConv := map[string]*discAgg{}
	byCmd := map[string]*discAgg{}
	reads := map[string]int64{}
	var execCalls, execTokens, overCap, saved int64
	for rows.Next() {
		var tool, summary, client, conv string
		var tok int64
		if err := rows.Scan(&tool, &summary, &tok, &client, &conv); err != nil {
			return "", err
		}
		if i := strings.LastIndexByte(client, '#'); i > 0 {
			client = client[:i] // agrupa pelo nome do cliente, sem o pid
		}
		if client == "" {
			client = "(unknown / before tracking)"
		}
		ca := byClient[client]
		if ca == nil {
			ca = &discAgg{}
			byClient[client] = ca
		}
		ca.calls++
		ca.tokens += tok
		if conv != "" {
			k := client + " " + conv
			if len(conv) > 8 {
				k = client + " " + conv[:8]
			}
			cv := byConv[k]
			if cv == nil {
				cv = &discAgg{}
				byConv[k] = cv
			}
			cv.calls++
			cv.tokens += tok
		}
		a := byTool[tool]
		if a == nil {
			a = &discAgg{}
			byTool[tool] = a
		}
		a.calls++
		a.tokens += tok
		if tok > a.max {
			a.max = tok
		}
		switch tool {
		case "exec":
			execCalls++
			execTokens += tok
			k := cmdKey(summary)
			c := byCmd[k]
			if c == nil {
				c = &discAgg{}
				byCmd[k] = c
			}
			c.calls++
			c.tokens += tok
			if tok > c.max {
				c.max = tok
			}
			if int(tok) > softCapTokens {
				overCap++
				saved += tok - int64(softCapTokens)
			}
		case "read_file":
			reads[summary]++
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	label, _ := periodDescription(filter)
	var b strings.Builder
	fmt.Fprintf(&b, "FastExec discover — %s\n\nTokens returned to the model, by tool:\n", label)
	for _, k := range topKeys(byTool, 8) {
		a := byTool[k]
		fmt.Fprintf(&b, "  %-12s %7d calls %10d tokens  (largest %d)\n", k, a.calls, a.tokens, a.max)
	}
	fmt.Fprintf(&b, "\nBy client (MCP channel):\n")
	for _, k := range topKeys(byClient, 8) {
		a := byClient[k]
		fmt.Fprintf(&b, "  %-28s %7d calls %10d tokens\n", k, a.calls, a.tokens)
	}
	if len(byConv) > 0 {
		fmt.Fprintf(&b, "\nBy conversation (clients that send an id), %d tracked:\n", len(byConv))
		for _, k := range topKeys(byConv, 6) {
			a := byConv[k]
			fmt.Fprintf(&b, "  %-37s %7d calls %10d tokens\n", k, a.calls, a.tokens)
		}
	}
	fmt.Fprintf(&b, "\nexec: %d calls, %d tokens. Soft cap %d tokens would cut %d calls and save ~%d tokens.\n", execCalls, execTokens, softCapTokens, overCap, saved)
	fmt.Fprintf(&b, "\nexec by command (first word):\n")
	for _, k := range topKeys(byCmd, 12) {
		a := byCmd[k]
		fmt.Fprintf(&b, "  %-16s %7d calls %10d tokens  (largest %d)\n", k, a.calls, a.tokens, a.max)
	}

	type rd struct {
		path string
		n    int64
	}
	var rep []rd
	for p, n := range reads {
		if n >= 3 && p != "" {
			rep = append(rep, rd{p, n})
		}
	}
	sort.Slice(rep, func(i, j int) bool { return rep[i].n > rep[j].n })
	if len(rep) > 0 {
		fmt.Fprintf(&b, "\nFiles read 3+ times with read_file (candidates for code_outline / pattern):\n")
		for i, r := range rep {
			if i == 8 {
				break
			}
			fmt.Fprintf(&b, "  %4dx %s\n", r.n, r.path)
		}
	}
	return b.String(), nil
}

// cmdKey reduces a command summary to its program (plus subcommand for the
// usual multi-tool CLIs).
func cmdKey(s string) string {
	s = strings.TrimSpace(s)
	w := strings.Fields(s)
	for len(w) > 1 && strings.EqualFold(w[0], "cd") {
		w = w[1:]
		for len(w) > 0 && (w[0] == "&&" || w[0] == ";") {
			w = w[1:]
		}
	}
	if len(w) == 0 {
		return "(empty)"
	}
	k := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(w[0], `"'`), `\`, "/")))
	switch k {
	case "git", "docker", "go", "npm", "dotnet", "cargo", "systemctl", "journalctl", "adb":
		if len(w) > 1 && !strings.HasPrefix(w[1], "-") {
			k += " " + strings.ToLower(w[1])
		}
	}
	return k
}

func topKeys(m map[string]*discAgg, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]].tokens > m[keys[j]].tokens })
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}
