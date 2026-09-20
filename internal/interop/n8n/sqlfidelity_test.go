package n8n_test

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/sqlbuild"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// sqlFixture is a one-node n8n workflow carrying the given Postgres parameters.
func sqlNodeFixture(parameters string) string {
	return `{
	  "name": "SQL",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Postgres","type":"n8n-nodes-base.postgres","typeVersion":2.5,"position":[220,0],
	     "parameters":` + parameters + `}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Postgres","type":"main","index":0}]]}}
	}`
}

// builtFrom imports one n8n node and returns the SQL it would actually run.
//
// One hop, not a round trip. The importer and the exporter map the condition
// vocabulary through tables that are each other's inverse, so an inversion
// between them is symmetric: it round-trips perfectly and runs the opposite
// query. The only assertion that can see it starts at n8n's JSON and ends at
// the statement, with n8n's own SQL as the expected value.
func builtFrom(t *testing.T, parameters string) (string, int) {
	t.Helper()
	result := importFixture(t, sqlNodeFixture(parameters))
	node := nodeByName(result.Document, "Postgres")
	if node.Name == "" {
		t.Fatal("the fixture did not import a Postgres node")
	}
	statement, err := nodes.BuildSQLStatementForTest(sqlbuild.Postgres, node.Parameters)
	if err != nil {
		t.Fatalf("building the imported node's SQL: %v", err)
	}
	return statement.SQL, len(statement.Parameters)
}

// Every n8n condition builds the SQL n8n would have built, and never its
// opposite.
//
// The expected fragments come from n8n's own helpers/utils.ts, not from either
// of this package's two maps — which is what makes the test able to fail when
// they are swapped. It found a real inversion once: "IS NULL" mapped to the
// operator meaning *present*, so an imported `WHERE col IS NULL` built
// `WHERE col IS NOT NULL`.
func TestAnImportedConditionBuildsTheSQLN8NWouldHaveRun(t *testing.T) {
	t.Parallel()

	rows := map[string]struct {
		// fragment is what the built SQL must contain.
		fragment string
		// opposite is the fragment of this condition's semantic inverse, which
		// it must not contain — the assertion that catches a swapped map.
		opposite string
		// binds is how many values the condition binds.
		binds int
	}{
		// The builder spells inequality the ANSI way, <>, where n8n writes !=.
		// The fragment is this builder's spelling because the assertion is
		// about which comparison runs, and the two spellings are the same
		// comparison — what must never happen is = appearing here.
		"equal":       {fragment: `"tier" = $1`, opposite: `"tier" <> $1`, binds: 1},
		"!=":          {fragment: `"tier" <> $1`, opposite: `"tier" = $1`, binds: 1},
		"LIKE":        {fragment: `"tier" LIKE $1`, opposite: `"tier" ILIKE $1`, binds: 1},
		"ILIKE":       {fragment: `"tier" ILIKE $1`, opposite: `"tier" LIKE $1`, binds: 1},
		">":           {fragment: `"tier" > $1`, opposite: `"tier" <= $1`, binds: 1},
		">=":          {fragment: `"tier" >= $1`, opposite: `"tier" < $1`, binds: 1},
		"<":           {fragment: `"tier" < $1`, opposite: `"tier" >= $1`, binds: 1},
		"<=":          {fragment: `"tier" <= $1`, opposite: `"tier" > $1`, binds: 1},
		"IS NULL":     {fragment: `"tier" IS NULL`, opposite: `"tier" IS NOT NULL`, binds: 0},
		"IS NOT NULL": {fragment: `"tier" IS NOT NULL`, opposite: `"tier" IS NULL`, binds: 0},
	}

	// Completeness, so an eleventh condition cannot be added untested.
	declared := n8n.N8NConditionNamesForTest()
	covered := make([]string, 0, len(rows))
	for name := range rows {
		covered = append(covered, name)
	}
	sort.Strings(covered)
	if strings.Join(declared, ",") != strings.Join(covered, ",") {
		t.Fatalf("the importer maps %v but this table covers %v", declared, covered)
	}

	for condition, want := range rows {
		t.Run(condition, func(t *testing.T) {
			sql, binds := builtFrom(t, `{"operation":"select",
			  "schema":{"__rl":true,"mode":"list","value":"public"},
			  "table":{"__rl":true,"mode":"list","value":"customers"},
			  "returnAll":true,
			  "where":{"values":[{"column":"tier","condition":"`+condition+`","value":"gold"}]}}`)

			if !strings.Contains(sql, want.fragment) {
				t.Errorf("SQL = %q, want it to contain %q", sql, want.fragment)
			}
			// IS NULL is a substring of IS NOT NULL's inverse only in one
			// direction, so the check is on the fragment either way rather
			// than on a naive Contains of the opposite.
			if want.opposite != want.fragment && strings.Contains(sql, want.opposite) {
				t.Errorf("SQL = %q, want it not to contain the opposite %q", sql, want.opposite)
			}
			if binds != want.binds {
				t.Errorf("bound %d values, want %d — a null test binds nothing", binds, want.binds)
			}
		})
	}
}

