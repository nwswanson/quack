package server

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"quack/internal/logformat"
)

var processLogLevel slog.LevelVar

func init() {
	processLogLevel.Set(slog.LevelWarn)
}

func ConfigureLogger(value string, out io.Writer) error {
	if err := SetLogLevel(value); err != nil {
		return err
	}
	slog.SetDefault(NewLogger(out))
	return nil
}

func NewLogger(out io.Writer) *slog.Logger {
	return NewLoggerWithLevel(out, &processLogLevel)
}

func NewLoggerWithLevel(out io.Writer, level *slog.LevelVar) *slog.Logger {
	return logformat.NewSlogLogger(out, level)
}

func SetLogLevel(value string) error {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return fmt.Errorf("unknown level %q; expected debug, info, warn, or error", value)
	}
	processLogLevel.Set(level)
	return nil
}
