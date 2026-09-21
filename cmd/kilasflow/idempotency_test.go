package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func openIdempotencyTestDB(t *testing.T) *database.DB {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, log)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	return db
}

// seedExpiredKey records one key whose retention has already passed, so the
// next sweep has something to delete.
func seedExpiredKey(t *testing.T, store repository.IdempotencyRepository, key string) {
	t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}
	past := time.Now().UTC().Add(-2 * time.Hour)
	token := "token-" + key
	claim, err := store.Claim(ctx, repository.IdempotencyClaim{
		Tenant: tenant, Key: key, Operation: "run-workflow", RequestHash: strings.Repeat("a", 64), Token: token,
		Now: past, InFlightUntil: past,
	})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !claim.Acquired {
		t.Fatalf("Claim() did not acquire %q", key)
	}
	recorded, err := store.Complete(ctx, tenant, key, token, 202, []byte(`{"id":"exec_1"}`), past)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !recorded {
		t.Fatalf("Complete() did not record %q", key)
	}
}

func countIdempotencyKeys(t *testing.T, db *database.DB) int64 {
	t.Helper()
	var count int64
	if err := db.DB.Raw("SELECT count(*) FROM idempotency_keys").Scan(&count).Error; err != nil {
		t.Fatalf("count idempotency_keys = %v", err)
	}
	return count
}

func waitForNoIdempotencyKeys(t *testing.T, db *database.DB) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countIdempotencyKeys(t, db) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("idempotency_keys rows = %d, want 0", countIdempotencyKeys(t, db))
}

// The sweeper has to be started by the roles that serve the API and must stop
// promptly, because Stop runs before the database close on the shutdown path.
func TestTheIdempotencySweeperRunsOnEveryRoleThatServesTheAPI(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := openIdempotencyTestDB(t)
	store := repository.NewIdempotencyStore(db.DB)
	service, err := idempotency.NewService(store, idempotency.Options{Retention: time.Hour})
	if err != nil {
		t.Fatalf("idempotency.NewService() error = %v", err)
	}

	restore := idempotencySweepInterval
	idempotencySweepInterval = 10 * time.Millisecond
	t.Cleanup(func() { idempotencySweepInterval = restore })

	for _, role := range []processRole{processRoleBoth, processRoleAPI} {
		seedExpiredKey(t, store, "key-"+string(role))
		if countIdempotencyKeys(t, db) != 1 {
			t.Fatalf("role %q: seeded rows = %d, want 1", role, countIdempotencyKeys(t, db))
		}

		ctx, cancel := context.WithCancel(context.Background())
		sweeper := startIdempotencySweeper(ctx, role, service, log)
		if sweeper == nil {
			cancel()
			t.Fatalf("role %q started no sweeper", role)
		}
		waitForNoIdempotencyKeys(t, db)

		stopped := make(chan struct{})
		go func() {
			sweeper.Stop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatalf("role %q: Stop did not return", role)
		}
		cancel()
	}

	if sweeper := startIdempotencySweeper(context.Background(), processRoleWorker, service, log); sweeper != nil {
		t.Error("the worker role started a sweeper, but no worker claims a key")
	}
	if sweeper := startIdempotencySweeper(context.Background(), processRoleAPI, nil, log); sweeper != nil {
		t.Error("a nil service started a sweeper")
	}
}

// TestRunWiresTheIdempotencyServiceAndItsSweeper reads the composition root
// rather than calling a helper.
//
// A test of startIdempotencySweeper passes even when run() no longer calls it,
// and main.go is the most contended file of this parallel wave: a merge has
// already deleted this repository's retention call sites once (see the comment
// above TestRetentionSweepersAreWired, which only pins signatures). This one
// fails when the call site, the service construction, the Deps field, or the
// deferred Stop() goes. The defer is part of the wiring rather than a detail:
// the sweeper must stop before the database it sweeps closes, and replacing the
// defer with a blank assignment used to leave this test green.
func TestRunWiresTheIdempotencyServiceAndItsSweeper(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go = %v", err)
	}
	var runFn *ast.FuncDecl
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "run" {
			runFn = fn
			break
		}
	}
	if runFn == nil {
		t.Fatal("main.go declares no func run")
	}

	var buildsService, startsSweeper, setsDeps, defersSweeperStop bool
	// sweeperIdent is the variable run() binds startIdempotencySweeper(...) to.
	// The defer is required to name that variable and call Stop() on it, so a
	// blank assignment in its place does not pass.
	var sweeperIdent string
	ast.Inspect(runFn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			if typed.Tok != token.DEFINE || len(typed.Lhs) != len(typed.Rhs) {
				break
			}
			for i, rhs := range typed.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok {
					continue
				}
				callee, ok := call.Fun.(*ast.Ident)
				if !ok || callee.Name != "startIdempotencySweeper" {
					continue
				}
				if bound, ok := typed.Lhs[i].(*ast.Ident); ok {
					startsSweeper = true
					sweeperIdent = bound.Name
				}
			}
		case *ast.DeferStmt:
			selector, ok := typed.Call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Stop" {
				break
			}
			receiver, ok := selector.X.(*ast.Ident)
			if ok && sweeperIdent != "" && receiver.Name == sweeperIdent {
				defersSweeperStop = true
			}
		case *ast.CallExpr:
			if selector, ok := typed.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "idempotency" && selector.Sel.Name == "NewService" {
					buildsService = true
				}
			}
		case *ast.KeyValueExpr:
			if key, ok := typed.Key.(*ast.Ident); ok && key.Name == "Idempotency" {
				setsDeps = true
			}
		}
		return true
	})

	if !buildsService {
		t.Error("run() does not build the idempotency service")
	}
	if !startsSweeper {
		t.Error("run() does not start the idempotency sweeper")
	}
	if !defersSweeperStop {
		t.Error("run() does not defer the idempotency sweeper's Stop()")
	}
	if !setsDeps {
		t.Error("run() does not pass Idempotency in the api.Deps literal")
	}
}
