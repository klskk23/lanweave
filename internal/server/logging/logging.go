// Package logging configures the process-wide structured logger.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// ParseLevel maps a config string to an slog.Level. Unknown values default to info.
func ParseLevel(level string) slog.Level {
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

// Setup builds a JSON logger writing to stdout at the given level and installs it
// as the default slog logger so every package logs in the same structured format.
func Setup(level string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: ParseLevel(level)})
	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger
}