// The delete command builds the verb n8n would have run, including when the
// n8n node never stored one.
//
// The absent row is the whole point. n8n's default is "truncate" and this
// node's is "delete", so letting the absence cross the boundary meant an
// "empty this table" arrived as a row delete — and, exporting, a row delete
// left as n8n's default became a TRUNCATE nobody asked for.
func TestAnImportedDeleteCommandBuildsTheVerbN8NWouldHaveRun(t *testing.T) {
	t.Parallel()

	for name, row := range map[string]struct{ stored, verb string }{
		"absent":   {stored: "", verb: "TRUNCATE TABLE"},
		"truncate": {stored: `"deleteCommand":"truncate",`, verb: "TRUNCATE TABLE"},
		"delete":   {stored: `"deleteCommand":"delete",`, verb: "DELETE FROM"},
		"drop":     {stored: `"deleteCommand":"drop",`, verb: "DROP TABLE"},
	} {
		t.Run(name, func(t *testing.T) {
			sql, _ := builtFrom(t, `{"operation":"deleteTable",`+row.stored+`
			  "schema":{"__rl":true,"mode":"list","value":"public"},
			  "table":{"__rl":true,"mode":"list","value":"customers"},
			  "where":{"values":[{"column":"tier","condition":"equal","value":"gold"}]}}`)

			if !strings.HasPrefix(sql, row.verb) {
				t.Fatalf("SQL = %q, want it to start with %q", sql, row.verb)
			}
			for _, other := range []string{"TRUNCATE TABLE", "DELETE FROM", "DROP TABLE"} {
				if other != row.verb && strings.Contains(sql, other) {
					t.Errorf("SQL = %q, want no trace of %q", sql, other)
				}
			}
		})
	}
}

// A sort rule builds the ORDER BY it names, and no rule builds none.
//
// Dropped silently until now: n8n's select carries a sort collection, nothing
// read it, and the builder was handed nil — so an imported
// "ORDER BY created_at DESC LIMIT 50" returned an arbitrary fifty rows. That
// looks like data rather than like a defect, which is why it needs a test
// rather than a reviewer.
func TestAnImportedSortBuildsTheOrderItNames(t *testing.T) {
	t.Parallel()

	t.Run("carried", func(t *testing.T) {
		sql, _ := builtFrom(t, `{"operation":"select","returnAll":true,
		  "schema":{"__rl":true,"mode":"list","value":"public"},
		  "table":{"__rl":true,"mode":"list","value":"customers"},
		  "sort":{"values":[{"column":"created_at","direction":"DESC"},{"column":"tier","direction":"ASC"}]}}`)

		if want := `ORDER BY "created_at" DESC, "tier" ASC`; !strings.Contains(sql, want) {
			t.Errorf("SQL = %q, want it to contain %q", sql, want)
		}
	})

	t.Run("absent", func(t *testing.T) {
		sql, _ := builtFrom(t, `{"operation":"select","returnAll":true,
		  "schema":{"__rl":true,"mode":"list","value":"public"},
		  "table":{"__rl":true,"mode":"list","value":"customers"}}`)

		if strings.Contains(sql, "ORDER BY") {
			t.Errorf("SQL = %q, want no ORDER BY where the node named no rule", sql)
		}
	})
}

