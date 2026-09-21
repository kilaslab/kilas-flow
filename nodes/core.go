// Package nodes registers KilasFlow's built-in node definitions.
package nodes

import (
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
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
		chatTrigger(),
		setNode(),
		ifNode(),
		mergeNode(),
		httpRequestNode(),
		webhookTrigger(),
		formTrigger(),
		errorTriggerNode(),
		stopAndErrorNode(),
		respondToWebhookNode(),
		scheduleTrigger(),
		postgresNode(),
		// Version 2 registers beside version 1, never over it: a workflow
		// authored against query/execute/transaction keeps running unchanged.
		postgresV2Node(),
		mysqlNode(),
		mysqlV2Node(),
		sqliteNode(),
		chatModelNode(),
		// One node type per provider, registered beside the generic one rather
		// than over it: an imported n8n workflow names a provider type, and a
		// saved workflow on the generic type keeps running unchanged.
		openAIChatModelNode(),
		openRouterChatModelNode(),
		memoryNode(),
		httpToolNode(),
		agentNode(),
		chainLlmNode(),
		calculatorNode(),
		calculatorToolNode(),
		workflowToolNode(),
		outputParserNode(),
		mcpClientToolNode(),
		documentLoaderNode(),
		textSplitterNode(),
		embeddingsNode(),
		extractFromFileNode(),
		vectorStorePGVectorNode(),
		googleDriveNode(),
		googleDriveTrigger(),
		gmailNode(),
		gmailTrigger(),
		codeNode(),
		stickyNoteNode(),
		loopNode(),
		telegramTrigger(),
		switchNode(),
		filterNode(),
		limitNode(),
		noOpNode(),
		aggregateNode(),
		splitOutNode(),
		sortNode(),
		summarizeNode(),
		removeDuplicatesNode(),
		dateTimeNode(),
		datastoreNode(),
		datastoreToolNode(),
		waitNode(),
		executeWorkflowNode(),
		executeWorkflowTrigger(),
		foreignCodeNode(),
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

func embeddingsNode() node.Definition {
	return EmbeddingsNode("")
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
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: "manual",
				Options: []node.PropertyOption{
					{Label: "Manual Mapping", Value: "manual"},
					{Label: "JSON", Value: "raw"},
				},
				Description: "Set fields one at a time, or replace the whole item with a JSON object.",
			},
			{
				Key: "assignments", Label: "Fields to Set", Kind: node.PropertyAssignments, Required: true,
				Description: "The fields to add to every item, in order, each with its own type. " +
					"A field set twice takes the value of the later row.",
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: "manual"}},
			},
			{
				Key: "jsonOutput", Label: "JSON", Kind: node.PropertyJSON, Required: true,
				Description: "The whole output item, as a JSON object. Supports expressions.",
				TypeOptions: &node.TypeOptions{Rows: 6},
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: "raw"}},
			},
			{
				Key: "include", Label: "Input Fields to Include", Kind: node.PropertyOptions, Default: "all",
				Options: []node.PropertyOption{
					{Label: "All Input Fields", Value: "all"},
					{Label: "No Input Fields", Value: "none"},
					{Label: "Selected Input Fields", Value: "selected"},
					{Label: "All Input Fields Except", Value: "except"},
				},
				Description: "Which of the incoming item's own fields survive into the output.",
			},
			{
				Key: "includeFields", Label: "Fields to Include", Kind: node.PropertyString,
				Description: "Comma-separated field names to keep from the incoming item.",
				VisibleWhen: []node.VisibilityCondition{{Key: "include", Equals: "selected"}},
			},
			{
				Key: "excludeFields", Label: "Fields to Exclude", Kind: node.PropertyString,
				Description: "Comma-separated field names to drop from the incoming item.",
				VisibleWhen: []node.VisibilityCondition{{Key: "include", Equals: "except"}},
			},
			{
				Key: "duplicateItem", Label: "Duplicate Item", Kind: node.PropertyBoolean, Default: false,
				Description: "Emit each incoming item several times. Useful for testing; it multiplies everything downstream.",
			},
			{
				Key: "duplicateCount", Label: "Duplicate Count", Kind: node.PropertyNumber, Default: 1,
				VisibleWhen: []node.VisibilityCondition{{Key: "duplicateItem", Equals: true}},
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "dotNotation", Label: "Support Dot Notation", Kind: node.PropertyBoolean, Default: true,
						Description: "On, a field named `a.b` writes `{\"a\": {\"b\": …}}`. Off, it writes a field whose name contains a dot.",
					},
					{
						Key: "ignoreConversionErrors", Label: "Ignore Type Conversion Errors", Kind: node.PropertyBoolean, Default: false,
						Description: "Keep a value that does not match its declared type instead of failing the node.",
					},
					{
						Key: "includeBinary", Label: "Include Binary File", Kind: node.PropertyBoolean, Default: true,
						Description: "Carry the incoming item's attachments through.",
					},
					{
						Key: "stripBinary", Label: "Strip Binary File", Kind: node.PropertyBoolean, Default: false,
						Description: "Drop the incoming item's attachments.",
					},
				},
			},
		},
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
		Description: "Combines several item streams.",
		Category:    "Core",
		// Two is what an unconfigured Merge shows. The real count comes from
		// `numberInputs` through PortsFor, because a fixed pair cannot import a
		// workflow that merged three streams.
		Inputs: []workflow.Port{
			{Name: "input1", DisplayName: "Input 1", Kind: workflow.ConnectionMain},
			{Name: "input2", DisplayName: "Input 2", Kind: workflow.ConnectionMain},
		},
		Outputs: mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Required: true, Default: MergeAppend,
				Options: []node.PropertyOption{
					{Label: "Append", Value: MergeAppend},
					{Label: "Combine by Matching Fields", Value: MergeByFields},
					{Label: "Combine by Position", Value: MergeByPosition},
					{Label: "Combine All (cross join)", Value: MergeCombineAll},
					{Label: "Choose Branch", Value: MergeChooseBranch},
				},
			},
			{
				Key: "numberInputs", Label: "Number of Inputs", Kind: node.PropertyNumber, Default: 2,
				Description: "How many streams this node takes. Changing it changes the node's input ports.",
			},
			{
				Key: "fieldsToMatch", Label: "Fields to Match", Kind: node.PropertyString,
				Description: "Comma-separated field names that must be equal for two items to combine.",
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: MergeByFields}},
			},
			{
				Key: "joinMode", Label: "Output Type", Kind: node.PropertyOptions, Default: "keepMatches",
				Options: []node.PropertyOption{
					{Label: "Keep Matches", Value: "keepMatches"},
					{Label: "Keep Everything", Value: "keepEverything"},
					{Label: "Enrich Input 1", Value: "enrichInput1"},
					{Label: "Enrich Input 2", Value: "enrichInput2"},
					{Label: "Keep Non-Matches", Value: "keepNonMatches"},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: MergeByFields}},
			},
			{
				Key: "chooseBranch", Label: "Branch to Keep", Kind: node.PropertyNumber, Default: 1,
				Description: "Which input's items to pass on, counting from 1.",
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: MergeChooseBranch}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     "core.merge",
		Validate:       validateMergeConfiguration,
		PortsFor:       mergePorts,
	}
}

