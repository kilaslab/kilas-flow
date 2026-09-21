package nodes

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Test helpers for the RAG cluster. Kept in a test-only export file so the
// production definitions do not grow a public API for the engine callbacks.

func SplitRecursiveForTest(text string, chunkSize, overlap int) []string {
	return splitRecursive(text, chunkSize, overlap, nil)
}

func ExecuteDocumentLoaderForTest(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	return executeDocumentLoader(ctx, ir, input, request)
}

func ExecuteTextSplitterForTest(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	return executeTextSplitter(ctx, ir, input, request)
}

func VectorStoreToolFromForTest(executor *AgentExecutor, ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	return executor.vectorStoreToolFrom(ir, descriptor, request)
}

func ExecuteExtractFromFileForTest(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	return executeExtractFromFile(ctx, ir, input, request)
}

func CustomerPGVectorErrorForTest(table string, err error) error {
	return customerPGVectorError(table, err)
}