// n8n's query replacements arrive as bound values, asserted element by element.
//
// On the parameters rather than on the node's stored field, because what
// matters is what reaches the database: a wrong JSON encoding would produce a
// plausible-looking stored string and bind the wrong thing.
func TestImportedQueryReplacementsArriveBound(t *testing.T) {
	t.Parallel()

	result := importFixture(t, sqlNodeFixture(`{"operation":"executeQuery",
	  "query":"SELECT * FROM customers WHERE tier = $1 AND region = $2",
	  "options":{"queryReplacement":"gold, europe"}}`))
	node := nodeByName(result.Document, "Postgres")

	bound, ok := node.Parameters["queryParameters"].([]any)
	if !ok {
		t.Fatalf("queryParameters = %#v, want a bound list", node.Parameters["queryParameters"])
	}
	if len(bound) != 2 || bound[0] != "gold" || bound[1] != "europe" {
		t.Fatalf("bound = %#v, want the two values with their surrounding space trimmed", bound)
	}
	// Not left in the options as well: n8n binds from the option and this node
	// binds from the field, and two copies of one thing disagree the moment
	// somebody edits either.
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if _, kept := options["queryReplacement"]; kept {
			t.Error("the replacement was translated and also kept in the options")
		}
	}
	// n8n's own split, reproduced exactly: an empty entry is dropped before
	// trimming and a whitespace-only one is not, which is observable.
	for stored, want := range map[string][]any{
		"a,,b":       {"a", "b"},
		"a, ,b":      {"a", "", "b"},
		"gold":       {"gold"},
		" gold , 7 ": {"gold", "7"},
	} {
		result := importFixture(t, sqlNodeFixture(`{"operation":"executeQuery","query":"SELECT 1",
		  "options":{"queryReplacement":"`+stored+`"}}`))
		got, _ := nodeByName(result.Document, "Postgres").Parameters["queryParameters"].([]any)
		if len(got) != len(want) {
			t.Errorf("%q bound %#v, want %#v", stored, got, want)
			continue
		}
		for index := range want {
			if got[index] != want[index] {
				t.Errorf("%q bound %#v, want %#v", stored, got, want)
				break
			}
		}
	}

	// The split is n8n's own, so it is reported rather than hidden — a reader
	// of the import report should learn that a comma is a separator there.
	named := false
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Field, "queryReplacement") {
			named = true
		}
	}
	if !named {
		t.Error("splitting the replacement string on the comma was not reported")
	}
}

// The export writes the version whose n8n behaviour matches this server's, and
// carries an imported node's own version back out.
//
// Exact numbers, not "not 2.4". n8n's getNodeType is an exact map lookup — see
// packages/workflow/src/versioned-node-type.ts — so a typeVersion n8n does not
// publish makes it throw when the file is opened, and one it does publish but
// that gates different behaviour changes the workflow's results without
// changing anything visible.
func TestTheSQLNodesExportAtTheVersionTheyMean(t *testing.T) {
	t.Parallel()

	for name, row := range map[string]struct {
		n8nType   string
		source    float64
		authored  float64
		published []float64
	}{
		"postgres": {n8nType: "n8n-nodes-base.postgres", source: 2.5, authored: 2.7,
			published: []float64{1, 2, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7}},
		"mysql": {n8nType: "n8n-nodes-base.mySql", source: 2.3, authored: 2.5,
			published: []float64{1, 2, 2.1, 2.2, 2.3, 2.4, 2.5}},
	} {
		t.Run(name, func(t *testing.T) {
			// A node that came from n8n goes back at the version it came in at.
			fixture := `{
			  "name": "Version",
			  "nodes": [
			    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
			    {"id":"b","name":"DB","type":"` + row.n8nType + `","typeVersion":` +
				strconv.FormatFloat(row.source, 'f', -1, 64) + `,"position":[220,0],
			     "parameters":{"operation":"executeQuery","query":"SELECT 1"}}
			  ],
			  "connections": {"Manual": {"main": [[{"node":"DB","type":"main","index":0}]]}}
			}`
			exported := exportFixture(t, importFixture(t, fixture).Document)
			if got := exportedVersion(t, exported, "DB"); got != row.source {
				t.Errorf("an imported node exported at %v, want the %v it arrived at", got, row.source)
			}

			// A node authored here gets the pin instead, since it has no n8n
			// version of its own to preserve.
			document := importFixture(t, fixture).Document
			for index := range document.Nodes {
				if document.Nodes[index].Name == "DB" {
					document.Nodes[index].TypeVersion = workflow.V(2)
				}
			}
			if got := exportedVersion(t, exportFixture(t, document), "DB"); got != row.authored {
				t.Errorf("a node authored here exported at %v, want the pin %v", got, row.authored)
			}

			// And whatever it writes, n8n has to publish it.
			for _, version := range []float64{row.source, row.authored} {
				if !contains(row.published, version) {
					t.Errorf("%v is not a version n8n registers, so n8n would refuse to open the file", version)
				}
			}
		})
	}
}

