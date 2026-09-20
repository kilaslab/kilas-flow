package engine

import (
	"encoding/json"

	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// projectTrace maps a marshalled node output to its durable trace form.
//
// Only the datastore node type is projected, to row counts and row
// identifiers rather than cell contents: a cell named `api_key` or shaped
// `{"name": "cookie", "value": ...}` would otherwise meet execution.Redact
// at the node-run write and the live publish and be stored as "[redacted]".
// Projection happens before both, so redaction never sees a cell and the
// trace can hold neither a leaked value nor a redaction marker.
//
// The projection covers the node's own output only, never its input. An
// input echoes whatever the upstream nodes produced — including datastore
// cells another node read into `$json` — and summarising it would destroy
// the debuggability of every downstream node. A downstream trace that
// repeats a sensitive-named cell is still redacted there; that loss is
// accepted and documented, not travelled with provenance. The live rows
// themselves always reach the next node in memory, untouched by either
// boundary.
func projectTrace(nodeType string, output json.RawMessage) json.RawMessage {
	return datastore.ProjectTrace(nodeType, output)
}
