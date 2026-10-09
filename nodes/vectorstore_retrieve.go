package nodes

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// VectorModeRetrieve is n8n's default vector-store mode, and the one a stored
// n8n node with no mode means. The store does nothing on its own: it hands
// itself, as a descriptor on an ai_vectorStore port, to the node that
// queries it (the Vector Store Tool).
const VectorModeRetrieve = "retrieve"

// vectorStoreSourceKind names the descriptor a vector store emits in
// retrieve mode: where its documents live and how to embed a query for them,
// with no tool naming. It is what the Vector Store Tool's vectorStore port
// reads.
const vectorStoreSourceKind = "vectorStoreSource"

// sourceFields describes the internal store a retrieve or retrieve-as-tool
// node reads: the collection, the distance it was created with, the metadata
// filter every match must satisfy, and the embeddings that turn a query into
// a vector. It carries no secret; the embeddings descriptor names its
// credential by id only.
func (executor *VectorStoreExecutor) sourceFields(ir workflow.IRNode, collection string, input workflow.NodeInput) map[string]any {
	fields := map[string]any{
		"nodeName":   ir.Name,
		"backend":    "internal",
		"collection": collection,
		"distance":   textValue(ir.Parameters["distance"], VectorDistanceCosine),
	}
	if embeddings := embeddingsDescriptorOf(input["embedding"]); embeddings != nil {
		fields["embeddings"] = embeddings
	}
	if filter, err := vectorObject(ir.Parameters["metadataFilter"]); err == nil && filter != nil {
		fields["metadataFilter"] = filter
	}
	return fields
}

// executeRetrieve hands the store to the node reading its vectorStore port.
// Nothing is searched here: the reader embeds its own query when it runs.
func (executor *VectorStoreExecutor) executeRetrieve(ir workflow.IRNode, collection string, input workflow.NodeInput) (workflow.NodeOutput, error) {
	return retrieveDescriptor(ir, executor.sourceFields(ir, collection, input))
}

// sourceFields describes a customer PostgreSQL table the same way: table,
// columns and the postgres credential's id, never its secret.
func (executor *VectorStorePGVectorExecutor) sourceFields(ir workflow.IRNode, cfg pgvectorTableConfig, input workflow.NodeInput) map[string]any {
	fields := map[string]any{
		"nodeName":        ir.Name,
		"backend":         "postgres",
		"collection":      cfg.collection,
		"tableName":       cfg.table,
		"idColumn":        cfg.id,
		"contentColumn":   cfg.content,
		"metadataColumn":  cfg.metadata,
		"embeddingColumn": cfg.embedding,
		"credentialId":    cfg.credentialID,
	}
	if embeddings := embeddingsDescriptorOf(input["embedding"]); embeddings != nil {
		fields["embeddings"] = embeddings
	}
	return fields
}

// emitStore is executeRetrieve for a customer table. It never opens the
// database: the tool that reads the descriptor does, per query, through the
// same guard every other customer connection goes through.
func (executor *VectorStorePGVectorExecutor) emitStore(ir workflow.IRNode, cfg pgvectorTableConfig, input workflow.NodeInput) (workflow.NodeOutput, error) {
	return retrieveDescriptor(ir, executor.sourceFields(ir, cfg, input))
}

// retrieveDescriptor wraps a store's fields as the one item its vectorStore
// port carries. A store with no embeddings is refused here, while the run can
// still name the node to fix, rather than on the first question a model asks.
func retrieveDescriptor(ir workflow.IRNode, fields map[string]any) (workflow.NodeOutput, error) {
	if fields["embeddings"] == nil {
		return nil, fmt.Errorf("node %q: connect an Embeddings sub-node to the embedding port; the store cannot be queried without one", ir.Name)
	}
	fields["kind"] = vectorStoreSourceKind
	return workflow.NodeOutput{[]workflow.Item{{JSON: map[string]any{descriptorKey: fields}}}}, nil
}
