package jsrun

// Roots are what a body's globals read beyond its own input items: the
// workflow, the execution, and the nodes that ran before it. The node that
// runs the code builds them from the same request an expression reads, so a
// Code node and an expression see the same data.
//
// Nodes are reached through functions rather than copied in, because a body
// that never names another node should not pay to serialise every node that
// ran before it.
type Roots struct {
	Workflow  WorkflowInfo
	Execution ExecutionInfo
	// Env is the allowlisted environment, backing $env.
	Env map[string]string
	// RunIndex is the Nth time this node runs in the execution, backing
	// $runIndex.
	RunIndex int
	// NodeVersion is the node's type version, backing $nodeVersion.
	NodeVersion float64
	// Timezone is the workflow's time zone, which DateTime, $now and $today
	// default to, as in n8n. Empty means UTC.
	Timezone string
	// Node returns a node that ran earlier in this execution, by name,
	// backing $('Name') and $node['Name']. Nil means no node has run.
	Node func(name string) (NodeView, bool)
	// Pair answers which item of the named node the input item at index
	// descends from: an index into that node's Items, or -1 and the reason
	// there is none. It backs $('Name').item and .itemMatching(index).
	Pair func(name string, index int) (int, string)
}

// WorkflowInfo backs $workflow.
type WorkflowInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// ExecutionInfo backs $execution.
type ExecutionInfo struct {
	ID          string `json:"id"`
	Mode        string `json:"mode"`
	ResumeURL   string `json:"resumeUrl"`
	ApprovalURL string `json:"approvalUrl"`
}

// NodeView is one node that ran, as a body sees it.
type NodeView struct {
	// Items are the JSON of every item the node produced.
	Items []map[string]any `json:"items"`
	// Params are the node's own parameters, backing .params.
	Params map[string]any `json:"params"`
}

// host is what the roots call back into the server for while the code runs.
type host struct {
	// node returns a node's view as JSON, or false when it has not run.
	node func(name string) (string, bool)
	// pair is Roots.Pair.
	pair func(name string, index int) (int, string)
	// console keeps one printed line and reports whether there is room for
	// more.
	console func(level, text string) bool
}

// snapshot is the part of the roots that crosses into the VM whole.
type snapshot struct {
	Workflow    WorkflowInfo      `json:"workflow"`
	Execution   ExecutionInfo     `json:"execution"`
	Env         map[string]string `json:"env"`
	Vars        map[string]any    `json:"vars"`
	RunIndex    int               `json:"runIndex"`
	NodeVersion float64           `json:"nodeVersion"`
	Advice      string            `json:"advice"`
	Caps        callCaps          `json:"caps"`
	// Timezone is the workflow's zone, which Luxon and $now use.
	Timezone string `json:"timezone"`
	// Modules is moduleOrder; Requirable maps require() names to modules;
	// Libraries lists the libraries require() may load, and Preload the ones
	// to load before the code runs.
	Modules    []string          `json:"modules"`
	Requirable map[string]string `json:"requirable"`
	Libraries  []string          `json:"libraries"`
	Preload    []string          `json:"preload"`
}

// callCaps carries the per-call bounds into the VM.
type callCaps struct {
	Elements   int `json:"elements"`
	Characters int `json:"characters"`
	Bytes      int `json:"bytes"`
}

func (roots Roots) snapshot(preload []string) snapshot {
	env := roots.Env
	if env == nil {
		env = map[string]string{}
	}
	version := roots.NodeVersion
	if version == 0 {
		version = 1
	}
	return snapshot{
		Workflow: roots.Workflow, Execution: roots.Execution, Env: env,
		// $vars reads variables the host application defines; KilasFlow has
		// none, and an expression sees the same empty object.
		Vars:     map[string]any{},
		RunIndex: roots.RunIndex, NodeVersion: version, Advice: UnsupportedAdvice,
		Caps:     callCaps{Elements: MaxElementsPerCall, Characters: MaxCharactersPerCall, Bytes: MaxBytesPerCall},
		Timezone: roots.Timezone, Modules: moduleOrder, Requirable: requirable,
		Libraries: libraryNames(), Preload: preload,
	}
}