// Exporting an imported workflow twice produces the same document.
//
// The second export is the one that matters: it runs over a document that has
// already been through both converters, so any key one side writes and the
// other does not read shows up as a difference. Only the document is compared —
// the diagnostics legitimately differ, because the second pass has no
// credentials to report.
func TestExportingASQLWorkflowTwiceIsIdempotent(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Idempotent",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Postgres","type":"n8n-nodes-base.postgres","typeVersion":2.5,"position":[220,0],
	     "parameters":{"operation":"select",
	                   "schema":{"__rl":true,"mode":"list","value":"public"},
	                   "table":{"__rl":true,"mode":"list","value":"customers"},
	                   "returnAll":false,"limit":25,"combineConditions":"OR",
	                   "sort":{"values":[{"column":"created_at","direction":"DESC"}]},
	                   "where":{"values":[{"column":"tier","condition":"equal","value":"gold"}]},
	                   "options":{"queryBatching":"transaction","largeNumbersOutput":"numbers"}}},
	    {"id":"c","name":"MySQL","type":"n8n-nodes-base.mySql","typeVersion":2.4,"position":[440,0],
	     "parameters":{"operation":"deleteTable","deleteCommand":"truncate",
	                   "table":{"__rl":true,"mode":"list","value":"audit"},
	                   "options":{}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Postgres","type":"main","index":0}]]},
	                  "Postgres": {"main": [[{"node":"MySQL","type":"main","index":0}]]}}
	}`

	first := exportFixture(t, importFixture(t, fixture).Document)
	encodedFirst, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal the first export: %v", err)
	}
	second := exportFixture(t, importFixture(t, string(encodedFirst)).Document)
	encodedSecond, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal the second export: %v", err)
	}
	if !bytes.Equal(encodedFirst, encodedSecond) {
		t.Errorf("the second export differs from the first:\nfirst  = %s\nsecond = %s", encodedFirst, encodedSecond)
	}
}

func exportFixture(t *testing.T, document workflow.Document) n8n.Document {
	t.Helper()
	result, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	return result.Document
}

func exportedVersion(t *testing.T, document n8n.Document, name string) float64 {
	t.Helper()
	for _, node := range document.Nodes {
		if node.Name == name {
			return node.TypeVersion
		}
	}
	t.Fatalf("the export has no node named %q", name)
	return 0
}

func contains(values []float64, want float64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// A node's error handling survives the journey back out.
//
// One hop from n8n's JSON to the exported JSON, not a round trip. An
// export-import-export idempotence check passes when both exports are equally
// wrong, and that is exactly what this defect was — the fields were absent
// from both, so the two agreed perfectly while losing the setting.
//
// The asymmetry that made it worth finding: a node with no n8n equivalent
// round-tripped faithfully, because its capsule kept everything, while a node
// this server supports came back having silently lost its retry policy.
func TestErrorHandlingSurvivesTheJourneyBackToN8N(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Retries",
	  "settings": {"timezone": "Asia/Jakarta"},
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Fetch","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[220,0],
	     "parameters":{"url":"https://example.test"},
	     "continueOnFail":true,"retryOnFail":true,"maxTries":3,"waitBetweenTries":250}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Fetch","type":"main","index":0}]]}}
	}`

	exported := exportFixture(t, importFixture(t, fixture).Document)

	var fetch n8n.Node
	for _, node := range exported.Nodes {
		if node.Name == "Fetch" {
			fetch = node
		}
	}
	if fetch.Name == "" {
		t.Fatal("the export has no node named Fetch")
	}
	if !fetch.ContinueOnFail {
		t.Error("continueOnFail was lost, so the node fails the whole run on its first error")
	}
	if !fetch.RetryOnFail {
		t.Error("retryOnFail was lost")
	}
	if fetch.MaxTries != 3 {
		t.Errorf("maxTries = %v, want 3", fetch.MaxTries)
	}
	if fetch.WaitBetweenTries != 250 {
		t.Errorf("waitBetweenTries = %v, want 250", fetch.WaitBetweenTries)
	}

	// And the workflow's timezone, which import carries because a schedule
	// without it runs at the wrong hour every day.
	if zone, _ := exported.Settings["timezone"].(string); zone != "Asia/Jakarta" {
		t.Errorf("settings.timezone = %#v, want the zone the workflow arrived with", exported.Settings["timezone"])
	}
}
