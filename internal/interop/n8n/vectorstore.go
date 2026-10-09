package n8n

import (
	"fmt"
	"math"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// VectorStoreToolNodeType is the Vector Store Tool's canonical type, mirrored
// from nodes.VectorStoreToolNodeType like every other type here;
// TestMirroredNodeTypesMatchTheNodePack keeps the two in step.
const VectorStoreToolNodeType = "kilasflow.vectorStoreTool"

// n8nPGVectorDefaultTable is the table n8n's PGVector store reads and writes
// when the node names none, and when it names an empty string.
const n8nPGVectorDefaultTable = "n8n_vectors"

// vectorStorePGVectorToKilas carries the store's parameters through, as
// packToKilas does, and writes the two values n8n supplies for a key it did
// not save.
//
// n8n omits a parameter left at its default. A store with no mode is in
// n8n's default mode, retrieve — the one that feeds a Vector Store Tool —
// whereas the node here reads an absent mode as a native insert. And a store
// with no table reads n8n's default table, not this node's: importing it
// against `documents` would query a table n8n never wrote to.
func vectorStorePGVectorToKilas(node Node) (map[string]any, []Unsupported) {
	converted, issues := packToKilas(node)
	if converted == nil {
		converted = map[string]any{}
	}
	if _, present := converted["mode"]; !present {
		converted["mode"] = nodes.VectorModeRetrieve
	}
	// An expression arrives as a marker object and is left to run.
	if _, expression := converted["tableName"].(map[string]any); !expression {
		if table, _ := converted["tableName"].(string); strings.TrimSpace(table) == "" {
			converted["tableName"] = n8nPGVectorDefaultTable
		}
	}
	return converted, issues
}

// kilasPGVectorDefaultTable is the table this server's PGVector store uses
// when the node names none.
const kilasPGVectorDefaultTable = "documents"

// vectorStorePGVectorToN8N is the inverse of vectorStorePGVectorToKilas. Every
// absence n8n would fill differently is written out:
//   - no mode is a native insert here and retrieve in n8n, so it goes out as
//     insert;
//   - Get Many is n8n's load, under any of the spellings the node accepts;
//   - no table is `documents` here and `n8n_vectors` in n8n, so the table the
//     node actually writes goes out by name.
//
// A native insert also takes its documents, vectors and all, on main. n8n's
// insert mode reads them from a Default Data Loader and embeds them with an
// Embeddings sub-node instead, which a graph authored that way does not have;
// the note says what to attach.
func vectorStorePGVectorToN8N(node workflow.Node) (map[string]any, []Lossy) {
	converted, issues := packToN8N(node)
	if converted == nil {
		converted = map[string]any{}
	}
	// packToN8N has already turned an expression back into n8n's `=` string,
	// which matches none of the cases below and goes out as written.
	mode, _ := converted["mode"].(string)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		converted["mode"] = nodes.VectorModeInsert
		issues = append(issues, Lossy{
			Severity: SeverityLossy, Field: "mode",
			Reason: "this store took its documents, with their vectors, on its main input; it goes out in n8n's insert mode, " +
				"which reads documents from a Default Data Loader and embeds them with an Embeddings sub-node, so attach both in n8n",
		})
	case strings.ToLower(nodes.VectorModeGetMany), "get_many", "search":
		converted["mode"] = "load"
	}
	if table, _ := converted["tableName"].(string); strings.TrimSpace(table) == "" {
		converted["tableName"] = kilasPGVectorDefaultTable
	}
	return converted, issues
}

// toolVectorStoreToKilas maps n8n's Vector Store Question Answer Tool.
//
// The model-facing name is the `name` parameter at version 1 and the node's
// own name from 1.1, where the parameter was dropped; a name left empty at
// version 1 falls back to the node's name too. The description is required
// here, as it is for every tool, so a template that left it empty gets one
// that says what the tool reads from rather than an unactivatable node.
func toolVectorStoreToKilas(node Node) (map[string]any, []Unsupported) {
	name := ""
	if node.TypeVersion <= 1 {
		name = strings.TrimSpace(stringParameter(node.Parameters, "name"))
	}
	if name == "" {
		name = node.Name
	}
	parameters := map[string]any{"toolName": nodes.NormalizeToolName(name)}

	var issues []Unsupported
	if description := node.Parameters["description"]; description != nil && description != "" {
		parameters["toolDescription"] = fromN8NValue(description)
	} else {
		written := "Answers questions from the " + name + " documents."
		parameters["toolDescription"] = written
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "description",
			Reason: fmt.Sprintf("the tool had no description, which the model reads to decide when to call it; %q was written in its place, so replace it with what the stored documents hold",
				written),
		})
	}

	// The limit is read as the node validates it: a whole number from 1 to
	// the store's search bound. Anything else is reported and replaced by the
	// default, never rounded or clamped into a number nobody chose.
	parameters["topK"] = float64(nodes.DefaultVectorTopK)
	if raw, present := node.Parameters["topK"]; present {
		topK, ok := numberParameter(node.Parameters, "topK")
		if ok && topK == math.Trunc(topK) && topK >= 1 && topK <= nodes.MaxVectorTopK {
			parameters["topK"] = topK
		} else {
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "topK",
				Reason: fmt.Sprintf("the limit %v is not a whole number from 1 to %d, which this tool needs; it retrieves %d documents per question until you set one",
					raw, nodes.MaxVectorTopK, nodes.DefaultVectorTopK),
			})
		}
	}
	return parameters, issues
}

// toolVectorStoreToN8N writes the tool back at version 1, the version that
// still carries a name parameter, so a toolName chosen here survives the
// round trip instead of being replaced by the node's name.
func toolVectorStoreToN8N(node workflow.Node) (map[string]any, []Lossy) {
	name := strings.TrimSpace(stringParameter(node.Parameters, "toolName"))
	if name == "" {
		name = nodes.NormalizeToolName(node.Name)
	}
	parameters := map[string]any{
		"name":        name,
		"description": toN8NValue(node.Parameters["toolDescription"]),
	}
	if topK, ok := numberParameter(node.Parameters, "topK"); ok {
		parameters["topK"] = topK
	}
	return parameters, nil
}
