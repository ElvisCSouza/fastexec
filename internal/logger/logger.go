package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxLogSize = 5 * 1024 * 1024 // 5 MB

type Logger struct {
	mu      sync.Mutex
	logPath string
	file    *os.File
	size    int64
	debug   bool
}

var globalLogger *Logger
var once sync.Once

func Init() *Logger {
	once.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir := filepath.Join(home, ".local", "state", "commander")
		_ = os.MkdirAll(dir, 0755)
		logPath := filepath.Join(dir, "commander.log")

		debug := os.Getenv("COMMANDER_DEBUG") == "1"

		l := &Logger{
			logPath: logPath,
			debug:   debug,
		}
		// Lazy open: o arquivo só é aberto/criado se houver algo para gravar,
		// evitando syscalls de disco em comandos CLI rápidos.
		globalLogger = l
	})
	return globalLogger
}

func (l *Logger) openFile() {
	if l == nil {
		return
	}
	info, err := os.Stat(l.logPath)
	if err == nil {
		l.size = info.Size()
		if l.size > maxLogSize {
			l.rotate()
		}
	}
	f, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		l.file = f
	}
}

func (l *Logger) rotate() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	backup := l.logPath + ".1"
	_ = os.Remove(backup)
	_ = os.Rename(l.logPath, backup)
	l.size = 0
}

func (l *Logger) log(level, format string, args ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, args...)
	entry := fmt.Sprintf("[%s] [%s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), level, msg)

	if l.file == nil {
		l.openFile()
	}
	if l.file != nil {
		n, _ := io.WriteString(l.file, entry)
		l.size += int64(n)
		if l.size > maxLogSize {
			l.rotate()
			l.openFile()
		}
	}

	// Logs para stderr apenas (nunca para stdout que é do MCP)
	if l.debug || level == "ERROR" {
		_, _ = fmt.Fprint(os.Stderr, entry)
	}
}

func Info(format string, args ...any) {
	if globalLogger != nil {
		globalLogger.log("INFO", format, args...)
	}
}

func Error(format string, args ...any) {
	if globalLogger != nil {
		globalLogger.log("ERROR", format, args...)
	}
}

func Debug(format string, args ...any) {
	if globalLogger != nil && globalLogger.debug {
		globalLogger.log("DEBUG", format, args...)
	}
}
