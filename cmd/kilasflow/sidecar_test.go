package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/sidecar"
	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

func sidecarTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// sidecarTestDeps builds the three catalogues the sidecar registers into. The
// credential registry is scratch rather than credentials.Default(): the global
// one is shared with every other test in the process, and a fixture credential
// type leaking into it would make a later test's result depend on this one.
func sidecarTestDeps() sidecarDeps {
	return sidecarDeps{
		Definitions: node.NewRegistry(),
		Executors:   engine.NewRegistry(),
		Credentials: credentials.NewRegistry(),
		Policy:      safehttp.DefaultPolicy(),
		Log:         sidecarTestLogger(),
	}
}

// sidecarTestConfig enables the sidecar against the fixture packages the
// sidecar's own tests use. It calls sidecartest.Node so a machine without Node
// skips rather than passing silently.
func sidecarTestConfig(t *testing.T, packages ...string) config.Sidecar {
	t.Helper()
	cfg := config.Default().Sidecar
	cfg.Enabled = true
	cfg.NodePath = sidecartest.Node(t)
	cfg.PackagesDir = sidecartest.PackagesDir(t)
	cfg.Packages = packages
	cfg.RuntimeDir = t.TempDir()
	return cfg
}

func TestSetupSidecarDisabledChangesNothing(t *testing.T) {
	deps := sidecarTestDeps()

	runtime, err := setupSidecar(context.Background(), config.Default().Sidecar, deps)
	if err != nil {
		t.Fatalf("setupSidecar(disabled) error = %v", err)
	}
	if runtime != nil {
		t.Fatal("setupSidecar(disabled) returned a runtime; a declined sidecar must start nothing")
	}
	if got := len(deps.Definitions.List()); got != 0 {
		t.Errorf("definitions registered by a disabled sidecar = %d, want 0", got)
	}
	if got := len(deps.Executors.Registered()); got != 0 {
		t.Errorf("executors registered by a disabled sidecar = %v, want none", got)
	}
	if got := len(deps.Credentials.List()); got != 0 {
		t.Errorf("credential types registered by a disabled sidecar = %d, want 0", got)
	}
}

