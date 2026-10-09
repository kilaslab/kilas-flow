//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testRunner is a runner that never talks to a server: it only starts shell
// commands, with the limits a test sets.
func testRunner(t *testing.T, ctx context.Context, timeout, waitDelay time.Duration) *runner {
	t.Helper()

	return &runner{
		ctx: ctx, scratch: t.TempDir(), binDir: t.TempDir(), url: "http://127.0.0.1:1",
		timeout: timeout, waitDelay: waitDelay,
	}
}

// A pipeline's second stage holds the output pipe open after the shell is gone.
// Killing only the shell, which is what a plain CommandContext does, leaves Wait
// blocked until that stage ends on its own: a stuck example held the whole run
// past its timeout. The group is killed, so the command returns near the timeout.
func TestShellKillsTheWholePipelineAtTheTimeout(t *testing.T) {
	r := testRunner(t, context.Background(), 300*time.Millisecond, 5*time.Second)

	started := time.Now()
	_, _, err := r.shell("sleep 30 | cat", nil)
	elapsed := time.Since(started)

	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("shell() error = %v, want it to say the command did not finish", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("shell() took %s to give up on a 300ms timeout; a stage of the pipeline outlived it", elapsed)
	}
}

// Whatever a stuck command's children do, the runner stops waiting for their
// output a few seconds after the kill: a child that left the group (a daemon, a
// setsid) must not hold the run for as long as it likes.
func TestShellStopsWaitingForAnOrphanThatKeepsThePipeOpen(t *testing.T) {
	// A child that starts a session of its own is outside the group the runner
	// kills. perl is on macOS and on the CI runner; setsid is the fallback.
	var orphan string
	if perl, err := exec.LookPath("perl"); err == nil {
		orphan = perl + ` -MPOSIX -e 'POSIX::setsid(); exec "sleep", "4"' & wait`
	} else if setsid, err := exec.LookPath("setsid"); err == nil {
		orphan = setsid + " sleep 4 & wait"
	} else {
		t.Skip("neither perl nor setsid is installed here")
	}
	r := testRunner(t, context.Background(), 300*time.Millisecond, 500*time.Millisecond)

	started := time.Now()
	_, _, runErr := r.shell(orphan, nil)
	elapsed := time.Since(started)

	if runErr == nil {
		t.Fatal("shell() returned no error for a command that outlived its timeout")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("shell() waited %s for an orphan holding the pipe, want it to give up after the 500ms wait delay", elapsed)
	}
}

func TestShellStopsWhenTheRunIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := testRunner(t, ctx, time.Minute, 5*time.Second)
	time.AfterFunc(200*time.Millisecond, cancel)

	started := time.Now()
	_, _, err := r.shell("sleep 30 | cat", nil)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("shell() returned no error for an interrupted command")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("shell() kept running %s after the run was interrupted", elapsed)
	}
}

// The binary under test is bounded the same way: a seed command that hangs
// fails the run near the timeout.
func TestCliKillsATimedOutBinaryAndItsChildren(t *testing.T) {
	script := filepath.Join(t.TempDir(), "kilasflow")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 | cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := testRunner(t, context.Background(), 300*time.Millisecond, 5*time.Second)
	r.binary = script

	started := time.Now()
	_, err := r.cli("workflow", "list")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("cli() returned no error for a binary that outlived its timeout")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("cli() took %s to give up on a 300ms timeout", elapsed)
	}
}
