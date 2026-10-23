package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const SessionsRoot = `W:\Data\sessions`

var (
	sessionIDRegex    = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	forbiddenKeywords = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bATTACH\b`),
		regexp.MustCompile(`(?i)\bDETACH\b`),
		regexp.MustCompile(`(?i)\bINSTALL\b`),
		regexp.MustCompile(`(?i)\bLOAD\b`),
		regexp.MustCompile(`(?i)\bEXPORT\s+DATABASE\b`),
		regexp.MustCompile(`(?i)\bPRAGMA\b`),
	}
	sensitivePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)[\\/]\.ssh[\\/]`),
		regexp.MustCompile(`(?i)id_rsa|id_ed25519`),
		regexp.MustCompile(`(?i)windows[\\/]system32[\\/]config`),
		regexp.MustCompile(`(?i)/etc/shadow|/etc/passwd`),
	}
)

func EnsureRoot() error {
	err := os.MkdirAll(SessionsRoot, 0755)
	SweepExpiredSessions(24 * time.Hour)
	return err
}

func SweepExpiredSessions(maxAge time.Duration) {
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	entries, err := os.ReadDir(SessionsRoot)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			dirPath := filepath.Join(SessionsRoot, entry.Name())
			_ = os.RemoveAll(dirPath)
		}
	}
}

func NormalizeSessionID(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		clean = "default"
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	clean = re.ReplaceAllString(clean, "-")
	if len(clean) > 32 {
		clean = clean[:32]
	}
	if !strings.HasPrefix(clean, "sess-") {
		clean = "sess-" + clean
	}
	return clean
}

func GenerateSessionID(prefix string) string {
	cleanPrefix := ""
	if prefix != "" {
		re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
		cleanPrefix = re.ReplaceAllString(prefix, "-")
		if len(cleanPrefix) > 20 {
			cleanPrefix = cleanPrefix[:20]
		}
	}

	timestamp := time.Now().Format("20060102-150405")
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	randomHex := hex.EncodeToString(b)

	if cleanPrefix != "" {
		return fmt.Sprintf("sess-%s-%s-%s", cleanPrefix, timestamp, randomHex)
	}
	return fmt.Sprintf("sess-%s-%s", timestamp, randomHex)
}

func ValidateSessionID(sessionID string) error {
	if !sessionIDRegex.MatchString(sessionID) {
		return errors.New("invalid session format (must be 1-64 chars: alphanumeric, _, -)")
	}
	return nil
}

func GetOrCreateSessionDir(sessionName string) (string, error) {
	sessionID := NormalizeSessionID(sessionName)
	if err := ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	_ = EnsureRoot()
	dir := filepath.Join(SessionsRoot, sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create session directory: %w", err)
	}
	return dir, nil
}

func ValidateSQL(sql string) error {
	// Bloqueia comandos destrutivos / expansão de extensões
	for _, kw := range forbiddenKeywords {
		if kw.MatchString(sql) {
			return fmt.Errorf("sandboxing violation: comando perigoso proibido: %s", kw.String())
		}
	}

	// Bloqueia acesso a arquivos de credenciais ou do sistema operacional
	for _, sp := range sensitivePatterns {
		if sp.MatchString(sql) {
			return errors.New("sandboxing violation: acesso a caminhos sensíveis do sistema proibido")
		}
	}

	return nil
}
