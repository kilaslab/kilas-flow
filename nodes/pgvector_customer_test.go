package nodes_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

type credentialMap map[string]engine.Credential

func (creds credentialMap) ResolveCredential(_ context.Context, id string) (engine.Credential, error) {
	credential, found := creds[id]
	if !found {
		return engine.Credential{}, fmt.Errorf("credential %q is not stored", id)
	}
	return credential, nil
}

func TestCustomerPGVectorErrorNamesTheOperatorSQL(t *testing.T) {
	t.Parallel()

	err := nodes.CustomerPGVectorErrorForTest("documents", fmt.Errorf(`pq: relation "documents" does not exist`))
	if err == nil || !strings.Contains(err.Error(), "CREATE EXTENSION IF NOT EXISTS vector") || !strings.Contains(err.Error(), "CREATE TABLE documents") {
		t.Fatalf("error = %v, want the operator SQL and never an auto-migrate", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "this workflow will run create") {
		t.Fatal("the tenant path must not claim it will run DDL")
	}
}

func TestVectorStorePGVectorRefusesAnUnlistedPrivateHost(t *testing.T) {
	t.Parallel()

	executor := nodes.NewVectorStorePGVectorExecutor(nil, sqlnode.Guard{Policy: safehttp.DefaultPolicy()})
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "vs", Name: "PGVector", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"mode": "insert", "tableName": "documents"},
		Credentials: map[string]string{"postgres": "cred-pg"},
	}, workflow.NodeInput{
		"document": {{JSON: map[string]any{"pageContent": "hello", "embedding": []any{0.1, 0.2, 0.3, 0.4}}}},
	}, engine.Request{Credentials: credentialMap{
		"cred-pg": {
			ID: "cred-pg", Name: "Customer Postgres", Type: "postgres",
			Fields: map[string]string{
				"host": "127.0.0.1", "port": "5432", "database": "other", "user": "u", "password": "p",
			},
		},
	}})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not allowed") && !strings.Contains(strings.ToLower(err.Error()), "private") && !strings.Contains(strings.ToLower(err.Error()), "loopback") {
		t.Fatalf("Execute() error = %v, want the private-host refusal", err)
	}
}

func TestVectorStorePGVectorInsertsAndSearchesOnLivePostgres(t *testing.T) {
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the customer PGVector coverage")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	table := "kf_rag_customer_docs"
	admin, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if _, err := admin.Exec(context.Background(), `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Skipf("CREATE EXTENSION vector: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `DROP TABLE IF EXISTS `+table); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `CREATE TABLE `+table+` (id TEXT PRIMARY KEY, text TEXT, metadata JSONB, embedding VECTOR(4))`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP TABLE IF EXISTS `+table)
	})

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[1,0,0,0],"index":0}]}`)
	}))
	t.Cleanup(provider.Close)
	target, _ := url.Parse(provider.URL)
	hostPort := parsed.Host + ":" + strconv.Itoa(int(parsed.Port))
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{hostPort, target.Host}

	embedder := nodes.NewEmbeddingsExecutor(policy, availableVectorStub{})
	executor := nodes.NewVectorStorePGVectorExecutor(embedder, sqlnode.Guard{Policy: policy})
	postgres := engine.Credential{
		ID: "cred-pg", Name: "Customer Postgres", Type: "postgres",
		Fields: map[string]string{
			"host": parsed.Host, "port": strconv.Itoa(int(parsed.Port)),
			"database": parsed.Database, "user": parsed.User,
			"password": parsed.Password, "sslMode": "disable",
		},
		AllowedDomains: []string{parsed.Host},
	}
	openai := engine.Credential{
		ID: "cred-emb", Name: "Embeddings", Type: "openAiApi",
		Fields:         map[string]string{"apiKey": "secret-key"},
		AllowedDomains: []string{httptestHost(provider.URL)},
	}
	request := engine.Request{
		Credentials: credentialMap{"cred-pg": postgres, "cred-emb": openai},
		Execution:   engine.ExecutionContext{TenantID: "tenant-rag"},
	}
	ir := workflow.IRNode{
		ID: "vs", Name: "PGVector", Type: nodes.VectorStorePGVectorNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"mode": "insert", "tableName": table, "collection": "handbook"},
		Credentials: map[string]string{"postgres": "cred-pg"},
	}
	inserted, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"document": {{JSON: map[string]any{"pageContent": "the handbook"}}},
		"embedding": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "embeddings", "model": "text-embedding-3-small",
			"baseUrl": provider.URL, "credentialId": "cred-emb",
		}}}},
	}, request)
	if err != nil {
		t.Fatalf("insert = %v", err)
	}
	if inserted[0][0].JSON["id"] == nil {
		t.Fatalf("insert item = %#v", inserted[0][0].JSON)
	}

	searchIR := ir
	searchIR.Parameters = map[string]any{"mode": "getMany", "tableName": table, "collection": "handbook", "query": "handbook", "topK": 1.0}
	found, err := executor.Execute(context.Background(), searchIR, workflow.NodeInput{
		"main": {{JSON: map[string]any{"text": "handbook"}}},
		"embedding": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "embeddings", "model": "text-embedding-3-small",
			"baseUrl": provider.URL, "credentialId": "cred-emb",
		}}}},
	}, request)
	if err != nil {
		t.Fatalf("search = %v", err)
	}
	matches, _ := found[0][0].JSON["matches"].([]any)
	if len(matches) == 0 {
		t.Fatalf("search matches = %#v", found[0][0].JSON)
	}
}
