package server

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

var processLogLevel slog.LevelVar

func init() {
	processLogLevel.Set(slog.LevelWarn)
}

func ConfigureLogger(value string, out io.Writer) error {
	if err := SetLogLevel(value); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{
		Level: &processLogLevel,
	})))
	return nil
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
