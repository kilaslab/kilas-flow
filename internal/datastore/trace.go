package datastore

import (
	"encoding/json"
	"math"
)

// NodeType is the workflow node type whose runs carry datastore cells.
//
// The engine's trace path consults it once, before the node-run write and
// the live publish, and replaces the output with the summary below. A
// datastore node built under another type keeps its cells in the trace and
// redaction keeps rewriting them there, so a renamed type must update this
// constant rather than shipping beside it.
const NodeType = "kilasflow.datastore"

// maxTraceIDs bounds how many row identifiers one trace summary carries. The
// count is always exact; only the identifier list is cut, and the cut is
// marked, so a ReturnAll listing cannot bloat a node-run row without bound.
const maxTraceIDs = 100

// traceEnvelope is the whole durable and live projection of a datastore node
// run: row counts and row identifiers, never cell contents.
//
// Every key is chosen to survive execution.Redact byte-identical: none
// normalises onto a sensitive key and no object carries a name/value pair,
// so Redact is a fixed point over this shape and the trace can never hold a
// "[redacted]" marker where a cell used to be.
type traceEnvelope struct {
	Datastore traceBody `json:"datastore"`
}

// traceBody holds the count, the bounded identifiers, and whether the list
// was cut. Truncated is omitted when false so the common small result reads
// clean. Fields sit in alphabetical order on purpose: execution.Redact
// round-trips through a map, and encoding/json emits map keys sorted, so
// alphabetical fields make Redact a byte-exact fixed point over this shape
// rather than merely a semantic one. Keep any new field in sorted position
// or the stability test fails.
type traceBody struct {
	IDs       []int64 `json:"ids"`
	Rows      int64   `json:"rows"`
	Truncated bool    `json:"truncated,omitempty"`
}

// invalid, and unrecognised payloads: the projector never destroys what it
// does not understand. A datastore output is walked structurally — item
// streams wrap rows under "json", lists hold them flat, a single get holds
// one object — and every object carrying a numeric "id" counts as one row.
// The system id is reserved and can never be a user column, so no cell can
// be mistaken for an identifier and no identifier carries cell content.
//
// When no row-like object is found the output passes through unchanged: an
// operation summary the projector was not taught must stay readable, not
// become an empty envelope.
func ProjectTrace(nodeType string, output json.RawMessage) json.RawMessage {
	if nodeType != NodeType || len(output) == 0 || !json.Valid(output) {
		return output
	}
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		return output
	}
	var ids []int64
	var count int64
	collectRowIDs(value, &ids, &count)
	if count == 0 {
		return output
	}
	summary, err := json.Marshal(traceEnvelope{Datastore: traceBody{
		Rows:      count,
		IDs:       ids,
		Truncated: count > int64(len(ids)),
	}})
	if err != nil {
		return output
	}
	return summary
}

// collectRowIDs counts every object with a numeric "id" and keeps the first
// maxTraceIDs identifiers in walk order. Walk order is document order for
// arrays and unspecified for objects, which is fine: the list is evidence
// for reconciliation, not a query result, and the count beside it is exact.
func collectRowIDs(value any, ids *[]int64, count *int64) {
	switch typed := value.(type) {
	case []any:
		for _, element := range typed {
			collectRowIDs(element, ids, count)
		}
	case map[string]any:
		if id, ok := rowIDOf(typed); ok {
			*count++
			if len(*ids) < maxTraceIDs {
				*ids = append(*ids, id)
			}
		}
		for _, element := range typed {
			collectRowIDs(element, ids, count)
		}
	}
}

// rowIDOf reports the system id of an object shaped like a datastore row: a
// numeric "id" holding an integer. A non-integer number or a string under
// that key is not a row the store wrote, so it is ignored rather than
// truncated into an identifier that names nothing.
func rowIDOf(object map[string]any) (int64, bool) {
	raw, found := object["id"]
	if !found {
		return 0, false
	}
	number, ok := raw.(float64)
	if !ok || math.Trunc(number) != number {
		return 0, false
	}
	return int64(number), true
}
