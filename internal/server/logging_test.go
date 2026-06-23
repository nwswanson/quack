package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLoggerWritesQuackStdoutFormat(t *testing.T) {
	var level slog.LevelVar
	level.Set(slog.LevelDebug)
	var out bytes.Buffer
	logger := NewLoggerWithLevel(&out, &level)

	logger.InfoContext(context.Background(), "starlark log",
		slog.String("site", "trello"),
		slog.Int64("version", 3),
		slog.String("route", "/api"),
		slog.String("message", "hello from script"),
		slog.String("path", "/boards"),
	)

	line := out.String()
	for _, want := range []string{
		" info starlark [trello@v3 /api] ",
		`"hello from script"`,
		" - ",
		"path=/boards",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line = %q, want %q", line, want)
		}
	}
}
