package repository

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// The notification carries identifiers only and stays far inside PostgreSQL's
// roughly 8000-byte NOTIFY payload limit.
func TestExecutionWakePayloadCarriesIdentifiersOnly(t *testing.T) {
	payload, err := executionWakePayload("tenant-9", "exec_01HXYZ")
	if err != nil {
		t.Fatalf("executionWakePayload() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload %q is not JSON: %v", payload, err)
	}
	if len(decoded) != 2 {
		t.Errorf("payload holds %d keys, want exactly tenant and execution IDs", len(decoded))
	}
	if len(payload) > 8000 {
		t.Errorf("payload is %d bytes, past the NOTIFY limit", len(payload))
	}
	if len(payload) > 200 {
		t.Errorf("payload is %d bytes for two short IDs, want tens of bytes", len(payload))
	}
	wake, err := parseExecutionWake(payload)
	if err != nil {
		t.Fatalf("parseExecutionWake() error = %v", err)
	}
	if wake.TenantID != "tenant-9" || wake.ExecutionID != "exec_01HXYZ" {
		t.Errorf("round trip = %+v, want the queued identifiers", wake)
	}
}

func TestParseExecutionWakeRefusesAnythingElse(t *testing.T) {
	for name, payload := range map[string]string{
		"not JSON":          "execution_wake",
		"wrong shape":       `{"channel":"execution_wake"}`,
		"missing execution": `{"tenant_id":"t"}`,
		"missing tenant":    `{"execution_id":"exec_1"}`,
		"empty":             "",
	} {
		if _, err := parseExecutionWake(payload); err == nil {
			t.Errorf("%s: parseExecutionWake(%q) = nil, want an error", name, payload)
		}
	}
}

func TestTablePrefixReadsTheOpenTimeNamingStrategy(t *testing.T) {
	if got := tablePrefix(nil); got != "" {
		t.Errorf("tablePrefix(nil) = %q, want empty", got)
	}
	if got := tablePrefix(&gorm.DB{Config: &gorm.Config{}}); got != "" {
		t.Errorf("tablePrefix without a namer = %q, want empty", got)
	}
	db := &gorm.DB{Config: &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "kflow_"}}}
	if got := tablePrefix(db); got != "kflow_" {
		t.Errorf("tablePrefix = %q, want %q", got, "kflow_")
	}
	// The wake channel and the ORM resolve the prefix through the same value,
	// so a prefixed install listens where its own queues notify.
	if got := ExecutionWakeChannel(tablePrefix(db)); !strings.HasPrefix(got, "kflow_") {
		t.Errorf("channel %q does not carry the handle's prefix", got)
	}
}

func TestIsPostgresFollowsTheDialector(t *testing.T) {
	if isPostgres(nil) {
		t.Error("isPostgres(nil) = true, want false")
	}
	sqliteDB, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if isPostgres(sqliteDB) {
		t.Error("isPostgres(sqlite) = true, want false")
	}
	pgDB, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost"}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	if !isPostgres(pgDB) {
		t.Error("isPostgres(postgres) = false, want true")
	}
}
