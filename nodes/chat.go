package nodes

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Chat Trigger is the editor-only start of a conversation: the canvas Chat
// panel sends one item per message, and the graph runs once. Hosted chat pages
// and embed widgets are a later slice; this node does not declare a webhook.
const (
	ChatTriggerNodeType   = "kilasflow.chatTrigger"
	ChatTriggerExecutorID = "core.chatTrigger"
)

func chatTrigger() node.Definition {
	return node.Definition{
		Type:        ChatTriggerNodeType,
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:message-circle"},
		IconColor:   "#14b8a6",
		Version:     workflow.V(1),
		DisplayName: "When chat message received",
		Description: "Starts a workflow from a chat message in the editor.",
		Category:    "Triggers",
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "editorChatNotice", Label: "Open Chat on the canvas to test", Kind: node.PropertyNotice,
				Description: "Send a message from the Chat panel. Hosted chat pages and embed widgets are not in this version.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ChatTriggerExecutorID,
	}
}

// executeChatTrigger emits the item the editor (or API) started the run with.
//
// The panel sends `{action, sessionId, chatInput}`. A run that names this
// trigger without an input still emits that empty item rather than inventing
// a message — Execute is supposed to leave this node alone, and a mistaken
// call should not look like a successful empty chat.
func executeChatTrigger(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{request.Input}}, nil
}
