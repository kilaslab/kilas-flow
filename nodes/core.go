// Package nodes registers KilasFlow's built-in node definitions.
package nodes

import (
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// RegisterAll installs the built-ins supported by the first graph slice.
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
	} {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	return nil
}

func manualTrigger() node.Definition {
	return node.Definition{
		Type:           "kilasflow.manual",
		Version:        1,
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
		Version:     1,
		DisplayName: "Set",
		Description: "Adds or replaces fields on every incoming item.",
		Category:    "Core",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{{
			Key: "assignments", Label: "Assignments", Kind: node.PropertyKeyValue, Required: true,
			Description: "Field/value pairs to add to every item.",
		}},
		SharedSettings: sharedSettings(),
		ExecutorID:     "core.set",
		Validate:       validateSetConfiguration,
	}
}

func ifNode() node.Definition {
	return node.Definition{
		Type:        "kilasflow.if",
		Version:     1,
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
		Version:     1,
		DisplayName: "Merge",
		Description: "Combines item streams from two main inputs.",
		Category:    "Core",
		Inputs: []workflow.Port{
			{Name: "input1", Kind: workflow.ConnectionMain},
			{Name: "input2", Kind: workflow.ConnectionMain},
		},
		Outputs: mainOutput(),
		Parameters: []node.PropertyDefinition{{
			Key: "mode", Label: "Mode", Kind: node.PropertySelect, Required: true, Default: "append",
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
	}
}
