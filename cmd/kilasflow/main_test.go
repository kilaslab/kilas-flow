package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The startup path is Open followed by Migrate and nothing else. The schema
// used to come from the models main handed the database package, so this test
// guards the replacement: a default SQLite install has to reach a usable schema
// from the migration files alone.
func TestADefaultInstallBootsToAMigratedSchema(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, log)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	for _, table := range []string{"workflows", "executions", "credentials", "schedules"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("a default install did not create the %q table", table)
		}
	}
}

// One configured egress policy governs HTTP and database targets alike: the
// guard the database executors receive carries the outbound section's policy,
// exactly as run() builds it, so allow_private_networks: true permits a
// private database and false refuses it before anything dials.
func TestOneEgressPolicyGovernsDatabaseTargets(t *testing.T) {
	fields := map[string]string{
		"host": "127.0.0.1", "port": "1", "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	}

	permissive := sqlnode.Guard{Policy: outboundPolicy(config.OutboundHTTP{AllowPrivateNetworks: true})}
	_, err := sqlnode.Open(context.Background(), sqlnode.DriverPostgres, fields, permissive)
	if err == nil {
		t.Fatal("a connection to a closed port reported success")
	}
	if errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Fatalf("error = %v, an allowed private database must fail at the dial, not at the policy", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("the dial failure leaked the password: %v", err)
	}

	restrictive := sqlnode.Guard{Policy: outboundPolicy(config.OutboundHTTP{})}
	_, err = sqlnode.Open(context.Background(), sqlnode.DriverPostgres, fields, restrictive)
	if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Fatalf("error = %v, want ErrForbiddenTarget under the default-deny policy", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("the refusal leaked the password: %v", err)
	}
}

// The boot guard must resolve the installation's own database on every
// driver it serves, and refuse to boot when it cannot: an unresolvable
// identity still returns "allowed" for every credential.
func TestDatabaseGuardResolvesTheInstallationsOwnTarget(t *testing.T) {
	t.Parallel()

	postgres, err := databaseGuard(config.Database{
		Driver: "postgres",
		DSN:    "postgres://kilas:hunter2@db.internal:5433/kilasflow?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("databaseGuard() error = %v", err)
	}
	if postgres.Internal == nil {
		t.Fatal("a postgres install has no internal network target")
	}
	if postgres.Internal.Host != "db.internal" || postgres.Internal.Port != "5433" || postgres.Internal.Database != "kilasflow" {
		t.Fatalf("internal target = %+v, want db.internal:5433/kilasflow", postgres.Internal)
	}

	if _, err := databaseGuard(config.Database{Driver: "postgres", DSN: "postgres://:bad port/"}); err == nil {
		t.Fatal("an unparsable internal DSN booted without a guard")
	}

	sqlite, err := databaseGuard(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	})
	if err != nil {
		t.Fatalf("databaseGuard() error = %v", err)
	}
	if len(sqlite.InternalPaths) != 1 || sqlite.Internal != nil {
		t.Fatalf("sqlite guard = %+v, want exactly one file path and no network target", sqlite)
	}

	empty, err := databaseGuard(config.Database{Driver: "postgres"})
	if err != nil {
		t.Fatalf("databaseGuard() error = %v", err)
	}
	if !reflect.DeepEqual(empty, sqlnode.Guard{}) {
		t.Fatalf("empty-DSN guard = %+v, want the zero guard", empty)
	}
}

// Two processes must never share a worker identity: the pid alone repeats
// across hosts, so the default carries the hostname, the pid, and a random
// suffix. An explicit --worker-id keeps its spelling for operators who want
// stable identities in their logs.
func TestDefaultWorkerIDIsHostQualifiedAndUnique(t *testing.T) {
	t.Parallel()
	first, second := defaultWorkerID(), defaultWorkerID()
	if first == "" || second == "" {
		t.Fatal("defaultWorkerID() returned an empty identity")
	}
	if first == second {
		t.Fatalf("defaultWorkerID() returned %q twice, want a unique identity per process", first)
	}
	for _, id := range []string{first, second} {
		if !strings.HasPrefix(id, "kilasflow-") {
			t.Errorf("worker ID %q has no kilasflow- prefix", id)
		}
		if strings.Contains(id, "/") || strings.Contains(id, " ") {
			t.Errorf("worker ID %q carries a separator that would confuse the lease_owner fencing token", id)
		}
	}
}

func TestResolveWorkerIDHonoursAnExplicitOverride(t *testing.T) {
	t.Parallel()
	if got := resolveWorkerID("worker-07"); got != "worker-07" {
		t.Errorf("resolveWorkerID(override) = %q, want the override untouched", got)
	}
	if got := resolveWorkerID("   "); got == "" || got == "   " {
		t.Errorf("resolveWorkerID(blank) = %q, want the generated default", got)
	}
	if got := resolveWorkerID(""); got == "" {
		t.Error("resolveWorkerID(empty) returned empty, want the generated default")
	}
}

// reachableCompiler is a Compiler that claims to be available, so the
// catalogue's silence can be checked without a real toolchain.
type reachableCompiler struct{}

func (reachableCompiler) Compile(context.Context, string) ([]byte, error) { return nil, nil }
func (reachableCompiler) Available() bool                                 { return true }

// An operator reading the node catalogue has to be told what to provide. "No
// Go compiler" alone sends them looking for a flag that does not exist.
func TestNodeAvailabilityNamesWhatTheOperatorMustProvide(t *testing.T) {
	t.Parallel()

	unavailable := nodeAvailability(nil)()
	message, found := unavailable[nodes.CodeNodeType]
	if !found {
		t.Fatalf("nodeAvailability() = %v, want the Code node reported", unavailable)
	}
	for _, wanted := range []string{"cannot compile Code nodes", runcode.MinimumGoVersion, "KILASFLOW_CODE_GO_BINARY", "code.go_binary", "PATH"} {
		if !strings.Contains(message, wanted) {
			t.Errorf("the catalogue says %q, want it to name %q", message, wanted)
		}
	}

	if available := nodeAvailability(reachableCompiler{})(); available != nil {
		t.Errorf("nodeAvailability() = %v with a reachable compiler, want no unavailable nodes", available)
	}
}

// A configured directory is what makes a compiled artifact survive a restart —
// which is the only way a deployment without a toolchain keeps running the
// Code nodes it already has.
func TestBuildCodeCachesPersistsWhenConfigured(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()

	artifacts, modules := buildCodeCaches(config.Code{CacheDir: dir, CacheMaxBytes: 0}, log)
	if _, ok := artifacts.(*runcode.DiskCache); !ok {
		t.Fatalf("buildCodeCaches() returned a %T, want the durable cache", artifacts)
	}
	if modules == nil {
		t.Fatal("buildCodeCaches() returned no translation cache")
	}
	defer modules.Close(context.Background())

	// A second call over the same directory is what a restart looks like: the
	// artifact written by the first must be visible to the second.
	artifact := runcode.Artifact{
		Hash:           runcode.SourceHash("return items, nil"),
		RuntimeVersion: runcode.RuntimeVersion,
		Module:         wasmtest.MinimalModule("{\"items\":[]}\n"),
		CompiledAt:     time.Now().UTC(),
	}
	artifacts.Put(artifact)
	restarted, _ := buildCodeCaches(config.Code{CacheDir: dir, CacheMaxBytes: 0}, log)
	stored, found := restarted.Get(artifact.Hash)
	if !found || !bytes.Equal(stored.Module, artifact.Module) {
		t.Error("an artifact written through one cache was not found by the next")
	}
}

// A cache is an optimisation. A directory that cannot be used — a path under a
// regular file, a volume that is not mounted — must warn and leave the server
// able to run, not refuse the boot.
func TestBuildCodeCachesFallsBackToMemoryWhenTheDirectoryIsUnusable(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	artifacts, modules := buildCodeCaches(config.Code{CacheDir: filepath.Join(blocker, "codecache"), CacheMaxBytes: 0}, log)
	if _, ok := artifacts.(*runcode.MemoryCache); !ok {
		t.Errorf("buildCodeCaches() returned a %T, want the in-memory cache", artifacts)
	}
	if modules == nil {
		t.Fatal("buildCodeCaches() returned no translation cache")
	}
	defer modules.Close(context.Background())

	// And an empty directory means memory too, which is the documented way to
	// keep every cache out of the filesystem.
	empty, _ := buildCodeCaches(config.Code{}, log)
	if _, ok := empty.(*runcode.MemoryCache); !ok {
		t.Errorf("buildCodeCaches(no cache_dir) returned a %T, want the in-memory cache", empty)
	}
}

// The keys only matter if they reach the toolchain: code.go_binary is what an
// operator points at a mounted toolchain, and code.cache_dir is the only
// writable place a read-only root filesystem offers the Go build cache.
func TestBuildCodeCompilerUsesTheConfiguredBinaryAndBuildCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	compiler := buildCodeCompiler(config.Code{GoBinary: "/opt/golang/bin/go", CacheDir: dir})
	if compiler.GoBinary != "/opt/golang/bin/go" {
		t.Errorf("GoBinary = %q, want the configured binary", compiler.GoBinary)
	}
	if compiler.CacheDir != runcode.GoBuildDir(dir) {
		t.Errorf("CacheDir = %q, want the toolchain's cache under %s", compiler.CacheDir, dir)
	}

	// An empty cache_dir means no GOCACHE is invented.
	plain := buildCodeCompiler(config.Code{})
	if plain.CacheDir != "" {
		t.Errorf("CacheDir = %q, want none without a configured cache directory", plain.CacheDir)
	}
}
