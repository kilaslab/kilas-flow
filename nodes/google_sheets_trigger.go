package nodes

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// GoogleSheetsTriggerExecutor emits the polled row on a manual run, the way the
// other poll triggers do: the poller queues an execution carrying the row as
// its input, and the trigger node hands it on.
type GoogleSheetsTriggerExecutor struct{}

func (GoogleSheetsTriggerExecutor) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return workflow.NodeOutput{{item}}, nil
}
