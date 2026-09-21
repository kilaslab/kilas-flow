package tenantpurge_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantpurge"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/migrations"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// dialect is one SQL dialect the orchestrator tests run against. SQLite always
// runs; PostgreSQL joins when KILASFLOW_TEST_POSTGRES_DSN names a live server,
// the same gate internal/database and internal/datastore use.
type dialect struct {
	name string
	open func(t *testing.T, prefix string) *database.DB
}

// purgeDialects is the gate every test in this package runs behind. Both
// dialects matter here more than anywhere else in the tree: the purge composes
// DDL (DROP TABLE) and deletes by tenant, and the two dialects disagree about
// transactions, about optional tables, and about what a placeholder may be.
func purgeDialects(t *testing.T) []dialect {
	t.Helper()
	dialects := []dialect{{name: "sqlite", open: openSQLite}}
	if os.Getenv("KILASFLOW_TEST_POSTGRES_DSN") != "" {
		dialects = append(dialects, dialect{name: "postgres", open: openPostgres})
	}
	return dialects
}

// openSQLite opens a private file database and migrates it.
func openSQLite(t *testing.T, prefix string) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"), TablePrefix: prefix,
	}, discardLogger())
	if err != nil {
		t.Fatalf("Open(sqlite) error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate(sqlite) error = %v", err)
	}
	return db
}

// openPostgres opens the shared server, makes sure the vector half of the
// schema exists, and migrates.
//
// The vector prerequisite is not optional and it is not obvious. A fresh
// pgvector/pgvector:pg17 container has the extension AVAILABLE but not
// INSTALLED, so migration 6 is skipped, no vector table exists, and every test
// that asserts the vector coverage passes vacuously — the trap the plan calls
// out. So: install the extension if this role can, re-arm migration 6 by NAME
// (never by number: the integrator renumbers on collision), and migrate again.
// When the extension cannot be installed the run continues loudly instead of
// failing, and the vector tables are seeded and asserted only where they exist.
func openPostgres(t *testing.T, prefix string) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "postgres", DSN: os.Getenv("KILASFLOW_TEST_POSTGRES_DSN"), TablePrefix: prefix,
	}, discardLogger())
	if err != nil {
		t.Fatalf("Open(postgres) error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Logf("pgvector cannot be installed on this server (%v); the vector tables stay absent and are covered only where they exist", err)
	} else if db.Migrator().HasTable("schema_migrations") {
		if err := db.Exec("DELETE FROM schema_migrations WHERE name = 'vector_store'").Error; err != nil {
			t.Fatalf("re-arm the vector migration: %v", err)
		}
	}
	if err := database.Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate(postgres) error = %v", err)
	}
	return db
}

// createTablePattern reads the tables one dialect's migrations define.
//
// The introspection queries below cannot be trusted on their own: the
// PostgreSQL server is shared with other packages, with prefix runs, and with
// whatever an interrupted run left behind, so "every table that has a tenant_id
// column" has to be narrowed to the tables this schema actually declares. The
// parser that does that inside internal/database is unexported, so this reads
// the same embedded files with a regexp.
var createTablePattern = regexp.MustCompile("(?i)CREATE TABLE(?:\\s+IF NOT EXISTS)?\\s+[`\"]?([A-Za-z0-9_]+)[`\"]?")

