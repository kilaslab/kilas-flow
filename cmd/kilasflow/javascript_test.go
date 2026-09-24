package main

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
)

// A worker user the server cannot start workers as stops the boot, naming the
// keys, instead of failing every JavaScript run after it. The server's own
// user is one on every platform: it would take nothing away.
func TestAWorkerUserTheServerCannotUseStopsTheBoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's own user is no worker user to configure")
	}
	cfg := config.Default().Code
	cfg.JavaScriptWorkerUID, cfg.JavaScriptWorkerGID = os.Geteuid(), os.Getegid()
	_, closeJavaScript, err := javaScriptRuntime(cfg, slog.New(slog.DiscardHandler))
	if err == nil {
		closeJavaScript()
		t.Fatal("javaScriptRuntime() = nil error, want the boot stopped")
	}
	if !strings.Contains(err.Error(), "code.javascript_worker_uid") {
		t.Fatalf("javaScriptRuntime() = %v, want it to name the key", err)
	}
}
