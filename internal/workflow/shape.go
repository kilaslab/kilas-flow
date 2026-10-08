package workflow

import (
	"errors"
	"strings"
)

// ShapeRefusals returns Compile's refusals of how a document's graph is wired:
// its topology, other than a cycle, and a port that must be connected, is full,
// or takes the wrong node.
//
// It exists for the same reason IllegalCycles does: an n8n import reports what
// run and activate will refuse, and asks the compiler rather than restating its
// rules, so the report cannot disagree with the run. A cycle is IllegalCycles'
// to name by its path. An unknown node type and a node's configuration are
// left out: the importer reports both from what it translated. The catalogue
// is the caller's, already narrowed to the tenant the import is for.
func ShapeRefusals(document Document, catalog Catalog) []ValidationError {
	if document.ID == "" {
		// Compile refuses a draft with no id before it looks at the graph, and
		// an import has not been saved yet.
		document.ID = "shape-check"
	}
	_, err := Compile(document, catalog)
	var refused *ValidationErrors
	if !errors.As(err, &refused) {
		return nil
	}
	var shape []ValidationError
	for _, issue := range refused.Issues {
		switch issue.Code {
		case ErrorInvalidTopology:
			if strings.Contains(issue.Message, "must not contain a cycle") {
				continue
			}
		case ErrorPortRequired, ErrorPortFull, ErrorPortNotAllowed, ErrorIncompatiblePort, ErrorUnknownPort:
		default:
			continue
		}
		shape = append(shape, issue)
	}
	return shape
}