func migrationTables(t *testing.T, dialectName string) map[string]bool {
	t.Helper()
	files, err := fs.Glob(migrations.FS, dialectName+"/*.up.sql")
	if err != nil {
		t.Fatalf("glob the %s migrations: %v", dialectName, err)
	}
	if len(files) == 0 {
		t.Fatalf("no %s migrations are embedded in this build", dialectName)
	}
	tables := map[string]bool{}
	for _, file := range files {
		content, err := fs.ReadFile(migrations.FS, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range createTablePattern.FindAllStringSubmatch(string(content), -1) {
			tables[match[1]] = true
		}
	}
	return tables
}

// introspectTenantTables returns the live tables that carry a tenant_id column
// and are declared by this dialect's migrations, prefix included.
//
// Live names are returned rather than logical ones because the caller counts
// and deletes through them; the prefix is stripped only to match against the
// migrations, whose names are never prefixed.
func introspectTenantTables(t *testing.T, db *database.DB, prefix string) []string {
	t.Helper()
	var names []string
	switch db.Dialector.Name() {
	case "sqlite":
		if err := db.Raw(
			"SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p ON p.name = 'tenant_id' WHERE m.type = 'table'",
		).Scan(&names).Error; err != nil {
			t.Fatalf("introspect the sqlite schema: %v", err)
		}
	case "postgres":
		if err := db.Raw(
			"SELECT table_name FROM information_schema.columns WHERE column_name = 'tenant_id' AND table_schema = current_schema()",
		).Scan(&names).Error; err != nil {
			t.Fatalf("introspect the postgres schema: %v", err)
		}
	default:
		t.Fatalf("no tenant-table introspection exists for the %q dialect", db.Dialector.Name())
	}

	defined := migrationTables(t, db.Dialector.Name())
	live := make([]string, 0, len(names))
	for _, name := range names {
		logical, ok := strings.CutPrefix(name, prefix)
		if !ok || !defined[logical] {
			continue
		}
		live = append(live, name)
	}
	sort.Strings(live)
	return live
}

// rawTenantCount counts one tenant's rows in a live table with raw SQL. Never
// GORM: a soft-deleted workflow is invisible to a scoped read, and "the row is
// gone" is exactly the claim under test.
func rawTenantCount(t *testing.T, db *database.DB, table, tenantID string) int64 {
	t.Helper()
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ?", tenantID).Scan(&count).Error; err != nil {
		t.Fatalf("count %s rows for %s: %v", table, tenantID, err)
	}
	return count
}

// rawIDCount counts rows some key column names, for the tenants table (whose
// tenant is itself) and for the like.
func rawIDCount(t *testing.T, db *database.DB, table, column, value string) int64 {
	t.Helper()
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", value).Scan(&count).Error; err != nil {
		t.Fatalf("count %s rows where %s = %s: %v", table, column, value, err)
	}
	return count
}

func tenantCounts(t *testing.T, db *database.DB, tables []string, tenantID string) map[string]int64 {
	t.Helper()
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		counts[table] = rawTenantCount(t, db, table, tenantID)
	}
	return counts
}

// randomTenantID mints a tenant id no other run and no other package can
// collide with, because the PostgreSQL server is shared.
func randomTenantID(prefix string) string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		panic("tenantpurge test: no entropy for a tenant id: " + err.Error())
	}
	return prefix + hex.EncodeToString(buffer)
}

// seedCatalog is the smallest catalogue the seeded documents compile against.
type seedCatalog map[string]workflow.NodeDefinition

func (c seedCatalog) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	definition, found := c[nodeType]
	if !found || definition.Version != version {
		return workflow.NodeDefinition{}, false
	}
	return definition, true
}

func (c seedCatalog) HasType(nodeType string) bool {
	_, found := c[nodeType]
	return found
}

func seedNodeCatalog() seedCatalog {
	main := workflow.Port{Name: "main", Kind: workflow.ConnectionMain}
	return seedCatalog{
		"kilasflow.manual":   {Type: "kilasflow.manual", Version: workflow.V(1), Outputs: []workflow.Port{main}},
		"kilasflow.webhook":  {Type: "kilasflow.webhook", Version: workflow.V(1), Outputs: []workflow.Port{main}},
		"kilasflow.schedule": {Type: "kilasflow.schedule", Version: workflow.V(1), Outputs: []workflow.Port{main}},
	}
}