func mainInput() []workflow.Port {
	return []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}
}

func mainOutput() []workflow.Port {
	return []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}
}

// legacyTimeoutKey is the parameter key the database, HTTP and Code nodes each
// used for their own timeout before it was found to collide with the shared
// setting of the same name.
const legacyTimeoutKey = "timeoutSeconds"

// timeoutParameter reads a node's own timeout parameter, falling back to the
// key it used to be stored under.
//
// The rename is deliberately not a stored-document migration: rewriting every
// saved workflow to correct a parameter name is a far larger and riskier change
// than reading both keys here. The new key wins whenever it is present, so a
// node the editor has saved since the rename means exactly what its form says —
// which also means an old document keeps its configured timeout until the first
// time someone saves that node, when the form's own value takes over.
func timeoutParameter(parameters map[string]any, key string) float64 {
	if value, found := parameters[key]; found && value != nil {
		return numberValue(value)
	}
	return numberValue(parameters[legacyTimeoutKey])
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
		{
			Key: "alwaysOutputData", Label: "Always Output Data", Kind: node.PropertyBoolean, Default: false,
			Description: "Hand downstream nodes an empty item when this node produced none, so a branch that matched " +
				"nothing still runs — a filtered read with no rows still reaches a Respond to Webhook node.",
		},
	}
}
