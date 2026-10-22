package analytics

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func FindDuckDB() (string, error) {
	userProfile := os.Getenv("USERPROFILE")
	if userProfile != "" {
		localPath := filepath.Join(userProfile, ".local", "bin", "duckdb.exe")
		if _, err := os.Stat(localPath); err == nil {
			return localPath, nil
		}
	}

	if p, err := exec.LookPath("duckdb.exe"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("duckdb"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("duckdb.exe não encontrado em ~/.local/bin ou PATH")
}

func ExecuteDuckDBQuery(sessionName, sql string, timeoutMs int) (string, error) {
	if err := ValidateSQL(sql); err != nil {
		return "", err
	}

	sessionDir, err := GetOrCreateSessionDir(sessionName)
	if err != nil {
		return "", err
	}

	duckdbPath, err := FindDuckDB()
	if err != nil {
		return "", err
	}

	if timeoutMs <= 0 {
		timeoutMs = 60000
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, duckdbPath, "session.duckdb", "-json", "-c", sql)
	cmd.Dir = sessionDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("query timed out after %d ms", timeoutMs)
	}

	if runErr != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = runErr.Error()
		}
		return "", fmt.Errorf("duckdb error: %s", errMsg)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return `[{"status":"ok"}]`, nil
	}

	return out, nil
}
