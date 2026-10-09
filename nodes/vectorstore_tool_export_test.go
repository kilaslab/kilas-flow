package nodes

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Test hooks for the Vector Store Tool, kept test-only like the RAG ones in
// rag_export_test.go.

func ExecuteVectorStoreToolForTest(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	return executeVectorStoreTool(ctx, ir, input, request)
}

func VectorStoreAnswerToolFromForTest(ctx context.Context, executor *AgentExecutor, ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	return executor.toolFrom(ctx, ir, descriptor, request)
}
