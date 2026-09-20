package datastore

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/execution"
)

// Unrecognised shapes pass through byte-identical: the projector never
// destroys what it does not understand, and other node types are untouched.
func TestProjectTraceLeavesForeignShapesAlone(t *testing.T) {
	cases := map[string]json.RawMessage{
		"other node type": json.RawMessage(`[[{"json":{"api_key":"live"}}]]`),
		"empty":           json.RawMessage(``),
		"null":            json.RawMessage(`null`),
		"invalid":         json.RawMessage(`[[{`),
		"no rows":         json.RawMessage(`{"deleted":2}`),
		"scalar":          json.RawMessage(`"just a string"`),
	}
	for name, output := range cases {
		nodeType := NodeType
		if name == "other node type" {
			nodeType = "kilasflow.set"
		}
		if got := ProjectTrace(nodeType, output); string(got) != string(output) {
			t.Errorf("%s: ProjectTrace = %s, want passthrough %s", name, got, output)
		}
	}
}

// A datastore item stream carrying the two hostile shapes — a column named
// api_key and a name/value pair naming cookie — projects to counts and ids.
// No cell reaches the trace, so redaction has nothing to rewrite and the
// summary is byte-identical through it.
func TestProjectTraceSummarizesHostileCells(t *testing.T) {
	output := json.RawMessage(`[[` +
		`{"json":{"id":1,"api_key":"live-secret","title":"first"}},` +
		`{"json":{"id":2,"name":"cookie","value":"chocolate chip","password":"hunter2"}}` +
		`]]`)
	got := ProjectTrace(NodeType, output)

	var envelope struct {
		Datastore struct {
			Rows      int64   `json:"rows"`
			IDs       []int64 `json:"ids"`
			Truncated bool    `json:"truncated"`
		} `json:"datastore"`
	}
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatalf("decode summary %s: %v", got, err)
	}
	if envelope.Datastore.Rows != 2 {
		t.Errorf("rows = %d, want 2", envelope.Datastore.Rows)
	}
	if len(envelope.Datastore.IDs) != 2 || envelope.Datastore.IDs[0] != 1 || envelope.Datastore.IDs[1] != 2 {
		t.Errorf("ids = %v, want [1 2]", envelope.Datastore.IDs)
	}
	if envelope.Datastore.Truncated {
		t.Errorf("truncated = true, want false for a small result")
	}
	for _, leaked := range []string{"live-secret", "chocolate chip", "hunter2", "first", execution.RedactedValue} {
		if strings.Contains(string(got), leaked) {
			t.Errorf("summary %s contains %q", got, leaked)
		}
	}
	if stable := execution.Redact(got); string(stable) != string(got) {
		t.Errorf("Redact(summary) = %s, want byte-identical %s", stable, got)
	}
}

// Flat row arrays and single row objects summarise the same way: the node
// may return any of these shapes and the trace still holds no cells.
func TestProjectTraceHandlesFlatShapes(t *testing.T) {
	single := ProjectTrace(NodeType, json.RawMessage(`{"id":7,"api_key":"x"}`))
	if !strings.Contains(string(single), `"rows":1`) || !strings.Contains(string(single), `"ids":[7]`) {
		t.Errorf("single row summary = %s, want rows 1 ids [7]", single)
	}
	flat := ProjectTrace(NodeType, json.RawMessage(`[{"id":3},{"id":4}]`))
	if !strings.Contains(string(flat), `"rows":2`) || !strings.Contains(string(flat), `"ids":[3,4]`) {
		t.Errorf("flat summary = %s, want rows 2 ids [3 4]", flat)
	}
}

// Only objects with an integer numeric id count. A string id or a
// fractional one is not a row the store wrote, so it is ignored rather than
// truncated into an identifier that names nothing.
func TestProjectTraceIgnoresNonRowIDs(t *testing.T) {
	output := json.RawMessage(`[[{"json":{"id":"row-1"}},{"json":{"id":1.5}}]]`)
	if got := ProjectTrace(NodeType, output); string(got) != string(output) {
		t.Errorf("ProjectTrace = %s, want passthrough %s", got, output)
	}
}

// The identifier list is bounded while the count stays exact, so a ReturnAll
// listing cannot bloat a node-run row without bound.
func TestProjectTraceTruncatesLongIDLists(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`[`)
	for i := 1; i <= 150; i++ {
		if i > 1 {
			builder.WriteString(`,`)
		}
		builder.WriteString(`{"json":{"id":` + itoa(i) + `}}`)
	}
	builder.WriteString(`]`)
	got := ProjectTrace(NodeType, json.RawMessage(builder.String()))
	var envelope struct {
		Datastore struct {
			Rows      int64   `json:"rows"`
			IDs       []int64 `json:"ids"`
			Truncated bool    `json:"truncated"`
		} `json:"datastore"`
	}
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if envelope.Datastore.Rows != 150 {
		t.Errorf("rows = %d, want the exact 150", envelope.Datastore.Rows)
	}
	if len(envelope.Datastore.IDs) != maxTraceIDs {
		t.Errorf("ids = %d entries, want the bounded %d", len(envelope.Datastore.IDs), maxTraceIDs)
	}
	if !envelope.Datastore.Truncated {
		t.Error("truncated = false, want true when the list is cut")
	}
}

// The store itself never redacts: a value stored under a sensitive-named
// column or beside a sensitive name reads back byte-identical through every
// row operation. Redaction lives at the trace boundary only.
func TestRowStorePreservesHostileValues(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			ds, err := eng.Create(ctx, "tenant-1", "kv", []ColumnInput{
				{Name: "api_key", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "value", Type: "string"},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			inserted, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{
				"api_key": "live-secret",
				"name":    "cookie",
				"value":   "chocolate chip",
			})
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			for _, read := range []Row{inserted} {
				if read["api_key"] != "live-secret" || read["name"] != "cookie" || read["value"] != "chocolate chip" {
					t.Fatalf("round trip = %v, want the exact cells", read)
				}
			}
			got, err := eng.Get(ctx, "tenant-1", ds.ID, inserted["id"].(int64))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got["api_key"] != "live-secret" || got["value"] != "chocolate chip" {
				t.Errorf("Get = %v, want the exact cells", got)
			}
			updated, err := eng.Update(ctx, "tenant-1", ds.ID,
				&Filter{Type: "and", Conditions: []FilterCondition{{Column: "name", Condition: CondEq, Value: "cookie"}}},
				map[string]any{"value": "oatmeal raisin"}, false)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if len(updated.Rows) != 1 || updated.Rows[0]["value"] != "oatmeal raisin" || updated.Rows[0]["api_key"] != "live-secret" {
				t.Errorf("Update rows = %v, want the exact cells", updated.Rows)
			}
		})
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