// seedWebhooks and seedSchedules stand in for internal/webhook.Extract and
// internal/scheduler.Extract. They live here rather than being imported
// because the real extractors take a node registry this package must not need
// to build a tenant's rows.
func seedWebhooks(document workflow.Document) []repository.WebhookTrigger {
	triggers := make([]repository.WebhookTrigger, 0, len(document.Nodes))
	for _, node := range document.Nodes {
		if node.Type != "kilasflow.webhook" {
			continue
		}
		path, _ := node.Parameters["path"].(string)
		method, _ := node.Parameters["httpMethod"].(string)
		triggers = append(triggers, repository.WebhookTrigger{
			NodeID: node.ID, NodeType: node.Type, Method: method, Path: path,
		})
	}
	return triggers
}

func seedSchedules(document workflow.Document) []repository.ScheduleTrigger {
	triggers := make([]repository.ScheduleTrigger, 0, len(document.Nodes))
	for _, node := range document.Nodes {
		if node.Type != "kilasflow.schedule" {
			continue
		}
		cron, _ := node.Parameters["cron"].(string)
		triggers = append(triggers, repository.ScheduleTrigger{NodeID: node.ID, IntervalIndex: 0, Cron: cron})
	}
	return triggers
}

// seedNext stands in for scheduler.Next: the rows only have to exist for the
// purge, so the first run is deliberately not cron-parsed here.
func seedNext(_ string, after time.Time) (time.Time, error) {
	return after.Add(time.Hour), nil
}

func seedDocument(tenantID, workflowID, name string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID,
		Name:          name,
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "hook", Name: "Webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"path": "purge-" + tenantID, "httpMethod": "POST"}},
			{ID: "cron", Name: "Schedule", Type: "kilasflow.schedule", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"cron": "0 * * * *"}},
		},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}
}

// purgeEnv is one dialect's worth of the real infrastructure a tenant purge
// touches: the real stores, the real datastore engine, a real payload
// directory under t.TempDir(), and the real in-process session memory.
type purgeEnv struct {
	t           *testing.T
	db          *database.DB
	prefix      string
	vectorDocs  []string
	files       *binary.FileStore
	memory      *ai.BufferMemory
	auth        *repository.GORMAuthStore
	workflows   *repository.GORMWorkflowStore
	executions  *repository.GORMExecutionStore
	secrets     *repository.GORMCredentialStore
	engine      *datastore.Engine
	idempotency *repository.GORMIdempotencyStore
}

