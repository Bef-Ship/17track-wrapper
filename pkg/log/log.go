// Package log is a thin slog wrapper with service-scoped loggers.
package log

import (
	"fmt"
	stdlog "log"
	"log/slog"
	"os"
	"strings"
)

// Init configures the process-wide default logger.
// level: debug|info|warn|error (default info).
// LOG_FORMAT=json switches to JSON; otherwise text (docker-friendly).
func Init(level string) {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
	stdlog.SetFlags(0)
	stdlog.SetOutput(slogWriter{})
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Svc returns a logger tagged with svc=<name>.
func Svc(name string) *Logger {
	return &Logger{l: slog.Default().With("svc", name)}
}

// Logger is a service-scoped slog wrapper.
type Logger struct {
	l *slog.Logger
}

func (l *Logger) Debug(msg string, kv ...any) { l.l.Debug(msg, kv...) }
func (l *Logger) Info(msg string, kv ...any)  { l.l.Info(msg, kv...) }
func (l *Logger) Warn(msg string, kv ...any)  { l.l.Warn(msg, kv...) }
func (l *Logger) Error(msg string, kv ...any) { l.l.Error(msg, kv...) }

func (l *Logger) Debugf(format string, args ...any) { l.l.Debug(fmt.Sprintf(format, args...)) }
func (l *Logger) Infof(format string, args ...any)  { l.l.Info(fmt.Sprintf(format, args...)) }
func (l *Logger) Warnf(format string, args ...any)  { l.l.Warn(fmt.Sprintf(format, args...)) }
func (l *Logger) Errorf(format string, args ...any) { l.l.Error(fmt.Sprintf(format, args...)) }

// Package-level helpers (cmd / wiring).

func Debug(msg string, kv ...any) { slog.Default().Debug(msg, kv...) }
func Info(msg string, kv ...any)  { slog.Default().Info(msg, kv...) }
func Warn(msg string, kv ...any)  { slog.Default().Warn(msg, kv...) }
func Error(msg string, kv ...any) { slog.Default().Error(msg, kv...) }

func Debugf(format string, args ...any) { slog.Default().Debug(fmt.Sprintf(format, args...)) }
func Infof(format string, args ...any)  { slog.Default().Info(fmt.Sprintf(format, args...)) }
func Warnf(format string, args ...any)  { slog.Default().Warn(fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { slog.Default().Error(fmt.Sprintf(format, args...)) }

func Fatalf(format string, args ...any) {
	slog.Default().Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

// slogWriter adapts stdlib log output into slog Info lines.
type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		slog.Default().Info(msg)
	}
	return len(p), nil
}
