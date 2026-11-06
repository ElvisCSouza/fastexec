package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/telemetry"
)

type HandoffArgs struct {
	Action  string `json:"action"` // "save", "generate", "latest", "read", "search", "list"
	Topic   string `json:"topic,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Pending string `json:"pending,omitempty"`
	Query   string `json:"query,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func (a *HandoffArgs) UnmarshalJSON(data []byte) error {
	type Alias HandoffArgs
	var aux struct {
		Alias
		Op          string `json:"op"`
		Cmd         string `json:"cmd"`
		Operation   string `json:"operation"`
		Title       string `json:"title"`
		Subject     string `json:"subject"`
		Name        string `json:"name"`
		Summary     string `json:"summary"`
		Desc        string `json:"desc"`
		Description string `json:"description"`
		Meta        string `json:"meta"`
		Next        string `json:"next"`
		Todo        string `json:"todo"`
		Todos       string `json:"todos"`
		NextSteps   string `json:"next_steps"`
		Q           string `json:"q"`
		Search      string `json:"search"`
		Term        string `json:"term"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*a = HandoffArgs(aux.Alias)

	if a.Action == "" {
		for _, s := range []string{aux.Op, aux.Cmd, aux.Operation} {
			if s != "" {
				a.Action = strings.ToLower(strings.TrimSpace(s))
				break
			}
		}
	} else {
		a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	}
	if a.Action == "" {
		a.Action = "latest"
	}

	if a.Topic == "" {
		for _, s := range []string{aux.Title, aux.Subject, aux.Name} {
			if s != "" {
				a.Topic = s
				break
			}
		}
	}
	if a.Notes == "" {
		for _, s := range []string{aux.Summary, aux.Desc, aux.Description, aux.Meta} {
			if s != "" {
				a.Notes = s
				break
			}
		}
	}
	if a.Pending == "" {
		for _, s := range []string{aux.Next, aux.Todo, aux.Todos, aux.NextSteps} {
			if s != "" {
				a.Pending = s
				break
			}
		}
	}
	if a.Query == "" {
		for _, s := range []string{aux.Q, aux.Search, aux.Term} {
			if s != "" {
				a.Query = s
				break
			}
		}
	}
	return nil
}

// GetHandoffDir determina o diretório canônico de handoffs.
func GetHandoffDir() string {
	// 1. Homelab primário
	homelabDir := `W:\ReposHomeLab\.agent\handoff`
	if _, err := os.Stat(`W:\ReposHomeLab`); err == nil {
		_ = os.MkdirAll(homelabDir, 0755)
		return homelabDir
	}

	// 2. Diretório local .agent/handoff relativo ao CWD
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, ".agent", "handoff")
		if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
			_ = os.MkdirAll(candidate, 0755)
			return candidate
		}
	}

	// 3. HOME do usuário
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".agent", "handoff")
		_ = os.MkdirAll(candidate, 0755)
		return candidate
	}

	tmp := filepath.Join(os.TempDir(), "fastexec-handoff")
	_ = os.MkdirAll(tmp, 0755)
	return tmp
}

var sanitizeTopicRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeTopic(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = sanitizeTopicRegex.ReplaceAllString(t, "-")
	t = strings.Trim(t, "-")
	if t == "" {
		return "sessao"
	}
	if len(t) > 40 {
		t = t[:40]
	}
	return t
}

func HandleHandoff(args HandoffArgs) (string, error) {
	handoffDir := GetHandoffDir()

	switch args.Action {
	case "save", "generate", "create":
		return handleHandoffSave(handoffDir, args)
	case "latest", "read", "get":
		return handleHandoffLatest(handoffDir)
	case "search", "find":
		return handleHandoffSearch(handoffDir, args.Query, args.Limit)
	case "list", "ls":
		return handleHandoffList(handoffDir, args.Limit)
	default:
		return "", fmt.Errorf("ação desconhecida: '%s' (use: save, latest, search, list)", args.Action)
	}
}

type failedCmd struct {
	Command string
	Error   string
}

type handoffFacts struct {
	ModifiedFiles     []string
	Failures          []failedCmd
	RecentValidations []string
}

func extractHandoffFacts() (*handoffFacts, error) {
	facts := &handoffFacts{}

	db, err := telemetry.GetDB()
	if err != nil || db == nil {
		return facts, err
	}

	rows, qErr := db.Query(`
		SELECT tool, command_summary, success, error_message 
		FROM audit 
		WHERE timestamp >= datetime('now', '-4 hours') OR timestamp >= date('now')
		ORDER BY id DESC LIMIT 150;
	`)
	if qErr != nil {
		return facts, qErr
	}
	defer rows.Close()

	fileSet := make(map[string]bool)
	failSet := make(map[string]bool)
	valSet := make(map[string]bool)

	for rows.Next() {
		var tool, summary, errMsg string
		var success int
		if err := rows.Scan(&tool, &summary, &success, &errMsg); err != nil {
			continue
		}

		summary = strings.TrimSpace(summary)
		if summary == "" {
			continue
		}

		// 1. Arquivos modificados
		if tool == "write_file" || tool == "edit_file" || tool == "put_file" {
			if !fileSet[summary] && !strings.Contains(summary, "inspect_telemetry") && !strings.Contains(summary, "handoff") {
				fileSet[summary] = true
				facts.ModifiedFiles = append(facts.ModifiedFiles, summary)
			}
		}

		// 2. Falhas / Negative Knowledge
		if success == 0 && errMsg != "" {
			key := summary + "|" + errMsg
			if !failSet[key] && len(facts.Failures) < 6 {
				failSet[key] = true
				facts.Failures = append(facts.Failures, failedCmd{
					Command: summary,
					Error:   errMsg,
				})
			}
		}

		// 3. Comandos de validação bem-sucedidos
		if success == 1 && (tool == "exec" || tool == "job") {
			lower := strings.ToLower(summary)
			if (strings.Contains(lower, "test") || strings.Contains(lower, "build") || strings.Contains(lower, "status") || strings.Contains(lower, "git")) && !valSet[summary] && len(facts.RecentValidations) < 6 {
				valSet[summary] = true
				facts.RecentValidations = append(facts.RecentValidations, summary)
			}
		}
	}

	return facts, nil
}

func handleHandoffSave(dir string, args HandoffArgs) (string, error) {
	now := time.Now()
	topic := sanitizeTopic(args.Topic)
	filename := fmt.Sprintf("%s-handoff-%s.md", now.Format("2006-01-02"), topic)
	filePath := filepath.Join(dir, filename)
	latestPath := filepath.Join(dir, "latest.md")

	facts, _ := extractHandoffFacts()
	modifiedFiles := facts.ModifiedFiles
	failures := facts.Failures
	recentValidations := facts.RecentValidations

	// Monta o Markdown estruturado
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Handoff — %s — %s\n\n", strings.Title(strings.ReplaceAll(topic, "-", " ")), now.Format("2006-01-02 15:04")))

	sb.WriteString("## Meta / Resumo\n")
	if strings.TrimSpace(args.Notes) != "" {
		sb.WriteString(strings.TrimSpace(args.Notes))
		sb.WriteString("\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("Sessão de trabalho em %s consolidada automaticamente via FastExec Telemetry.\n\n", topic))
	}

	sb.WriteString(fmt.Sprintf("## Arquivos Modificados (%d)\n", len(modifiedFiles)))
	if len(modifiedFiles) > 0 {
		for _, f := range modifiedFiles {
			sb.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
	} else {
		sb.WriteString("- Nenhum arquivo gravado via write_file/edit_file nesta janela.\n")
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("## Falhas Registradas (Negative Knowledge / O que Evitar)\n"))
	if len(failures) > 0 {
		for _, fail := range failures {
			sb.WriteString(fmt.Sprintf("- ⚠️ `%s`\n  - *Erro:* %s\n", fail.Command, fail.Error))
		}
	} else {
		sb.WriteString("- Nenhuma falha crítica recente registrada.\n")
	}
	sb.WriteString("\n")

	if len(recentValidations) > 0 {
		sb.WriteString("## Validações Recentes\n")
		for _, v := range recentValidations {
			sb.WriteString(fmt.Sprintf("- ✔ `%s`\n", v))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Pendente / Próximos Passos\n")
	if strings.TrimSpace(args.Pending) != "" {
		sb.WriteString(strings.TrimSpace(args.Pending))
		sb.WriteString("\n")
	} else {
		sb.WriteString("Continuar a partir dos arquivos modificados listados acima.\n")
	}

	content := sb.String()

	// Grava o arquivo datado
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("falha ao gravar handoff em '%s': %w", filePath, err)
	}

	// Atualiza o ponteiro latest.md
	_ = os.WriteFile(latestPath, []byte(content), 0644)

	res := fmt.Sprintf("[FastExec Handoff] Salvo com sucesso:\n- Arquivo: %s\n- Ponteiro Atualizado: %s\n\n---\n%s", filePath, latestPath, content)
	return res, nil
}

func handleHandoffLatest(dir string) (string, error) {
	latestPath := filepath.Join(dir, "latest.md")
	if data, err := os.ReadFile(latestPath); err == nil && len(data) > 0 {
		return string(data), nil
	}

	// Se latest.md não existe, busca o mais recente na pasta
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("pasta de handoff não encontrada: %w", err)
	}

	var mdFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") && e.Name() != "latest.md" {
			mdFiles = append(mdFiles, e.Name())
		}
	}

	if len(mdFiles) == 0 {
		return "Nenhum handoff registrado anteriormente em " + dir, nil
	}

	sort.Strings(mdFiles)
	lastFile := mdFiles[len(mdFiles)-1]
	data, err := os.ReadFile(filepath.Join(dir, lastFile))
	if err != nil {
		return "", err
	}

	// Aproveita para criar o latest.md
	_ = os.WriteFile(latestPath, data, 0644)
	return string(data), nil
}

func handleHandoffSearch(dir, query string, limit int) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("informe o parâmetro 'query' para pesquisa")
	}
	if limit <= 0 {
		limit = 5
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("erro lendo pasta de handoff: %w", err)
	}

	lowerQ := strings.ToLower(query)
	var matches []string

	// Ordena do mais recente para o mais antigo
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() > entries[j].Name()
	})

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") || e.Name() == "latest.md" {
			continue
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		content := string(data)
		if strings.Contains(strings.ToLower(content), lowerQ) {
			lines := strings.Split(content, "\n")
			var hitLines []string
			for idx, l := range lines {
				if strings.Contains(strings.ToLower(l), lowerQ) {
					start := idx - 1
					if start < 0 {
						start = 0
					}
					end := idx + 2
					if end > len(lines) {
						end = len(lines)
					}
					snippet := strings.Join(lines[start:end], "\n")
					hitLines = append(hitLines, fmt.Sprintf("  [L%d]:\n%s", idx+1, snippet))
					if len(hitLines) >= 2 {
						break
					}
				}
			}
			matches = append(matches, fmt.Sprintf("### %s\n%s", e.Name(), strings.Join(hitLines, "\n---\n")))
			if len(matches) >= limit {
				break
			}
		}
	}

	if len(matches) == 0 {
		return fmt.Sprintf("Nenhuma correspondência encontrada para '%s' em %s", query, dir), nil
	}

	return fmt.Sprintf("=== RESULTADOS DA BUSCA PARA '%s' ===\n\n%s", query, strings.Join(matches, "\n\n")), nil
}

func handleHandoffList(dir string, limit int) (string, error) {
	if limit <= 0 {
		limit = 10
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("erro ao listar handoffs: %w", err)
	}

	var mdFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			mdFiles = append(mdFiles, e)
		}
	}

	if len(mdFiles) == 0 {
		return "Nenhum arquivo de handoff em " + dir, nil
	}

	sort.Slice(mdFiles, func(i, j int) bool {
		return mdFiles[i].Name() > mdFiles[j].Name()
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== ARQUIVOS DE HANDOFF EM %s ===\n\n", dir))
	count := 0
	for _, f := range mdFiles {
		info, err := f.Info()
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %-35s (%6d bytes | %s)\n", f.Name(), info.Size(), info.ModTime().Format("02/01 15:04")))
		count++
		if count >= limit {
			break
		}
	}
	return sb.String(), nil
}

var (
	autoCheckpointMu    sync.Mutex
	autoCheckpointTimer *time.Timer
	lastAutoSavedCount  int
)

// ScheduleAutoCheckpoint agenda a sincronização silenciosa do handoff (debounce de 15 segundos).
func ScheduleAutoCheckpoint() {
	autoCheckpointMu.Lock()
	defer autoCheckpointMu.Unlock()

	if autoCheckpointTimer != nil {
		autoCheckpointTimer.Stop()
	}

	autoCheckpointTimer = time.AfterFunc(15*time.Second, func() {
		FlushAutoCheckpoint()
	})
}

// FlushAutoCheckpoint executa o auto-checkpoint imediato se houver novos dados.
func FlushAutoCheckpoint() {
	autoCheckpointMu.Lock()
	if autoCheckpointTimer != nil {
		autoCheckpointTimer.Stop()
		autoCheckpointTimer = nil
	}
	autoCheckpointMu.Unlock()

	handoffDir := GetHandoffDir()
	facts, err := extractHandoffFacts()
	if err != nil || facts == nil {
		return
	}

	totalEvents := len(facts.ModifiedFiles) + len(facts.Failures) + len(facts.RecentValidations)
	if totalEvents == 0 || totalEvents == lastAutoSavedCount {
		return
	}

	autoCheckpointMu.Lock()
	lastAutoSavedCount = totalEvents
	autoCheckpointMu.Unlock()

	_, _ = handleHandoffSave(handoffDir, HandoffArgs{
		Action: "save",
		Topic:  "auto-session",
		Notes:  "Checkpoint contínuo gerado automaticamente pelo FastExec em background.",
	})
}

