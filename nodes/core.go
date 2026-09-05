// Package nodes registers KilasFlow's built-in node definitions.
package nodes

import (
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// RegisterAll installs the built-ins supported by the first graph slice.
// RegisterAll registers every node compiled into this binary.
//
// Order is deterministic and matters: built-ins register first and always win a
// collision, so a pack loaded afterwards can never displace one. Within this
// function the order is the literal order below, so two runs of the same binary
// produce the same catalogue — a catalogue that depended on map iteration or a
// directory listing would change between runs for no reason anyone could see.
func RegisterAll(registry *node.Registry) error {
	for _, definition := range []node.Definition{
		manualTrigger(),
		setNode(),
		ifNode(),
		mergeNode(),
		httpRequestNode(),
		webhookTrigger(),
		respondToWebhookNode(),
		scheduleTrigger(),
		postgresNode(),
		mysqlNode(),
		sqliteNode(),
		chatModelNode(),
		memoryNode(),
		httpToolNode(),
		agentNode(),
		codeNode(),
		stickyNoteNode(),
		loopNode(),
		telegramTrigger(),
	} {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	// The import placeholder is registered once per port arity; see
	// UnsupportedArities for why one definition cannot cover them all.
	for _, arity := range UnsupportedArities {
		if err := registry.Register(unsupportedNode(arity)); err != nil {
			return err
		}
	}
	return nil
}

func manualTrigger() node.Definition {
	return node.Definition{
		Type:           "kilasflow.manual",
		Group:          []node.NodeGroup{node.GroupTrigger},
		Icon:           &node.NodeIcon{Light: "builtin:mouse-pointer-click"},
		IconColor:      "#6366f1",
		Version:        workflow.V(1),
		DisplayName:    "Manual Trigger",
		Description:    "Starts a workflow from the editor or API.",
		Category:       "Triggers",
		Outputs:        mainOutput(),
		SharedSettings: sharedSettings(),
		ExecutorID:     "core.manual",
	}
}

func setNode() node.Definition {
	return node.Definition{
		Type:        "kilasflow.set",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:pencil"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.mode }}",
		Version:     workflow.V(1),
		DisplayName: "Set",
		Description: "Adds or replaces fields on every incoming item.",
		Category:    "Core",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{{
			Key: "assignments", Label: "Fields to Set", Kind: node.PropertyAssignments, Required: true,
			Description: "The fields to add to every item, in order, each with its own type. " +
				"A field set twice takes the value of the later row.",
		}},
		SharedSettings: sharedSettings(),
		ExecutorID:     "core.set",
		Validate:       validateSetConfiguration,
	}
}

func ifNode() node.Definition {
	return node.Definition{
		Type:        "kilasflow.if",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:git-branch"},
		IconColor:   "#f59e0b",
		Version:     workflow.V(1),
		DisplayName: "IF",
		Description: "Routes items to the true or false branch.",
		Category:    "Core",
		Inputs:      mainInput(),
		Outputs: []workflow.Port{
			{Name: "true", Kind: workflow.ConnectionMain},
			{Name: "false", Kind: workflow.ConnectionMain},
		},
		Parameters: []node.PropertyDefinition{{
			Key: "conditions", Label: "Conditions", Kind: node.PropertyConditions, Required: true,
			Description: "Conditions evaluated for each incoming item.",
		}},
		SharedSettings: sharedSettings(),
		ExecutorID:     "core.if",
		Validate:       validateIFConfiguration,
	}
}

func mergeNode() node.Definition {
	return node.Definition{
		Type:        "kilasflow.merge",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:git-merge"},
		IconColor:   "#f59e0b",
		Subtitle:    "{{ $parameter.mode }}",
		Version:     workflow.V(1),
		DisplayName: "Merge",
		Description: "Combines item streams from two main inputs.",
		Category:    "Core",
		Inputs: []workflow.Port{
			{Name: "input1", Kind: workflow.ConnectionMain},
			{Name: "input2", Kind: workflow.ConnectionMain},
		},
		Outputs: mainOutput(),
		Parameters: []node.PropertyDefinition{{
			Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Required: true, Default: "append",
			Options: []node.PropertyOption{{Label: "Append", Value: "append"}},
		}},
		SharedSettings: sharedSettings(),
		ExecutorID:     "core.merge",
		Validate:       validateMergeConfiguration,
	}
}

func mainInput() []workflow.Port {
	return []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}
}

func mainOutput() []workflow.Port {
	return []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}
}

func sharedSettings() []node.PropertyDefinition {
	return []node.PropertyDefinition{
		{Key: "continueOnFail", Label: "Continue on Fail", Kind: node.PropertyBoolean, Default: false},
		{Key: "retryOnFail", Label: "Retry on Fail", Kind: node.PropertyBoolean, Default: false},
		{Key: "timeoutSeconds", Label: "Timeout (seconds)", Kind: node.PropertyNumber, Default: 0},
		{
			Key: "maxTries", Label: "Maximum Attempts", Kind: node.PropertyNumber, Default: 3,
			VisibleWhen: []node.VisibilityCondition{{Key: "retryOnFail", Equals: true}},
		},
		{
			// Non-zero by default on purpose. Without a delay, the first user
			// who ticks Retry on Fail against a rate-limited API sends every
			// attempt inside a millisecond, which turns one failing request
			// into a burst against an upstream that is already struggling.
			Key: "waitBetweenTries", Label: "Wait Between Attempts (ms)", Kind: node.PropertyNumber, Default: 1000,
			VisibleWhen: []node.VisibilityCondition{{Key: "retryOnFail", Equals: true}},
		},
	}
}