func newPurgeEnv(t *testing.T, db *database.DB, prefix string) *purgeEnv {
	t.Helper()
	files, err := binary.NewFileStore(filepath.Join(t.TempDir(), "payloads"), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	memory, err := ai.NewBufferMemory(ai.DefaultRetention(), nil)
	if err != nil {
		t.Fatalf("NewBufferMemory() error = %v", err)
	}
	key := make([]byte, credentials.KeySize)
	for index := range key {
		key[index] = byte(index + 7)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	engine, err := datastore.NewEngine(db, prefix)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return &purgeEnv{
		t: t, db: db, prefix: prefix,
		vectorDocs: existingVectorDocuments(t, db, prefix),
		files:      files,
		memory:     memory,
		auth:       repository.NewAuthStore(db.DB),
		workflows: repository.NewWorkflowStore(db.DB).
			WithWebhooks(seedWebhooks).
			WithSchedules(seedSchedules, seedNext),
		executions:  repository.NewExecutionStore(db.DB),
		secrets:     repository.NewCredentialStore(db.DB, cipher),
		engine:      engine,
		idempotency: repository.NewIdempotencyStore(db.DB),
	}
}

// existingVectorDocuments lists the document tables this database actually
// has. They are PostgreSQL-only and, on a server without pgvector, absent
// entirely.
func existingVectorDocuments(t *testing.T, db *database.DB, prefix string) []string {
	t.Helper()
	var tables []string
	for _, name := range []string{
		"vector_documents_384", "vector_documents_768", "vector_documents_1024", "vector_documents_1536",
	} {
		if db.Migrator().HasTable(prefix + name) {
			tables = append(tables, prefix+name)
		}
	}
	if !db.Migrator().HasTable(prefix + "vector_collections") {
		t.Fatalf("the vector catalogue is missing from this schema; %s/000006_vector_store should define it", db.Dialector.Name())
	}
	if len(tables) == 0 {
		t.Logf("this server carries no vector document tables: migration 000006 was skipped for a missing pgvector extension, or this is SQLite. The vector catalogue row is seeded and asserted either way")
	}
	return tables
}

// service builds the orchestrator over this environment's real collaborators.
// A nil stopper is honest for a test that does not care whether remote
// triggers were told.
func (env *purgeEnv) service(stopper tenantpurge.TriggerStopper) *tenantpurge.Service {
	env.t.Helper()
	service, err := tenantpurge.New(tenantpurge.Deps{
		Logger:     discardLogger(),
		Runs:       env.executions,
		Rows:       repository.NewTenantPurger(env.db.DB),
		Datastores: env.engine,
		Binaries:   env.files,
		Sessions:   env.memory,
		Triggers:   stopper,
	})
	if err != nil {
		env.t.Fatalf("tenantpurge.New() error = %v", err)
	}
	return service
}

func (env *purgeEnv) ident(table string) string {
	if env.db.Dialector.Name() == "sqlite" {
		return "`" + env.prefix + table + "`"
	}
	return `"` + env.prefix + table + `"`
}

// tenantSeed names what one seeded tenant owns, so a test can prove the
// survivor's values rather than only its row counts.
type tenantSeed struct {
	id                string
	workflowID        string
	removedWorkflowID string
	route             string
	executionID       string
	cell              string
	dsID              string
	dsTable           string
	dsRowID           int64
	binaryScope       binary.Scope
	binaryID          string
	binaryBody        string
	session           ai.SessionKey
}

// seedTenant writes one tenant across every tenant-scoped table through the
// public APIs, so the rows under test are the rows the product writes.
func (env *purgeEnv) seedTenant(id string) *tenantSeed {
	env.t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: id}
	seed := &tenantSeed{id: id, cell: "cell-" + id}

	if _, err := env.auth.EnsureTenant(ctx, id, "Tenant "+id); err != nil {
		env.t.Fatalf("EnsureTenant(%s) error = %v", id, err)
	}
	hash, err := auth.HashPassword("seed-password")
	if err != nil {
		env.t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := env.auth.CreateUser(ctx, tenant, "owner@"+id+".example", "Owner", hash); err != nil {
		env.t.Fatalf("CreateUser(%s) error = %v", id, err)
	}
	if _, _, err := env.auth.CreateAPIKey(ctx, tenant, "seed key"); err != nil {
		env.t.Fatalf("CreateAPIKey(%s) error = %v", id, err)
	}

	document := seedDocument(id, "wf_"+id, "Purge source")
	stored, err := env.workflows.SaveDraft(ctx, tenant, document)
	if err != nil {
		env.t.Fatalf("SaveDraft(%s) error = %v", id, err)
	}
	seed.workflowID = stored.ID
	if _, err := env.workflows.Activate(ctx, tenant, stored.ID, seedNodeCatalog()); err != nil {
		env.t.Fatalf("Activate(%s) error = %v", id, err)
	}
	bindings, err := env.workflows.WebhookRoutes(ctx, tenant, stored.ID)
	if err != nil {
		env.t.Fatalf("WebhookRoutes(%s) error = %v", id, err)
	}
	for _, binding := range bindings {
		if binding.NodeID == "hook" {
			seed.route = binding.Route
		}
	}
	if seed.route == "" {
		env.t.Fatalf("activation of %s bound no route for its webhook node", id)
	}

	// A workflow the tenant deleted before the tenant itself. workflowModel
	// soft-deletes, so a purge that only stamps deleted_at would leave the
	// customer's workflow name in the table.
	doomed, err := env.workflows.SaveDraft(ctx, tenant, seedDocument(id, "wf_"+id+"_removed", "Removed"))
	if err != nil {
		env.t.Fatalf("SaveDraft(removed %s) error = %v", id, err)
	}
	seed.removedWorkflowID = doomed.ID
	if err := env.workflows.Delete(ctx, tenant, doomed.ID); err != nil {
		env.t.Fatalf("Delete(removed %s) error = %v", id, err)
	}

	// The execution trace is queued through the store and traced by raw
	// inserts: the public node-run write needs a claimed lease, and ClaimNext
	// claims the globally oldest queued execution, which on the shared
	// PostgreSQL server belongs to whoever ran last.
	queued, err := env.executions.QueueManualLatest(ctx, tenant, stored.ID, seedNodeCatalog(), "", []byte(`{}`))
	if err != nil {
		env.t.Fatalf("QueueManualLatest(%s) error = %v", id, err)
	}
	seed.executionID = queued.ID
	env.insertNodeRun(seed)
	env.insertWait(seed)

	if _, _, err := env.workflows.ClaimDelivery(ctx, id, seed.route, "delivery-"+id, seed.executionID, repository.DefaultDeliveryWindow); err != nil {
		env.t.Fatalf("ClaimDelivery(%s) error = %v", id, err)
	}

	if _, err := env.secrets.Create(ctx, tenant, credentials.Record{
		Name: "openai", Type: "openAiApi", Fields: map[string]string{"apiKey": "seed-" + id},
	}); err != nil {
		env.t.Fatalf("Create(credential %s) error = %v", id, err)
	}
	if _, err := env.secrets.CreateSecretBinding(ctx, tenant, credentials.Binding{
		Name: "vault-" + id, Provider: "vault", Address: "https://vault.example",
		TokenEnv: "KILASFLOW_SEED_VAULT_TOKEN",
	}); err != nil {
		env.t.Fatalf("CreateSecretBinding(%s) error = %v", id, err)
	}

	store, err := env.engine.Create(ctx, id, "cells", []datastore.ColumnInput{{Name: "cell", Type: "string"}})
	if err != nil {
		env.t.Fatalf("Create(datastore %s) error = %v", id, err)
	}
	seed.dsID, seed.dsTable = store.ID, store.Table
	row, err := env.engine.Insert(ctx, id, store.ID, map[string]any{"cell": seed.cell})
	if err != nil {
		env.t.Fatalf("Insert(datastore %s) error = %v", id, err)
	}
	seed.dsRowID = row["id"].(int64)

	scope := binary.Scope{TenantID: id, ExecutionID: seed.executionID}
	seed.binaryScope, seed.binaryBody = scope, "payload-"+id
	reference, err := env.files.Put(scope, "note.txt", "text/plain", strings.NewReader(seed.binaryBody))
	if err != nil {
		env.t.Fatalf("Put(binary %s) error = %v", id, err)
	}
	seed.binaryID = reference.ID

	// One request-idempotency key, claimed the way a retried request claims it.
	// The key's recorded outcome would name the execution above, so it is the
	// tenant's data and the purge must take it.
	if _, err := env.idempotency.Claim(ctx, repository.IdempotencyClaim{
		Tenant: tenant, Key: "seed-" + id, Operation: "run-workflow",
		RequestHash: strings.Repeat("ab", 32), Token: "token-" + id,
		Now: time.Now().UTC(), InFlightUntil: time.Now().UTC().Add(time.Minute),
	}); err != nil {
		env.t.Fatalf("Claim(idempotency %s) error = %v", id, err)
	}

	seed.session = ai.SessionKey{TenantID: id, WorkflowID: stored.ID, SessionID: "session-" + id}
	if err := env.memory.Append(ctx, seed.session, []ai.Message{{Role: ai.RoleUser, Content: "hello " + id}}); err != nil {
		env.t.Fatalf("Append(session %s) error = %v", id, err)
	}

	env.seedVectors(seed)
	return seed
}

// insertNodeRun writes the trace row whose output carries the tenant's cell
// value. It is a raw insert so no lease is needed, and the blob is passed as
// bytes so both dialects store it in their own binary column.
func (env *purgeEnv) insertNodeRun(seed *tenantSeed) {
	env.t.Helper()
	now := time.Now().UTC()
	statement := "INSERT INTO " + env.ident("execution_node_runs") +
		" (id, tenant_id, execution_id, node_id, run_index, attempt, sequence, status, input, output, error, started_at)" +
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	err := env.db.Exec(statement, "run-"+seed.id, seed.id, seed.executionID, "set", 0, 1, 1, "succeeded",
		[]byte(`{}`), []byte(`{"cell":"`+seed.cell+`"}`), []byte(`null`), now).Error
	if err != nil {
		env.t.Fatalf("insert the node run of %s: %v", seed.id, err)
	}
}

// insertWait writes the suspension that would make an executions-first purge
// fail on execution_waits.execution_id's RESTRICT foreign key.
func (env *purgeEnv) insertWait(seed *tenantSeed) {
	env.t.Helper()
	now := time.Now().UTC()
	statement := "INSERT INTO " + env.ident("execution_waits") +
		" (tenant_id, execution_id, workflow_id, node_id, mode, token_hash, resume_token, checkpoint, run_count, expires_at, outcome, created_at, updated_at)" +
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	err := env.db.Exec(statement, seed.id, seed.executionID, seed.workflowID, "approve", "approval",
		"hash-"+seed.id, "token-"+seed.id, []byte(`{"checkpoint":true}`), 1, now.Add(time.Hour), "", now, now).Error
	if err != nil {
		env.t.Fatalf("insert the wait of %s: %v", seed.id, err)
	}
}

// seedVectors writes the catalogue row every dialect carries and one document
// per document table this server has. A fresh pgvector container has none, so
// the loop is empty there and the test says so rather than pretending.
func (env *purgeEnv) seedVectors(seed *tenantSeed) {
	env.t.Helper()
	now := time.Now().UTC()
	statement := "INSERT INTO " + env.ident("vector_collections") +
		" (id, tenant_id, name, dimension, distance, index_type, table_name, created_at, updated_at)" +
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"
	if err := env.db.Exec(statement, "vc-"+seed.id, seed.id, "seed", 384, "cosine", "hnsw", "", now, now).Error; err != nil {
		env.t.Fatalf("seed the vector collection of %s: %v", seed.id, err)
	}
	for _, table := range env.vectorDocs {
		dimension := strings.TrimPrefix(strings.TrimPrefix(table, env.prefix), "vector_documents_")
		statement := "INSERT INTO " + env.ident(table) +
			" (id, tenant_id, collection_id, content, metadata, embedding)" +
			" VALUES (?, ?, ?, ?, ?::jsonb, array_fill(0.1::real, ARRAY[" + dimension + "])::vector)"
		if err := env.db.Exec(statement, "vd-"+seed.id+"-"+dimension, seed.id, "vc-"+seed.id, seed.cell, "{}").Error; err != nil {
			env.t.Fatalf("seed a vector document in %s: %v", table, err)
		}
	}
}

// cellRowCount counts node-run rows anywhere whose stored output still carries
// a cell value, decoded in SQL because a blob cannot be compared to text
// directly on either dialect.
func cellRowCount(t *testing.T, db *database.DB, table, cell string) int64 {
	t.Helper()
	statement := "SELECT COUNT(*) FROM " + table + " WHERE CAST(output AS TEXT) LIKE ?"
	if db.Dialector.Name() == "postgres" {
		// encode(..., 'escape') renders any byte, so a payload that is not
		// valid UTF-8 cannot turn this assertion into an error.
		statement = "SELECT COUNT(*) FROM " + table + " WHERE encode(output, 'escape') LIKE ?"
	}
	var count int64
	if err := db.Raw(statement, "%"+cell+"%").Scan(&count).Error; err != nil {
		t.Fatalf("count rows still holding %q: %v", cell, err)
	}
	return count
}
