package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
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
