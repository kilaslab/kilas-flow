package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestTheBootLoggerWritesTheSameTextLinesAsTheConfiguredOne(t *testing.T) {
	// A configuration warning is raised before newLogger runs. It used to go
	// through slog's built-in default, `2026/09/26 23:45:59 WARN ...` on
	// stderr, while every line after it read `time=... level=...` on stdout,
	// so the one line saying a setting was ignored was the line a collector
	// parsed differently or missed.
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	var out bytes.Buffer
	installBootLogger(&out)
	slog.Warn("configuration key matches nothing and was ignored", "key", "outbound.allowed_private_endpoint")

	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "time=") || !strings.Contains(line, "level=WARN") {
		t.Fatalf("boot warning = %q, want the text handler's time=... level=WARN form", line)
	}
	if !strings.Contains(line, "key=outbound.allowed_private_endpoint") {
		t.Errorf("boot warning = %q, want its attributes", line)
	}
}