func TestSetupSidecarRegistersNodesAndAnExecutorEveryDefinitionResolvesTo(t *testing.T) {
	deps := sidecarTestDeps()
	cfg := sidecarTestConfig(t, "kf-fixture-nodes")

	runtime, err := setupSidecar(context.Background(), cfg, deps)
	if err != nil {
		t.Fatalf("setupSidecar() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	var sidecarTypes int
	for _, definition := range deps.Definitions.List() {
		if definition.Source != node.SourceSidecar {
			t.Errorf("definition %q registered from a fixture package has source %q, want sidecar", definition.Type, definition.Source)
			continue
		}
		sidecarTypes++
		if definition.ExecutorID != nodes.SidecarExecutorID {
			t.Errorf("definition %q binds executor %q, want %q", definition.Type, definition.ExecutorID, nodes.SidecarExecutorID)
		}
		if _, found := deps.Executors.Lookup(definition.ExecutorID); !found {
			t.Errorf("definition %q points at executor %q, which is not registered", definition.Type, definition.ExecutorID)
		}
	}
	if sidecarTypes == 0 {
		t.Fatal("the sidecar registered no node definitions for a package that carries two")
	}
	if got, want := runtime.index.Len(), sidecarTypes; got != want {
		t.Errorf("index holds %d nodes, want %d (every registered definition must resolve to a run)", got, want)
	}
}

func TestSetupSidecarRefusesTheBootOnABrokenPackageNamingIt(t *testing.T) {
	deps := sidecarTestDeps()
	cfg := sidecarTestConfig(t, "kf-fixture-badmanifest")

	_, err := setupSidecar(context.Background(), cfg, deps)
	if err == nil {
		t.Fatal("setupSidecar() = nil error for a package whose manifest is unusable, want a refusal")
	}
	if !strings.Contains(err.Error(), "kf-fixture-badmanifest") {
		t.Errorf("setupSidecar() error = %v, want it to name the package", err)
	}
	// A refused boot must not leave the manifest's nodes half-registered.
	for _, definition := range deps.Definitions.List() {
		if definition.Source == node.SourceSidecar {
			t.Errorf("definition %q was registered by a refused package", definition.Type)
		}
	}
}

func TestSetupSidecarKeepsBootingWhenOneNodeFileFailsToLoad(t *testing.T) {
	deps := sidecarTestDeps()
	cfg := sidecarTestConfig(t, "kf-fixture-badload")

	runtime, err := setupSidecar(context.Background(), cfg, deps)
	if err != nil {
		t.Fatalf("setupSidecar() error = %v, want a boot that excludes the broken node", err)
	}
	t.Cleanup(runtime.Close)
	if runtime.index.Len() != 0 {
		t.Errorf("index holds %d nodes, want 0: the only node file fails to load", runtime.index.Len())
	}
	if len(runtime.index.Exclusions()) == 0 {
		t.Error("Exclusions() is empty; a per-file failure must be recorded, not swallowed")
	}
}

func TestSetupSidecarRefusesAnOldNode(t *testing.T) {
	deps := sidecarTestDeps()
	cfg := sidecarTestConfig(t, "kf-fixture-nodes")

	// A stub that answers --version with an old major, so the refusal is
	// exercised without depending on whichever node the machine happens to
	// have. This is the shape a Node 20 image produces.
	stub := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho v20.11.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.NodePath = stub

	_, err := setupSidecar(context.Background(), cfg, deps)
	if err == nil {
		t.Fatal("setupSidecar() = nil error for Node 20, want a refusal")
	}
	if !strings.Contains(err.Error(), "Node 24") {
		t.Errorf("setupSidecar() error = %v, want it to say Node 24 is required", err)
	}
}

func TestSidecarAvailabilityReportsAnOutageWithoutSpawningPerRequest(t *testing.T) {
	definitions := node.NewRegistry()
	for _, nodeType := range []string{"sidecar.test.greet", "sidecar.test.relay"} {
		if err := definitions.RegisterFrom(node.SourceSidecar, sidecarAvailabilityDefinition(nodeType)); err != nil {
			t.Fatalf("register %s: %v", nodeType, err)
		}
	}
	if err := definitions.Register(sidecarAvailabilityDefinition("kilasflow.test.builtin")); err != nil {
		t.Fatalf("register the built-in: %v", err)
	}

	nodePath := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\necho v24.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	clock := time.Unix(0, 0)
	runtime := &sidecarRuntime{
		definitions: definitions,
		nodePath:    nodePath,
		checkNode: func(string) (string, error) {
			calls.Add(1)
			if fail.Load() {
				return "v20.11.0", errors.New("sidecar needs Node 24 or newer, found v20.11.0")
			}
			return "v24.0.0", nil
		},
		verdict:    "sidecar needs Node 24 or newer, found v20.11.0",
		verdictAt:  clock,
		verdictTTL: time.Minute,
		now:        func() time.Time { return clock },
	}

	// A cached failure answers every request: ten catalogue reads must not cost
	// ten probes.
	for range 10 {
		report := runtime.Availability()
		if len(report) != 2 {
			t.Fatalf("Availability() = %v, want exactly the two sidecar-tagged types", report)
		}
		if _, tagged := report["kilasflow.test.builtin"]; tagged {
			t.Errorf("Availability() reported a built-in node: %v", report)
		}
		for _, reason := range report {
			if !strings.Contains(reason, "cannot start") {
				t.Errorf("reason = %q, want it to say the sidecar cannot start", reason)
			}
		}
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("checkNode ran %d times behind the cached verdict, want 0", got)
	}

	// Once the verdict expires the probe runs again, and a Node that is back
	// clears the report without a restart.
	clock = clock.Add(2 * time.Minute)
	if report := runtime.Availability(); len(report) != 2 {
		t.Fatalf("Availability() after expiry = %v, want the failure re-probed and still reported", report)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("checkNode ran %d times after the verdict expired, want 1", got)
	}
	fail.Store(false)
	clock = clock.Add(2 * time.Minute)
	if report := runtime.Availability(); report != nil {
		t.Errorf("Availability() after recovery = %v, want nil", report)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("checkNode ran %d times, want 2", got)
	}

	// A Node that is gone is caught by the stat, which spawns nothing at all.
	missing := &sidecarRuntime{
		definitions: definitions,
		nodePath:    filepath.Join(t.TempDir(), "absent-node"),
		checkNode: func(string) (string, error) {
			t.Error("checkNode ran for a node binary that does not exist")
			return "", nil
		},
		verdictTTL: time.Minute,
		now:        func() time.Time { return clock },
	}
	report := missing.Availability()
	if len(report) != 2 {
		t.Fatalf("Availability() with no node binary = %v, want both sidecar types reported", report)
	}
}

func sidecarAvailabilityDefinition(nodeType string) node.Definition {
	return node.Definition{
		Type:        nodeType,
		Version:     workflow.V(1),
		DisplayName: "Availability Fixture",
		Category:    "Community",
		Group:       []node.NodeGroup{node.GroupTransform},
		ExecutorID:  nodes.SidecarExecutorID,
		Inputs:      []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
}

func TestShutdownReapsEveryChild(t *testing.T) {
	deps := sidecarTestDeps()
	runtime, err := setupSidecar(context.Background(), sidecarTestConfig(t, "kf-fixture-nodes"), deps)
	if err != nil {
		t.Fatalf("setupSidecar() error = %v", err)
	}
	runSidecarFixtureNode(t, runtime, "tenant-a")
	runSidecarFixtureNode(t, runtime, "tenant-b")

	// The describe run is tracked too and is already reaped by the load, so the
	// liveness precondition is checked against the tenants' own processes.
	pids := append(runtime.children.forTenant("tenant-a"), runtime.children.forTenant("tenant-b")...)
	if len(pids) < 2 {
		t.Fatalf("tracked %d tenant processes, want one per tenant", len(pids))
	}
	for _, pid := range pids {
		if !sidecarProcessAlive(pid) {
			t.Fatalf("child %d is not alive before shutdown; the test proves nothing", pid)
		}
	}

	runtime.Close()

	for _, pid := range pids {
		if sidecarProcessAlive(pid) {
			t.Errorf("child %d survived Close", pid)
		}
	}
}

func TestEvictTenantReapsOnlyThatTenant(t *testing.T) {
	deps := sidecarTestDeps()
	runtime, err := setupSidecar(context.Background(), sidecarTestConfig(t, "kf-fixture-nodes"), deps)
	if err != nil {
		t.Fatalf("setupSidecar() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	runSidecarFixtureNode(t, runtime, "tenant-a")
	runSidecarFixtureNode(t, runtime, "tenant-b")

	first := runtime.children.forTenant("tenant-a")
	second := runtime.children.forTenant("tenant-b")
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("tracked pids tenant-a=%v tenant-b=%v, want both non-empty", first, second)
	}

	runtime.EvictTenant("tenant-a")

	for _, pid := range first {
		if sidecarProcessAlive(pid) {
			t.Errorf("tenant-a's child %d survived its tenant's eviction", pid)
		}
	}
	for _, pid := range second {
		if !sidecarProcessAlive(pid) {
			t.Errorf("tenant-b's child %d died with tenant-a's eviction", pid)
		}
	}
}

// runSidecarFixtureNode runs the fixture Greet node once for a tenant, so the
// pool starts and keeps a warm process for it.
func runSidecarFixtureNode(t *testing.T, runtime *sidecarRuntime, tenant string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := runtime.pool.Execute(ctx, sidecar.Request{
		Tenant:      tenant,
		Node:        "fixtureGreet",
		NodeVersion: 1,
		Params:      map[string]any{"name": "world", "providerId": "fixture"},
		Credentials: map[string]map[string]string{"fixtureApi": {"baseUrl": "https://fixture.test", "apiKey": "fixture-key"}},
		Items:       []sidecar.Item{{JSON: map[string]any{"name": "world"}}},
	})
	if err != nil {
		t.Fatalf("Execute(%s) error = %v", tenant, err)
	}
}

// sidecarProcessAlive reports whether a pid is still present. Signal 0 performs
// the permission and existence check without delivering anything.
func sidecarProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
