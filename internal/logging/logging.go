// Package logging builds the JSON logger used by every service (docs/contracts/observability.md §3).
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON logger tagged with the service name.
func New(service, level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})).With("service", service)
}
