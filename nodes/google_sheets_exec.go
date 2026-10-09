package nodes

import (
	"context"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// GoogleSheetsExecutor runs the Google Sheets node's six operations.
type GoogleSheetsExecutor struct {
	google *GoogleClient
}

// NewGoogleSheetsExecutor builds the Sheets action node over the shared Google
// HTTP client, so it is held to the same egress policy as Drive and Gmail.
func NewGoogleSheetsExecutor(policy safehttp.Policy) *GoogleSheetsExecutor {
	return &GoogleSheetsExecutor{google: NewGoogleClient(policy)}
}

func (executor *GoogleSheetsExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("node %q: not implemented", ir.Name)
}
