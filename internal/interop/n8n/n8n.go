// Package n8n converts between n8n workflow JSON and KilasFlow's canonical
// document.
//
// This is a boundary adapter and nothing more. KilasFlow never executes n8n
// JSON and takes no runtime dependency on any n8n package: an import is
// translated into canonical nodes that the existing registry and compiler
// validate exactly as they would a hand-built workflow.
//
// A node type outside the advertised subset is never guessed at. It is
// imported as an explicit unsupported placeholder that keeps its original
// type and parameters, so it stays visible on the canvas — and it fails
// compilation, so a workflow containing one can be inspected and edited but
// never activated or run. Silently mapping an unknown node onto a similar one
// would be far worse than refusing it.
package n8n

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// UnsupportedNodeType is the canonical placeholder an unmappable n8n node
// becomes. It is registered in the node catalogue so the editor can render it.
const UnsupportedNodeType = "kilasflow.unsupported"

// StickyNoteNodeType is the canvas annotation an n8n sticky note becomes.
//
// Mirrored from nodes.StickyNoteNodeType rather than imported, for the same
// reason UnsupportedNodeType is: the adapter must not depend on the node pack.
// TestMirroredNodeTypesMatchTheNodePack keeps the two in step.
const StickyNoteNodeType = "kilasflow.stickyNote"

// Document is the subset of n8n workflow JSON this adapter reads and writes.
//
// Fields outside it are ignored on import and never invented on export; the
// lossy report names what was dropped.
type Document struct {
	Name        string         `json:"name"`
	Nodes       []Node         `json:"nodes"`
	Connections Connections    `json:"connections"`
	Settings    map[string]any `json:"settings,omitempty"`
	PinData     map[string]any `json:"pinData,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
	// StaticData is n8n's per-workflow persisted scratch space. It was not even
	// a field, so it was dropped before anything could report it.
	StaticData map[string]any `json:"staticData,omitempty"`
}

// Node is one n8n node.
type Node struct {
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	TypeVersion float64        `json:"typeVersion"`
	Position    []float64      `json:"position,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Credentials map[string]any `json:"credentials,omitempty"`
	Disabled    bool           `json:"disabled,omitempty"`
	Notes       string         `json:"notes,omitempty"`
	// WebhookID is n8n's per-node webhook identity. A node imported as a
	// placeholder must carry it back out, or the exported workflow claims a
	// different endpoint than the one it came from.
	WebhookID string `json:"webhookId,omitempty"`
	// The error-handling set. KilasFlow does not honour these yet — that is a
	// separate ticket — but the placeholder must not lose them, because a node
	// that round-trips without its retry policy silently changes behaviour in
	// the instance it returns to.
	ContinueOnFail   bool    `json:"continueOnFail,omitempty"`
	RetryOnFail      bool    `json:"retryOnFail,omitempty"`
	MaxTries         float64 `json:"maxTries,omitempty"`
	WaitBetweenTries float64 `json:"waitBetweenTries,omitempty"`
	AlwaysOutputData bool    `json:"alwaysOutputData,omitempty"`
	ExecuteOnce      bool    `json:"executeOnce,omitempty"`
	OnError          string  `json:"onError,omitempty"`
}

// Connections is n8n's shape: source node *name* → connection kind → one slot
// per output index → the targets on that output.
type Connections map[string]map[string][][]Target

// Target is one end of an n8n connection.
type Target struct {
	Node  string `json:"node"`
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// Unsupported reports one element the adapter refused to map.
// IssueSeverity says what an import or export diagnostic actually costs the
// user, which is the difference between "fix this before activating" and "we
// noticed and moved on".
type IssueSeverity string

const (
	// SeverityBlocking means the workflow cannot run as imported.
	SeverityBlocking IssueSeverity = "blocking"
	// SeverityLossy means the element was carried, but differently.
	SeverityLossy IssueSeverity = "lossy"
	// SeverityDropped means the element was not carried at all. It is not
	// lossy: nothing about it survived, and saying otherwise would imply a
	// setting was applied when it was ignored.
	SeverityDropped IssueSeverity = "dropped"
)

// ImportIssue is one thing the importer could not carry faithfully.
//
// It was called Unsupported when it only ever described a node type with no
// equivalent. It now also reports fields that were dropped or carried
// differently, so the name would have been actively misleading — a `notes`
// field is not "unsupported", it is simply not carried.
type ImportIssue struct {
	// Severity says whether this stops the workflow running.
	Severity IssueSeverity `json:"severity" enum:"blocking,lossy,dropped" doc:"blocking stops the workflow running; lossy was carried differently; dropped was not carried at all"`
	// NodeName is the n8n node's name, which is what a user sees in n8n.
	NodeName string `json:"nodeName,omitempty"`
	NodeID   string `json:"nodeId,omitempty"`
	// Field names the specific element, where there is one — `pinData`,
	// `retryOnFail`. Empty when the issue is about the node as a whole.
	Field string `json:"field,omitempty"`
	// Type and TypeVersion are the original n8n identity, preserved so the
	// message can name exactly what was not supported.
	Type string `json:"type,omitempty"`
	// TypeVersion is the source version exactly as n8n wrote it. It was an int
	// and truncated through a cast, so a node on version 4.2 was reported as
	// version 4 — which is a different node with a different parameter shape,
	// and the diagnostic pointed at the wrong one.
	TypeVersion workflow.TypeVersion `json:"typeVersion,omitempty"`
	Reason      string               `json:"reason"`
}

// Unsupported is the previous name for ImportIssue.
//
// Kept as an alias so the mapping table's converter signatures, which are
// written once per node type, did not all have to change in the same commit
// that widened the type.
type Unsupported = ImportIssue

// ExportIssue is one thing the exporter could not carry faithfully. It mirrors
// ImportIssue so the two directions read the same way.
type ExportIssue struct {
	Severity IssueSeverity `json:"severity" enum:"blocking,lossy,dropped" doc:"blocking stops the workflow running; lossy was carried differently; dropped was not carried at all"`
	NodeName string        `json:"nodeName,omitempty"`
	Field    string        `json:"field,omitempty"`
	Reason   string        `json:"reason"`
}

// Lossy is the previous name for ExportIssue.
type Lossy = ExportIssue

// mapping is one entry in the advertised node subset.
type mapping struct {
	n8nType   string
	kilasType string
	// kilasVersion is the target version when the source names none.
	kilasVersion workflow.TypeVersion
	// toKilas translates n8n parameters. A nil translator means the node has
	// no parameters worth carrying.
	toKilas func(node Node) (map[string]any, []Unsupported)
	// toN8N translates back. A nil translator exports no parameters.
	toN8N func(node workflow.Node) (map[string]any, []Lossy)
	// refuseKilas names why a source node must not import as this mapping's
	// target, or empty when it may. It exists for mappings whose source
	// carries a variant selector this server does not implement: importing
	// the variant as the target would run a workflow the author never wrote.
	refuseKilas func(node Node) string
	// exportTypeVersion is the n8n typeVersion written on export.
	exportTypeVersion float64
	// publishedVersions are the n8n typeVersions the mapped node actually has,
	// transcribed from n8n's own registration.
	//
	// It exists so an imported node can go back out at the version it was
	// authored at instead of being rewritten to one number. n8n's own
	// getNodeType is an exact map lookup with no resolve-down — see
	// packages/workflow/src/versioned-node-type.ts — so a typeVersion it does
	// not publish makes it throw NodeVersionNotFoundError when the file is
	// opened. A version outside this list therefore falls back to
	// exportTypeVersion rather than being written through.
	//
	// Empty means "do not carry the node's own version", which is the right
	// answer for a mapping whose two sides number their versions differently.
	publishedVersions []float64
	// minimumExportVersion raises the written typeVersion for a node whose
	// parameters need a newer n8n version than the pin.
	//
	// It only ever raises: a node imported at a version n8n publishes keeps
	// that version, and one whose configuration would be meaningless at the pin
	// — a Webhook answering several methods exists only from 2.1 — is written
	// at the version that publishes it instead of one that cannot represent it.
	// Zero means the pin applies.
	minimumExportVersion func(node workflow.Node) float64
	// sharedVersion marks a mapping whose two sides use the same version
	// numbers, so export writes the node's own version rather than a fixed one.
	//
	// It is what a generated pack needs: the WAHA node is registered at 202409
	// and 202502 because the package it mirrors publishes those, and exporting
	// both as one number would send a workflow back claiming a version it was
	// not authored at.
	sharedVersion bool
	// exportOnly marks a canonical node with no n8n equivalent to import from.
	exportOnly bool
	// importOnly marks an n8n node with no faithful export.
	importOnly bool
}

// SupportedMappings is the advertised subset, in stable order.
//
// It is deliberately explicit rather than derived: interoperability claims are
// only meaningful if the exact list is written down and testable.
func SupportedMappings() []string {
	names := make([]string, 0, len(mappings))
	for _, entry := range mappings {
		names = append(names, entry.n8nType+" ↔ "+entry.kilasType)
	}
	sort.Strings(names)
	return names
}

var mappings = []mapping{
	{
		n8nType: "n8n-nodes-base.manualTrigger", kilasType: "kilasflow.manual", kilasVersion: workflow.V(1),
		exportTypeVersion: 1,
	},
	{
		n8nType: "n8n-nodes-base.set", kilasType: "kilasflow.set", kilasVersion: workflow.V(1),
		exportTypeVersion: 3.4, toKilas: setToKilas, toN8N: setToN8N,
	},
	{
		n8nType: "n8n-nodes-base.if", kilasType: "kilasflow.if", kilasVersion: workflow.V(1),
		exportTypeVersion: 2, toKilas: ifToKilas, toN8N: ifToN8N,
	},
	{
		n8nType: "n8n-nodes-base.merge", kilasType: "kilasflow.merge", kilasVersion: workflow.V(1),
		exportTypeVersion: 3, toKilas: mergeToKilas, toN8N: mergeToN8N,
	},
	{
		n8nType: "n8n-nodes-base.httpRequest", kilasType: "kilasflow.httpRequest", kilasVersion: workflow.V(1),
		exportTypeVersion: 4.2, toKilas: httpToKilas, toN8N: httpToN8N,
	},
	{
		n8nType: "n8n-nodes-base.webhook", kilasType: "kilasflow.webhook", kilasVersion: workflow.V(1),
		exportTypeVersion: 2, toKilas: webhookToKilas, toN8N: webhookToN8N,
		minimumExportVersion: webhookMinimumVersion,
	},
	{
		n8nType: "n8n-nodes-base.respondToWebhook", kilasType: "kilasflow.respondToWebhook", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.1, toKilas: respondToKilas, toN8N: respondToN8N,
	},
	// The error workflow pair. An error workflow is what n8n runs when another
	// workflow fails, and both halves of it had no mapping — so a workflow that
	// was *about* handling failures imported as unsupported placeholders.
	{
		n8nType: "n8n-nodes-base.errorTrigger", kilasType: ErrorTriggerNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: errorTriggerToKilas, toN8N: errorTriggerToN8N,
	},
	{
		n8nType: "n8n-nodes-base.stopAndError", kilasType: StopAndErrorNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: stopAndErrorToKilas, toN8N: stopAndErrorToN8N,
	},
	// The Form Trigger. Both spellings: the community package published it as
	// `n8n-nodes-base.formTrigger` before it was folded into core, and an
	// exported workflow may name either.
	{
		n8nType: "n8n-nodes-base.formTrigger", kilasType: FormTriggerType, kilasVersion: workflow.V(1),
		exportTypeVersion: 2.2, toKilas: formTriggerToKilas, toN8N: formTriggerToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-base.formTrigger", kilasType: FormTriggerType, kilasVersion: workflow.V(1),
		exportTypeVersion: 2.2, toKilas: formTriggerToKilas, toN8N: formTriggerToN8N, importOnly: true,
	},
	{
		n8nType: "n8n-nodes-base.scheduleTrigger", kilasType: "kilasflow.schedule", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.2, toKilas: scheduleToKilas, toN8N: scheduleToN8N,
	},
	{
		// Version 2, which is where the operation set lives. Every Postgres
		// node n8n exports carries a typeVersion of 2.4 or higher, and the
		// registry resolves the highest version at or below what a document
		// asks for — so an imported node lands here and its operation carries
		// across as itself rather than being flattened to an empty query.
		n8nType: "n8n-nodes-base.postgres", kilasType: "kilasflow.postgres", kilasVersion: workflow.V(2),
		// 2.7 rather than a lower pin, because 2.7 is the version whose
		// behaviour matches this server's. Below it n8n hands DATE and
		// date-array columns back as JS Date objects where this server returns
		// RFC3339 strings, and below 2.5 it binds query replacements through
		// its own unvalidated legacy path.
		exportTypeVersion: 2.7,
		// Transcribed from Postgres.node.ts at 2.34.0, which registers exactly
		// these. The reference checkout is never a build input, so this is a
		// literal and the test below is what keeps it honest.
		publishedVersions: []float64{1, 2, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7},
		toKilas:           postgresToKilas, toN8N: postgresToN8N,
	},
	{
		// Version 2, for the same reason PostgreSQL is: every MySQL node n8n
		// exports carries a typeVersion of 2 or higher and would otherwise land
		// on v1 with v2's parameters.
		n8nType: "n8n-nodes-base.mySql", kilasType: "kilasflow.mysql", kilasVersion: workflow.V(2),
		// 2.5 is the highest MySql.node.ts registers, and the one above whose
		// gate n8n validates that every $n placeholder a query names actually
		// has a bound value.
		exportTypeVersion: 2.5,
		publishedVersions: []float64{1, 2, 2.1, 2.2, 2.3, 2.4, 2.5},
		toKilas:           mysqlToKilas, toN8N: mysqlToN8N,
	},
	{
		n8nType: "n8n-nodes-base.stickyNote", kilasType: StickyNoteNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: stickyToKilas, toN8N: stickyToN8N,
	},

	// Data shaping. Aggregate and Split Out are inverses; the rest are the
	// transforms the corpus actually reaches for.
	{
		n8nType: "n8n-nodes-base.aggregate", kilasType: AggregateNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: aggregateToKilas, toN8N: aggregateToN8N,
	},
	{
		n8nType: "n8n-nodes-base.splitOut", kilasType: SplitOutNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: splitOutToKilas, toN8N: splitOutToN8N,
	},
	{
		n8nType: "n8n-nodes-base.sort", kilasType: SortNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: sortToKilas, toN8N: sortToN8N,
	},
	{
		n8nType: "n8n-nodes-base.summarize", kilasType: SummarizeNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.1, toKilas: summarizeToKilas, toN8N: summarizeToN8N,
	},
	{
		n8nType: "n8n-nodes-base.removeDuplicates", kilasType: RemoveDuplicatesNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 2, toKilas: removeDuplicatesToKilas, toN8N: removeDuplicatesToN8N,
	},

	// Code. Refused rather than translated, but refused as a first-class node:
	// see codeToKilas for why translating is the worse of the two.
	{
		n8nType: "n8n-nodes-base.code", kilasType: ForeignCodeNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 2, toKilas: codeToKilas, toN8N: codeToN8N,
	},

	// Workflow composition. Every corpus workflow that factored shared logic
	// into a sub-workflow imported as the unsupported placeholder before.
	{
		n8nType: "n8n-nodes-base.executeWorkflow", kilasType: ExecuteWorkflowNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.2, toKilas: executeWorkflowToKilas, toN8N: executeWorkflowToN8N,
	},
	{
		n8nType: "n8n-nodes-base.executeWorkflowTrigger", kilasType: ExecuteWorkflowTriggerType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.1, toKilas: executeWorkflowTriggerToKilas, toN8N: executeWorkflowTriggerToN8N,
	},

	// Time. Both were the unsupported placeholder before, so a single Wait or
	// a single date calculation blocked a whole imported workflow.
	{
		n8nType: "n8n-nodes-base.dateTime", kilasType: DateTimeNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 2, toKilas: dateTimeToKilas, toN8N: dateTimeToN8N,
	},
	{
		n8nType: "n8n-nodes-base.wait", kilasType: WaitNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.1, toKilas: waitToKilas, toN8N: waitToN8N,
	},

	// Flow control. Every one of these used to become the unsupported
	// placeholder, so a single Switch made an entire imported workflow
	// unactivatable.
	{
		n8nType: "n8n-nodes-base.switch", kilasType: SwitchNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 3.2, toKilas: switchToKilas, toN8N: switchToN8N,
	},
	{
		n8nType: "n8n-nodes-base.filter", kilasType: FilterNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 2.2, toKilas: filterNodeToKilas, toN8N: filterNodeToN8N,
	},
	{
		n8nType: "n8n-nodes-base.limit", kilasType: LimitNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: limitToKilas, toN8N: limitToN8N,
	},
	{
		n8nType: "n8n-nodes-base.noOp", kilasType: NoOpNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1,
	},
	{
		n8nType: "n8n-nodes-base.splitInBatches", kilasType: LoopNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 3, toKilas: splitInBatchesToKilas, toN8N: splitInBatchesToN8N,
	},

	// Telegram. The action node is a pack; the trigger is a built-in, because
	// its registration, its file downloads and its polling mode are behaviour
	// rather than data.
	{
		n8nType: "n8n-nodes-base.telegram", kilasType: TelegramNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.2, toKilas: telegramToKilas, toN8N: telegramToN8N,
	},
	{
		n8nType: "n8n-nodes-base.telegramTrigger", kilasType: TelegramTriggerNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.2, toKilas: telegramTriggerToKilas, toN8N: packToN8N,
	},

	// WAHA. Four entries for two nodes: the published package is scoped and an
	// older one was not, and a workflow authored against either has to find the
	// same node.
	//
	// The capitalisation is not a typo and must not be normalised. One package
	// ships `WAHA` in caps for the action node and `wahaTrigger` in camel case
	// for the trigger; these strings are matched byte for byte, exactly as
	// `n8n-nodes-base.mySql` already is, and being helpful about case here
	// would break both mappings at once.
	{
		n8nType: "@devlikeapro/n8n-nodes-waha.WAHA", kilasType: WAHANodeType,
		kilasVersion: workflow.V(202502), sharedVersion: true,
		toKilas: packToKilas, toN8N: packToN8N,
	},
	{
		n8nType: "@devlikeapro/n8n-nodes-waha.wahaTrigger", kilasType: WAHATriggerNodeType,
		kilasVersion: workflow.V(202502), sharedVersion: true,
		toKilas: packToKilas, toN8N: packToN8N,
	},
	// The legacy unscoped forms, listed rather than prefix-matched: an explicit
	// entry is greppable and cannot accidentally capture a package that merely
	// begins with the same characters.
	{
		n8nType: "n8n-nodes-waha.WAHA", kilasType: WAHANodeType,
		kilasVersion: workflow.V(202502), sharedVersion: true,
		toKilas: packToKilas, toN8N: packToN8N, importOnly: true,
	},
	{
		n8nType: "n8n-nodes-waha.wahaTrigger", kilasType: WAHATriggerNodeType,
		kilasVersion: workflow.V(202502), sharedVersion: true,
		toKilas: packToKilas, toN8N: packToN8N, importOnly: true,
	},

	// GOWA (@aldinokemal2104/n8n-nodes-gowa). Mapped onto HTTP against the GOWA
	// REST API for the operations this suite uses; see gowa.go and packs/gowa/GAPS.md.
	{
		n8nType: "@aldinokemal2104/n8n-nodes-gowa.gowa", kilasType: "kilasflow.httpRequest", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: gowaToHTTP, toN8N: gowaToN8N,
		refuseKilas: refuseUnknownGOWAOperation,
	},
	{
		n8nType: "n8n-nodes-gowa.gowa", kilasType: "kilasflow.httpRequest", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: gowaToHTTP, toN8N: gowaToN8N, importOnly: true,
		refuseKilas: refuseUnknownGOWAOperation,
	},
	// The LangChain cluster. These are the AI node types, which until now had no
	// entry at all and so arrived as the unsupported placeholder — an imported
	// agent was a graph that could be looked at and never activated.
	//
	// The connection direction needs no translation. n8n keys a connection by
	// its source node, and for a typed channel the source is the sub-node and
	// the target is the root agent, which is the direction workflow.Connection
	// already uses. See the cluster translators in parameters.go.
	{
		n8nType: "@n8n/n8n-nodes-langchain.agent", kilasType: "kilasflow.agent", kilasVersion: workflow.V(1),
		exportTypeVersion: 3.1, toKilas: agentToKilas, toN8N: agentToN8N,
		refuseKilas: refuseNonToolsAgent,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.chainLlm", kilasType: "kilasflow.chainLlm", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.9, toKilas: chainToKilas, toN8N: chainToN8N,
	},
	{
		// Editor-only in this slice: hosted chat, embed widgets, CORS and
		// public auth are dropped rather than run. All published 1.x versions
		// land here so a 1.1 corpus node and a 1.4 export are not placeholders.
		n8nType: "@n8n/n8n-nodes-langchain.chatTrigger", kilasType: ChatTriggerNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1.1, publishedVersions: []float64{1, 1.1, 1.2, 1.3, 1.4},
		toKilas: chatTriggerToKilas, toN8N: chatTriggerToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatOpenAi", kilasType: "kilasflow.lmChatOpenAi", kilasVersion: workflow.V(1),
		// 1.2 rather than the published 1.3: 1.3 switches to OpenAI's Responses
		// API, which is a different wire protocol, and 1.2 is the highest
		// version whose model locator is the shape written here.
		exportTypeVersion: 1.2, toKilas: openAIModelToKilas, toN8N: openAIModelToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatOpenRouter", kilasType: "kilasflow.lmChatOpenRouter", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: openRouterModelToKilas, toN8N: openRouterModelToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.memoryBufferWindow", kilasType: "kilasflow.memoryBuffer", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.3, toKilas: memoryToKilas, toN8N: memoryToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.toolHttpRequest", kilasType: "kilasflow.httpTool", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.1, toKilas: httpToolToKilas, toN8N: httpToolToN8N,
	},
	// The tool and model nodes this server already implements, whose n8n
	// equivalents had no entry at all — so they arrived as unsupported
	// placeholders and blocked the whole workflow, although the native node
	// was sitting right there. Every entry below is a mapping between two
	// nodes that both exist; none of them needed new node code.
	{
		n8nType: "@n8n/n8n-nodes-langchain.toolCalculator", kilasType: "kilasflow.calculatorTool", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: toolCalculatorToKilas, toN8N: toolCalculatorToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.mcpClientTool", kilasType: "kilasflow.mcpClientTool", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.2, toKilas: mcpClientToolToKilas, toN8N: mcpClientToolToN8N,
	},
	// The OpenAI-compatible providers. Each publishes an endpoint that speaks
	// OpenAI's protocol, so the native node is the same one in every case and
	// only the base URL differs.
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatOllama", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: ollamaModelToKilas, importOnly: true,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmOllama", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: ollamaModelToKilas, importOnly: true,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatGoogleGemini", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, importOnly: true,
		toKilas: func(node Node) (map[string]any, []Unsupported) {
			return openAICompatibleModelToKilas(node, "https://generativelanguage.googleapis.com/v1beta/openai", "model")
		},
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatDeepSeek", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, importOnly: true,
		toKilas: func(node Node) (map[string]any, []Unsupported) {
			return openAICompatibleModelToKilas(node, "https://api.deepseek.com/v1", "model")
		},
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatGroq", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, importOnly: true,
		toKilas: func(node Node) (map[string]any, []Unsupported) {
			return openAICompatibleModelToKilas(node, "https://api.groq.com/openai/v1", "model")
		},
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatMistralCloud", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, importOnly: true,
		toKilas: func(node Node) (map[string]any, []Unsupported) {
			return openAICompatibleModelToKilas(node, "https://api.mistral.ai/v1", "model")
		},
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.lmChatXAiGrok", kilasType: "kilasflow.chatModel", kilasVersion: workflow.V(1),
		exportTypeVersion: 1, importOnly: true,
		toKilas: func(node Node) (map[string]any, []Unsupported) {
			return openAICompatibleModelToKilas(node, "https://api.x.ai/v1", "model")
		},
	},
	{
		// Versions 1 through 2.2: v1 is [1, 1.1, 1.2, 1.3] and v2 is
		// [2, 2.1, 2.2], transcribed from the two versionDescriptions.
		// 2.2 is the export pin because it is the newest version whose
		// resourceMapper inputs this translator writes.
		n8nType: "@n8n/n8n-nodes-langchain.toolWorkflow", kilasType: "kilasflow.workflowTool", kilasVersion: workflow.V(1),
		exportTypeVersion: 2.2, toKilas: workflowToolToKilas, toN8N: workflowToolToN8N,
	},
	{
		n8nType: "@n8n/n8n-nodes-langchain.outputParserStructured", kilasType: "kilasflow.outputParser", kilasVersion: workflow.V(1),
		exportTypeVersion: 1.3, toKilas: outputParserToKilas, toN8N: outputParserToN8N,
	},
	// The Data Table family. Twelve operations across two resources — seven
	// row operations and five table ones, with the table update surfaced as
	// Rename — and the same resource locator and column-mapping surface on
	// both the node and its tool variant. The tool is its own entry rather
	// than a derivation: KilasFlow has no usableAsTool concept to derive
	// from, and the HTTP tool beside it is already a separately registered
	// type. See the translators in parameters.go.
	{
		n8nType: DataTableNodeType, kilasType: DatastoreNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: dataTableToKilas, toN8N: dataTableToN8N,
	},
	{
		n8nType: DataTableToolNodeType, kilasType: DatastoreToolNodeType, kilasVersion: workflow.V(1),
		exportTypeVersion: 1, toKilas: dataTableToolToKilas, toN8N: dataTableToolToN8N,
	},
}

// The WAHA pack's node types, named here so the mapping table and the pack
// cannot disagree about them without a compile error somewhere.
// The flow-control family's node types, named here so the mapping table and
// the node package cannot disagree about them without a compile error.
const (
	AggregateNodeType        = "kilasflow.aggregate"
	SplitOutNodeType         = "kilasflow.splitOut"
	SortNodeType             = "kilasflow.sort"
	SummarizeNodeType        = "kilasflow.summarize"
	RemoveDuplicatesNodeType = "kilasflow.removeDuplicates"
)

// The time family's node types.
const (
	DateTimeNodeType = "kilasflow.dateTime"
	WaitNodeType     = "kilasflow.wait"
)

// ForeignCodeNodeType holds an imported Code node this runtime cannot run.
const ForeignCodeNodeType = "kilasflow.foreignCode"

// The workflow-composition family's node types.
const (
	ExecuteWorkflowNodeType    = "kilasflow.executeWorkflow"
	ExecuteWorkflowTriggerType = "kilasflow.executeWorkflowTrigger"
)

// FormTriggerType is the form trigger's canonical type. Mirrored from
// nodes.FormTriggerType rather than imported, for the same reason every other
// type here is; TestMirroredNodeTypesMatchTheNodePack keeps them in step.
const FormTriggerType = "kilasflow.formTrigger"

// ChatTriggerNodeType is the editor chat start, mirrored from
// nodes.ChatTriggerNodeType. Hosted chat is not mapped; the node itself is.
const ChatTriggerNodeType = "kilasflow.chatTrigger"

// The error-workflow pair's canonical types, mirrored from
// nodes.ErrorTriggerNodeType and nodes.StopAndErrorNodeType.
const (
	ErrorTriggerNodeType = "kilasflow.errorTrigger"
	StopAndErrorNodeType = "kilasflow.stopAndError"
)

const (
	SwitchNodeType = "kilasflow.switch"
	FilterNodeType = "kilasflow.filter"
	LimitNodeType  = "kilasflow.limit"
	NoOpNodeType   = "kilasflow.noOp"
	LoopNodeType   = "kilasflow.loop"
)

const (
	WAHANodeType        = "pack.waha"
	WAHATriggerNodeType = "pack.wahaTrigger"
	// TelegramNodeType is the pack; TelegramTriggerNodeType is a built-in.
	TelegramNodeType        = "pack.telegram"
	TelegramTriggerNodeType = "kilasflow.telegramTrigger"
)

// The Data Table family's node types, named here so the mapping table cannot
// drift from them without a compile error somewhere. The capitalisation is
// n8n's own — dataTable in camel case for both the node and its tool variant
// — and is matched byte for byte, never normalised.
const (
	DataTableNodeType     = "n8n-nodes-base.dataTable"
	DataTableToolNodeType = "n8n-nodes-base.dataTableTool"
)

// The datastore family's canonical types, mirrored from nodes.DatastoreNodeType
// and the datastore tool registration rather than imported, for the same
// reason UnsupportedNodeType is: the adapter must not depend on the node pack.
// TestDatastoreTypesMatchTheNodePack keeps them in step.
const (
	DatastoreNodeType     = "kilasflow.datastore"
	DatastoreToolNodeType = "kilasflow.datastoreTool"
)

func byN8NType(nodeType string) (mapping, bool) {
	for _, entry := range mappings {
		if entry.n8nType == nodeType && !entry.exportOnly {
			return entry, true
		}
	}
	return mapping{}, false
}

func byKilasType(nodeType string) (mapping, bool) {
	for _, entry := range mappings {
		if entry.kilasType == nodeType && !entry.importOnly {
			return entry, true
		}
	}
	return mapping{}, false
}

// ImportResult is one converted workflow plus everything the adapter refused.
type ImportResult struct {
	Document    workflow.Document
	Unsupported []Unsupported
}

// Import converts n8n workflow JSON into a canonical KilasFlow document.
//
// The result is a *draft*. It is not compiled here: the caller saves it
// through the normal repository path, and the existing compiler is what
// decides whether it can be activated or run. That keeps one validation
// authority rather than a second, weaker one inside the adapter.
//
// The catalogue is read, never validated against. n8n identifies a connection
// endpoint by kind and index; KilasFlow identifies it by port name, and the
// only authority on what ports a node type declares is the registry. Asking it
// is what lets a typed AI edge land on the right port without a second
// hardcoded table to drift from the definitions. A nil catalogue falls back to
// the positional names, which can only resolve the item channel.
func Import(payload []byte, catalog workflow.Catalog) (ImportResult, error) {
	if len(payload) == 0 {
		return ImportResult{}, fmt.Errorf("no workflow JSON was supplied")
	}
	var source Document
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	if err := decoder.Decode(&source); err != nil {
		return ImportResult{}, fmt.Errorf("this is not valid n8n workflow JSON: %w", err)
	}
	if len(source.Nodes) == 0 {
		return ImportResult{}, fmt.Errorf("the workflow contains no nodes")
	}

	name := strings.TrimSpace(source.Name)
	if name == "" {
		name = "Imported workflow"
	}

	// A placeholder's declared ports must cover the edges the source workflow
	// drew, so the arity has to be known before the node is converted — which
	// means reading the connections first. Getting this wrong is not a subtle
	// failure: the compiler rejects the edge for an unknown port before the
	// placeholder's own validator runs, so the user is told their topology is
	// broken rather than that a node is unsupported.
	arityByName := observedArity(source.Connections)

	unsupported := make([]Unsupported, 0)
	nodes := make([]workflow.Node, 0, len(source.Nodes))
	// n8n connections are keyed by node *name*; KilasFlow's are keyed by ID.
	idByName := make(map[string]string, len(source.Nodes))
	seenName := make(map[string]bool, len(source.Nodes))

	for index, node := range source.Nodes {
		// Verbatim, never trimmed. n8n keys connections by the name as written,
		// and a name with a trailing space is legal there — so trimming here and
		// looking the raw name up below dropped every edge of a node called
		// "Get ScreenShot ". Only a name that is *entirely* whitespace needs a
		// substitute, because nothing can refer to it.
		name := node.Name
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("Node %d", index+1)
		}
		if seenName[name] {
			// n8n keys connections by name, so duplicates would make the graph
			// ambiguous. Refusing beats importing something that routes wrongly.
			return ImportResult{}, fmt.Errorf("two nodes are both named %q; n8n connections are keyed by name, so this workflow cannot be imported unambiguously", name)
		}
		seenName[name] = true

		id := strings.TrimSpace(node.ID)
		if id == "" {
			id = fmt.Sprintf("n8n-%d", index+1)
		}
		idByName[name] = id

		converted := workflow.Node{
			ID: id, Name: name,
			Position: positionFrom(node.Position),
		}

		entry, supported := byN8NType(node.Type)
		if !supported {
			// Preserved rather than dropped: the node stays visible with its
			// original identity, and the placeholder refuses to compile.
			//
			// The capsule is the whole source node, not a summary of it. A
			// placeholder exists so a node "came from n8n and belongs there",
			// and a round trip that returned it stripped of its credentials,
			// its notes or its retry policy would defeat exactly that.
			unsupported = append(unsupported, placeholderFor(&converted, name, id, node, arityByName,
				fmt.Sprintf("KilasFlow has no equivalent of the n8n node %q. It was imported as an unsupported placeholder: the workflow can be edited, but it cannot run until this node is replaced.", node.Type)))
			nodes = append(nodes, converted)
			continue
		}
		// A mapped node the source variant disqualifies: imported as the
		// target it would run a workflow the author never wrote, so it
		// arrives as the same placeholder an unmapped node does.
		if entry.refuseKilas != nil {
			if reason := entry.refuseKilas(node); reason != "" {
				unsupported = append(unsupported, placeholderFor(&converted, name, id, node, arityByName, reason))
				nodes = append(nodes, converted)
				continue
			}
		}

		// Node-level elements the mapping does not carry. This runs only for a
		// *mapped* node: an unsupported one keeps the whole source node in its
		// capsule and returns it on export, so reporting these there would say
		// something was lost when it was preserved.
		unsupported = append(unsupported, nodeIssues(name, id, node)...)
		if settings := errorHandlingSettings(node); len(settings) > 0 {
			converted.Settings = settings
		}

		converted.Type = entry.kilasType
		// n8n's own typeVersion is preserved rather than replaced with the
		// mapping's target. KilasFlow's node versions mirror n8n's, so keeping
		// the source version means an imported node lands on the right
		// parameter shape the moment that shape is registered — and until then
		// the registry resolves down to the highest version it does have.
		converted.TypeVersion = entry.kilasVersion
		if sourceVersion := sourceTypeVersion(node.TypeVersion); !sourceVersion.IsZero() {
			converted.TypeVersion = sourceVersion
		}
		if issue, mismatched := versionIssue(catalog, entry, name, id, node, converted); mismatched {
			unsupported = append(unsupported, issue)
		}
		if issue, referenced := credentialIssue(name, id, node); referenced {
			// Blocking, including for GOWA. An unbound credential on a node that
			// had one in n8n is a node that will call somebody's service
			// unauthenticated or not at all, and downgrading it to lossy let a
			// GOWA automation activate against a host it was never pointed at.
			unsupported = append(unsupported, issue)
			// The reference itself is never carried. An n8n credential id
			// names a row in somebody else's database; storing it would leave
			// a node that looks configured and fails at run time.
			converted.Credentials = nil
		}
		if entry.toKilas != nil {
			parameters, issues := entry.toKilas(node)
			converted.Parameters = parameters
			for _, issue := range issues {
				issue.NodeName = name
				issue.NodeID = id
				unsupported = append(unsupported, issue)
			}
		}
		// After the translator, never before: a translator returns the whole
		// parameter map and would overwrite anything written ahead of it.
		if issue, invented := webhookPathIssue(catalog, name, id, node, &converted); invented {
			unsupported = append(unsupported, issue)
		}
		// Carried as itself. A disabled node is one the author switched off, and
		// the runtime skips it — passing its input through and never starting a
		// disabled trigger — so importing it as active would start side effects
		// nobody asked for. Nothing is reported: the flag crossed intact.
		converted.Disabled = node.Disabled
		nodes = append(nodes, converted)
	}

	typeByID := make(map[string]string, len(nodes))
	versionByID := make(map[string]workflow.TypeVersion, len(nodes))
	parametersByID := make(map[string]map[string]any, len(nodes))
	settingsByID := make(map[string]map[string]any, len(nodes))
	for _, converted := range nodes {
		typeByID[converted.ID] = converted.Type
		versionByID[converted.ID] = converted.TypeVersion
		parametersByID[converted.ID] = converted.Parameters
		settingsByID[converted.ID] = converted.Settings
	}
	connections, connectionIssues := importConnections(source.Connections, idByName, typeByID, versionByID, parametersByID, settingsByID, catalog)
	unsupported = append(unsupported, connectionIssues...)
	// A hasOutputParser flag whose parser survived as its own node with an
	// ai_outputParser edge is answered by that edge: the converter reports
	// the flag because it sees one node, but the workflow as a whole kept
	// its parser. Leaving the blocking diagnostic in place would claim a
	// wired-up agent returns unparsed text.
	unsupported = dropResolvedParserFlags(unsupported, connections)
	settings, settingIssues := importSettings(source.Settings)
	unsupported = append(unsupported, settingIssues...)
	unsupported = append(unsupported, documentIssues(source)...)

	return ImportResult{
		Document: workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			Name:          name,
			Nodes:         nodes,
			Connections:   connections,
			Settings:      settings,
		},
		Unsupported: withDefaultSeverity(unsupported),
	}, nil
}

// withDefaultSeverity fills in the severity of issues raised by the per-node
// parameter converters.
//
// Those describe a parameter that could not be carried faithfully while the
// node itself still imported, which is exactly `lossy`. Defaulting here rather
// than restating it at each of the thirty-odd converter sites keeps the meaning
// in one place; the two severities that are *not* lossy — a node type with no
// equivalent is blocking, a field nothing reads is dropped — are set explicitly
// where they are raised.
func withDefaultSeverity(issues []ImportIssue) []ImportIssue {
	for index := range issues {
		if issues[index].Severity == "" {
			issues[index].Severity = SeverityLossy
		}
	}
	return issues
}

func withDefaultExportSeverity(issues []ExportIssue) []ExportIssue {
	for index := range issues {
		if issues[index].Severity == "" {
			issues[index].Severity = SeverityLossy
		}
	}
	return issues
}

// documentIssues reports the workflow-level elements KilasFlow does not carry.
//
// FEAT-chxkvq shipped on the principle that an unsupported element is named
// rather than silently applied. The node-level mapping honoured it; these did
// not — `settings`, `pinData` and `meta` were read into the struct and then
// discarded without a word, and `staticData` was not even a field.
//
// `pinData` is dropped rather than parked under a reserved key. Carrying data
// nothing reads would create a second silent-drop problem one release later,
// and the diagnostic is what the user actually needs.
// importSettings carries the workflow settings this product understands: the
// timezone, the workflow's own run budget, and the error workflow.
//
// The timezone matters because a scheduled workflow whose zone was dropped runs
// at the wrong hour every day, and nothing anywhere says why; it was reported
// as uncarried until the Schedule Trigger learned to read it, and the
// diagnostic outlived the gap it described.
func importSettings(source map[string]any) (map[string]any, []ImportIssue) {
	settings := map[string]any{}
	issues := make([]ImportIssue, 0)
	zone, _ := source["timezone"].(string)
	zone = strings.TrimSpace(zone)
	if zone == "" || strings.EqualFold(zone, "DEFAULT") {
		// n8n writes the sentinel "DEFAULT" for "use the instance's zone", and
		// the instance's zone is what applies here too. Carried verbatim it was
		// a literal this server's document validation refuses — so a workflow
		// that relied on n8n's default zone could not even be activated.
	} else if _, err := time.LoadLocation(zone); err != nil {
		// Named rather than carried. A zone this server cannot resolve would
		// fall back to UTC at run time with nothing reporting it, which is the
		// same silent wrong hour by a different route.
		return settings, append(issues, ImportIssue{
			Severity: SeverityLossy, Field: "settings.timezone",
			Reason: fmt.Sprintf("the workflow's timezone %q is not a zone this server knows, so it was not "+
				"carried and schedules will run in UTC", zone),
		})
	} else {
		settings["timezone"] = zone
	}

	// The workflow's own run budget. n8n's -1 means "no timeout", which is not
	// a duration this server can honour — the absence of the key is what means
	// the instance default applies, so -1 is left uncarried and named.
	if timeout, ok := numberParameter(source, "executionTimeout"); ok {
		if timeout > 0 {
			settings["executionTimeout"] = timeout
		} else if timeout < 0 {
			issues = append(issues, ImportIssue{
				Severity: SeverityLossy, Field: "settings.executionTimeout",
				Reason: "this workflow disabled n8n's execution timeout, and this server always applies " +
					"one; the instance's limit applies instead",
			})
		}
	}

	// The workflow to run when this one fails. The engine reads the same
	// setting and starts that workflow from its Error Trigger, so the value is
	// carried as the ID n8n held. What the import cannot know is whether that
	// workflow came into this workspace too — n8n's IDs do not survive the trip
	// unless the whole export was imported — so the caveat is named rather than
	// discovered later as a failure that quietly alerts nobody.
	if target, _ := source["errorWorkflow"].(string); strings.TrimSpace(target) != "" {
		settings["errorWorkflow"] = strings.TrimSpace(target)
		issues = append(issues, ImportIssue{
			Severity: SeverityLossy, Field: "settings.errorWorkflow",
			Reason: "n8n's error workflow is carried as the workflow ID it named; unless that workflow " +
				"was imported into this workspace as well, the reference resolves to nothing",
		})
	}
	return settings, issues
}

// hasUncarriedSettings reports settings beyond the ones importSettings keeps.
func hasUncarriedSettings(source map[string]any) bool {
	for key := range source {
		switch key {
		case "timezone", "executionTimeout", "errorWorkflow":
		default:
			return true
		}
	}
	return false
}

func documentIssues(source Document) []ImportIssue {
	var issues []ImportIssue
	for _, element := range []struct {
		field   string
		present bool
		reason  string
	}{
		{"settings", hasUncarriedSettings(source.Settings),
			"n8n workflow settings beyond the timezone, the execution timeout and the error workflow — " +
				"execution order, the save-data flags and the rest — have no KilasFlow equivalent yet and were not carried"},
		{"pinData", len(source.PinData) > 0,
			"pinned test data is an n8n editor feature with no KilasFlow equivalent; it was not carried, so the nodes that had it pinned will run for real"},
		{"meta", len(source.Meta) > 0,
			"n8n instance metadata describes where the workflow came from and has no meaning here; it was not carried"},
		{"staticData", len(source.StaticData) > 0,
			"n8n's per-workflow static data is scratch space its nodes persist between runs; KilasFlow has no equivalent and it was not carried"},
	} {
		if !element.present {
			continue
		}
		issues = append(issues, ImportIssue{
			Severity: SeverityDropped, Field: element.field, Reason: element.reason,
		})
	}
	return issues
}

// importConnections converts n8n's name-keyed connection map into canonical
// edges.
//
// n8n keys connections by the *source* node's name, and for a typed AI channel
// the source is the sub-node and the target is the node it configures — a chat
// model emits ai_languageModel, an agent receives it. KilasFlow declares the
// same shape, so the loop is already directionally correct for every kind; what
// it needed was the kind itself and ports that exist on both endpoints.
func importConnections(source Connections, idByName, typeByID map[string]string, versionByID map[string]workflow.TypeVersion, parametersByID, settingsByID map[string]map[string]any, catalog workflow.Catalog) ([]workflow.Connection, []Unsupported) {
	connections := make([]workflow.Connection, 0)
	issues := make([]Unsupported, 0)

	sourceNames := make([]string, 0, len(source))
	for name := range source {
		sourceNames = append(sourceNames, name)
	}
	sort.Strings(sourceNames)

	counter := 0
	for _, sourceName := range sourceNames {
		sourceID, known := idByName[sourceName]
		if !known {
			issues = append(issues, Unsupported{
				NodeName: sourceName,
				Reason:   fmt.Sprintf("a connection starts at %q, which is not a node in this workflow; it was dropped", sourceName),
			})
			continue
		}
		kinds := source[sourceName]
		kindNames := make([]string, 0, len(kinds))
		for kind := range kinds {
			kindNames = append(kindNames, kind)
		}
		sort.Strings(kindNames)

		for _, kindName := range kindNames {
			// n8n's connection-kind strings are exactly KilasFlow's
			// ConnectionKind values, so this is an identity check against a
			// closed set rather than a translation. The casing matters:
			// ai_languageModel is camel-cased after the underscore, and nothing
			// in this adapter may normalise a kind or type string.
			kind := workflow.ConnectionKind(kindName)
			if !workflow.KnownConnectionKind(kind) {
				for _, targets := range kinds[kindName] {
					for _, target := range targets {
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason: fmt.Sprintf("the connection from %q to %q on channel %q has no equivalent in KilasFlow and was dropped",
								sourceName, target.Node, kindName),
						})
					}
				}
				continue
			}

			for outputIndex, targets := range kinds[kindName] {
				for _, target := range targets {
					targetID, found := idByName[target.Node]
					if !found {
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason:   fmt.Sprintf("a connection points at %q, which is not a node in this workflow; it was dropped", target.Node),
						})
						continue
					}

					sourcePort, sourceOK := resolvePort(catalog, typeByID[sourceID], versionByID[sourceID], parametersByID[sourceID], kind, outputIndex, portOutput)
					if !sourceOK {
						// A node that continues on a separate error branch has
						// one more output than its type declares, and n8n writes
						// it in the slot just past them. The compiler adds that
						// port to the definition for exactly this reason, so the
						// edge resolves to it rather than being held back.
						if mode, _ := settingsByID[sourceID]["onError"].(string); mode == "continueErrorOutput" &&
							kind == workflow.ConnectionMain &&
							outputIndex == mainOutputCount(catalog, typeByID[sourceID], versionByID[sourceID], parametersByID[sourceID]) {
							sourcePort, sourceOK = errorPortName, true
						}
					}
					targetPort, targetOK := resolvePort(catalog, typeByID[targetID], versionByID[targetID], parametersByID[targetID], kind, target.Index, portInput)
					if !sourceOK || !targetOK {
						// Held back rather than dropped silently: recording an
						// edge onto a port that does not exist would fail
						// compilation with an unknown-port error, which reads
						// as a broken graph rather than as a missing node type.
						missing := sourceName
						if !targetOK {
							missing = target.Node
						}
						issues = append(issues, Unsupported{
							NodeName: sourceName,
							Reason: fmt.Sprintf("the %q connection from %q to %q was held back because %q declares no %s port for it",
								kindName, sourceName, target.Node, missing, kindName),
						})
						continue
					}

					counter++
					connections = append(connections, workflow.Connection{
						ID:     fmt.Sprintf("n8n-c%d", counter),
						Kind:   kind,
						Source: workflow.Endpoint{NodeID: sourceID, Port: sourcePort},
						Target: workflow.Endpoint{NodeID: targetID, Port: targetPort},
					})
				}
			}
		}
	}
	return connections, issues
}

// dropResolvedParserFlags removes a hasOutputParser diagnostic when the
// parser it names survived the import as its own node with an ai_outputParser
// edge onto the flagged node. The per-node converter reports the flag
// because it sees one node; only the whole graph knows the parser made it.
func dropResolvedParserFlags(issues []Unsupported, connections []workflow.Connection) []Unsupported {
	wired := make(map[string]bool, len(connections))
	for _, connection := range connections {
		if connection.Kind == workflow.ConnectionOutputParser {
			wired[connection.Target.NodeID] = true
		}
	}
	if len(wired) == 0 {
		return issues
	}
	kept := issues[:0]
	for _, issue := range issues {
		if issue.Field == "hasOutputParser" && wired[issue.NodeID] {
			continue
		}
		kept = append(kept, issue)
	}
	return kept
}

// portDirection selects which side of a definition resolvePort reads.
type portDirection int

const (
	portOutput portDirection = iota
	portInput
)

// resolvePort finds the canonical port an n8n endpoint refers to.
//
// n8n identifies an endpoint by kind and index. For the item channel the index
// is positional and meaningful — IF's second output is its false branch. For a
// typed AI channel it is not: a node has exactly one port per AI kind, and n8n
// itself always writes index 0, so the first declared port of that kind is the
// answer.
//
// It asks the registry rather than a table. The adapter already has to know
// canonical port names to be correct, and a second hardcoded table is how the
// existing pair of helpers came to need a comment explaining that one is the
// inverse of the other. Going through the catalogue is also what keeps this
// working when generated node packs arrive with ports nobody hardcoded.
func resolvePort(catalog workflow.Catalog, nodeType string, version workflow.TypeVersion, parameters map[string]any, kind workflow.ConnectionKind, index int, direction portDirection) (string, bool) {
	// A source node whose outputs changed meaning between its own versions
	// cannot be resolved from the canonical port list alone: the list
	// describes the *current* node, and the edge was drawn against an older
	// one. This is asked before the catalogue, so it holds with or without
	// one.
	if name, remapped := legacyOutputPort(nodeType, version, kind, direction, index); remapped {
		return name, true
	}
	if catalog == nil {
		// No catalogue: fall back to the positional names, which is what the
		// adapter did before it could ask. Only the item channel is nameable
		// this way.
		if kind != workflow.ConnectionMain {
			return "", false
		}
		if direction == portOutput {
			return outputPortName(nodeType, index), true
		}
		return inputPortName(nodeType, index), true
	}
	definition, found := catalog.Lookup(nodeType, version)
	if !found {
		return "", false
	}
	// A node whose ports depend on its own configuration is asked, exactly as
	// the compiler asks it. A Switch's static list is the one port a picker
	// shows for an unconfigured node; the rules decide the real count, and
	// resolving against the static list would collapse every branch onto the
	// first wire.
	if definition.PortsFor != nil {
		inputs, outputs := definition.PortsFor(parameters, version)
		definition.Inputs, definition.Outputs = inputs, outputs
	}
	declared := definition.Outputs
	if direction == portInput {
		declared = definition.Inputs
	}
	matching := make([]workflow.Port, 0, len(declared))
	for _, port := range declared {
		if port.Kind == kind {
			matching = append(matching, port)
		}
	}
	if len(matching) == 0 {
		return "", false
	}
	if kind != workflow.ConnectionMain {
		// One port per AI kind; n8n always writes index 0.
		return matching[0].Name, true
	}
	if index < 0 || index >= len(matching) {
		return "", false
	}
	return matching[index].Name, true
}

// legacyOutputPort resolves an n8n output index whose meaning changed between
// versions of the source node, for the mappings where it did.
//
// It is a small, explicit table rather than a per-mapping hook, because there
// is exactly one such node today and the table is the place a second one is
// added — with the version comparison that makes it right, next to the comment
// that says why. A remap that a mapping forgot to declare is a silently
// rewired workflow, which is the defect this exists to close.
func legacyOutputPort(kilasType string, version workflow.TypeVersion, kind workflow.ConnectionKind, direction portDirection, index int) (string, bool) {
	if kind != workflow.ConnectionMain || direction != portOutput {
		return "", false
	}
	switch kilasType {
	case LoopNodeType:
		// n8n's Split In Batches v1 and v2 have a single output carrying each
		// batch; v3 split it into `done` (index 0) and `loop` (index 1).
		// Resolving positionally put the loop body on `done`, so the body ran
		// once at the end — when it ran at all: the compiler usually refused
		// the graph first, with a message about scheduling rather than about
		// the loop.
		if version.Compare(workflow.V(3)) < 0 && index == 0 {
			return "loop", true
		}
	}
	return "", false
}

// outputPortName maps an n8n output index onto a canonical port name.
//
// n8n identifies outputs positionally; KilasFlow names them, and the names
// differ per node — IF has `true`/`false` where everything else has `main`.
// The mapping is therefore driven by the node's canonical type rather than by
// the index alone, so a branch cannot be wired to a port that does not exist.
// It is the exact inverse of outputIndexesFor, used on export.
func outputPortName(kilasType string, index int) string {
	ports := outputPortsFor(kilasType)
	if index >= 0 && index < len(ports) {
		return ports[index]
	}
	return fmt.Sprintf("output%d", index)
}

func outputPortsFor(kilasType string) []string {
	if kilasType == "kilasflow.if" {
		return []string{"true", "false"}
	}
	return []string{"main"}
}

func inputPortName(kilasType string, index int) string {
	ports := inputPortsFor(kilasType)
	if index >= 0 && index < len(ports) {
		return ports[index]
	}
	return fmt.Sprintf("input%d", index+1)
}

func inputPortsFor(kilasType string) []string {
	if kilasType == "kilasflow.merge" {
		return []string{"input1", "input2"}
	}
	return []string{"main"}
}

func positionFrom(position []float64) workflow.Position {
	if len(position) < 2 {
		return workflow.Position{}
	}
	return workflow.Position{X: position[0], Y: position[1]}
}

// ExportResult is one converted document plus everything it could not carry.
type ExportResult struct {
	Document Document
	Lossy    []Lossy
}

// Export converts a canonical KilasFlow document into n8n workflow JSON.
//
// The catalogue is what turns a named output port back into n8n's positional
// index. A hardcoded table could only ever describe the nodes somebody wrote it
// for: a generated pack's trigger has twenty-six outputs nobody hardcoded, and
// collapsing them onto slot zero would send every branch of an exported
// workflow to the same wire. A nil catalogue falls back to the positional
// names, which is what this did before it could ask.
func Export(document workflow.Document, catalog workflow.Catalog) (ExportResult, error) {
	result := ExportResult{
		Document: Document{
			Name:        document.Name,
			Nodes:       make([]Node, 0, len(document.Nodes)),
			Connections: Connections{},
			Settings:    exportSettings(document.Settings),
		},
		Lossy: make([]Lossy, 0),
	}

	nameByID := make(map[string]string, len(document.Nodes))
	typeByID := make(map[string]string, len(document.Nodes))
	portIndex := make(map[string]map[string]int, len(document.Nodes))
	inputIndex := make(map[string]map[string]int, len(document.Nodes))

	for _, node := range document.Nodes {
		nameByID[node.ID] = node.Name
		typeByID[node.ID] = node.Type

		if node.Type == UnsupportedNodeType {
			// Round-tripping the placeholder back to its original n8n identity
			// is the honest thing: the node came from n8n and belongs there.
			// Everything the capsule kept is handed back, not just the
			// parameters — a node that returns without its credentials, its
			// notes or its retry policy is a node that quietly changed.
			originalType, _ := node.Parameters["originalType"].(string)
			originalVersion, _ := node.Parameters["originalTypeVersion"].(float64)
			exported := restoreCapsule(node.Parameters["original"])
			exported.ID = node.ID
			exported.Name = node.Name
			exported.Position = []float64{node.Position.X, node.Position.Y}
			if exported.Type == "" {
				exported.Type = originalType
			}
			if exported.TypeVersion == 0 {
				exported.TypeVersion = originalVersion
			}
			result.Document.Nodes = append(result.Document.Nodes, exported)
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name,
				Reason:   fmt.Sprintf("this node was imported from n8n as unsupported; it was exported back as %q with everything the import preserved", originalType),
			})
			// The placeholder's own ports, so a multi-output node keeps its
			// branches. Falling through without this sent every outgoing edge
			// to n8n output 0, silently rewiring a Switch so that all its
			// branches left the first slot.
			portIndex[node.ID] = placeholderOutputIndexes(node)
			inputIndex[node.ID] = inputIndexesFor(catalog, node.Type, node.TypeVersion, node.Parameters)
			continue
		}

		entry, supported := byKilasType(node.Type)
		if !supported {
			// Emitted under its own type rather than omitted. n8n does not
			// know this type and will show it as unrecognised and refuse to
			// run the workflow, which is the honest outcome: the graph keeps
			// its shape and its edges, and the one node that cannot work says
			// so where it is.
			//
			// Omitting it was worse in a way nobody would notice. Every edge
			// touching the node went with it, so a linear workflow came out as
			// two disconnected halves and an n8n user would see a workflow
			// that looked complete and ran only the first part.
			result.Document.Nodes = append(result.Document.Nodes, Node{
				ID: node.ID, Name: node.Name, Type: node.Type,
				TypeVersion: node.TypeVersion.Float(),
				Position:    []float64{node.Position.X, node.Position.Y},
				Parameters:  node.Parameters,
			})
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name,
				Reason: fmt.Sprintf("n8n has no equivalent of the KilasFlow node %q; it was exported under its "+
					"own type so the workflow keeps its shape, and n8n will not recognise it — replace it "+
					"there before running the workflow", node.Type),
			})
			// Its own ports, so a multi-output node keeps its branches rather
			// than sending every outgoing edge to n8n output 0.
			portIndex[node.ID] = outputIndexesFor(catalog, node.Type, node.TypeVersion, node.Parameters)
			inputIndex[node.ID] = inputIndexesFor(catalog, node.Type, node.TypeVersion, node.Parameters)
			continue
		}

		version := exportVersion(entry, node)
		exported := Node{
			ID: node.ID, Name: node.Name, Type: entry.n8nType, TypeVersion: version,
			Position: []float64{node.Position.X, node.Position.Y},
		}
		// A node that arrived at a version this mapping cannot write back is
		// named, not silently re-versioned. The two sides' version numbers mean
		// different things for a core node, so the comparison is only meaningful
		// for a node carrying its own n8n version — which is exactly the case
		// where a version change is a behaviour change nobody asked for.
		if !entry.sharedVersion && !node.TypeVersion.IsZero() && node.TypeVersion != entry.kilasVersion &&
			version != node.TypeVersion.Float() {
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name, Field: "typeVersion", Severity: SeverityLossy,
				Reason: fmt.Sprintf("this node was authored at n8n typeVersion %s and was exported at %s: "+
					"the translator writes that version's parameter shape, and n8n does not publish the "+
					"original. Check the node's settings in n8n after importing the file.",
					node.TypeVersion.String(), strconv.FormatFloat(version, 'g', -1, 64)),
			})
		}
		// The error handling goes back out with it. It used to be left behind,
		// and the asymmetry was the sharp part: a node with no n8n equivalent
		// round-tripped faithfully because its capsule kept everything, while
		// a node this server *supports* came back having quietly lost its
		// retry policy. A workflow whose HTTP node retried three times and
		// continued on failure returned to n8n as one that fails the whole run
		// on the first error.
		applyErrorHandling(&exported, node.Settings)
		// A node the author switched off goes back switched off.
		exported.Disabled = node.Disabled
		if entry.toN8N != nil {
			parameters, issues := entry.toN8N(node)
			exported.Parameters = parameters
			for _, issue := range issues {
				issue.NodeName = node.Name
				result.Lossy = append(result.Lossy, issue)
			}
		}
		if len(node.Credentials) > 0 {
			// Credential *references* are KilasFlow IDs and mean nothing in an
			// n8n instance, so they are named as lost rather than emitted as
			// broken references.
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: node.Name, Field: "credentials",
				Reason: "credential references are KilasFlow identifiers and were not exported; reattach credentials in n8n",
			})
		}
		result.Document.Nodes = append(result.Document.Nodes, exported)
		portIndex[node.ID] = outputIndexesFor(catalog, node.Type, node.TypeVersion, node.Parameters)
		inputIndex[node.ID] = inputIndexesFor(catalog, node.Type, node.TypeVersion, node.Parameters)
	}

	exported := map[string]bool{}
	for _, node := range result.Document.Nodes {
		exported[node.Name] = true
	}

	// The AI sub-node flags are decided by the graph, not by the node, so they
	// are written after every node exists. n8n computes a root node's inputs
	// from hasOutputParser and needsFallback, and its default is false — so an
	// exported agent with an ai_outputParser edge but no flag offers no input
	// for that edge, and n8n drops the parser on open. The import side already
	// reports the flag; this is the other half.
	result.Document.Nodes = applySubnodeFlags(result.Document.Nodes, document.Connections)

	for _, connection := range document.Connections {
		sourceName, sourceKnown := nameByID[connection.Source.NodeID]
		targetName, targetKnown := nameByID[connection.Target.NodeID]
		if !sourceKnown || !targetKnown || !exported[sourceName] || !exported[targetName] {
			result.Lossy = append(result.Lossy, Lossy{
				Reason: "a connection referenced a node that was not exported and was dropped with it",
			})
			continue
		}
		if !workflow.KnownConnectionKind(connection.Kind) {
			result.Lossy = append(result.Lossy, Lossy{
				NodeName: sourceName,
				Reason: fmt.Sprintf("the connection from %q to %q on channel %q has no n8n equivalent and was dropped",
					sourceName, targetName, connection.Kind),
			})
			continue
		}

		channel := string(connection.Kind)
		// A typed AI channel carries exactly one port per kind and n8n always
		// writes slot zero, so only the item channel is positional.
		index := 0
		targetIndex := 0
		if connection.Kind == workflow.ConnectionMain {
			if indexes, known := portIndex[connection.Source.NodeID]; known {
				if position, found := indexes[connection.Source.Port]; found {
					index = position
				}
			}
			if connection.Source.Port == errorPortName {
				// One past the declared outputs, which is where n8n writes the
				// error branch of a node that continues on a separate output.
				// The map holds exactly the positional item ports, so its
				// length is the index of the slot after them.
				if indexes, known := portIndex[connection.Source.NodeID]; known {
					index = len(indexes)
				}
			}
			targetIndex = inputIndexFor(typeByID[connection.Target.NodeID], connection.Target.Port)
			if indexes, known := inputIndex[connection.Target.NodeID]; known {
				if position, found := indexes[connection.Target.Port]; found {
					targetIndex = position
				}
			}
		}
		if result.Document.Connections[sourceName] == nil {
			result.Document.Connections[sourceName] = map[string][][]Target{}
		}
		slots := result.Document.Connections[sourceName][channel]
		for len(slots) <= index {
			slots = append(slots, []Target{})
		}
		slots[index] = append(slots[index], Target{
			Node: targetName, Type: channel, Index: targetIndex,
		})
		result.Document.Connections[sourceName][channel] = slots
	}

	result.Lossy = withDefaultExportSeverity(result.Lossy)
	return result, nil
}

// applySubnodeFlags writes the boolean flags n8n derives a root node's inputs
// from.
//
// An n8n agent or chain declares its Output Parser input only when
// hasOutputParser is true, and its fallback model input only when needsFallback
// is. Both default to false, so a node exported with the wired-up sub-node but
// without the flag comes back in n8n with an edge onto an input that does not
// exist — and structured output silently stops working.
func applySubnodeFlags(nodes []Node, connections []workflow.Connection) []Node {
	parsers := map[string]bool{}
	models := map[string]int{}
	for _, connection := range connections {
		switch connection.Kind {
		case workflow.ConnectionOutputParser:
			parsers[connection.Target.NodeID] = true
		case workflow.ConnectionLanguageModel:
			models[connection.Target.NodeID]++
		}
	}
	if len(parsers) == 0 && len(models) == 0 {
		return nodes
	}
	for index := range nodes {
		parameters := nodes[index].Parameters
		if parameters == nil {
			continue
		}
		switch nodes[index].Type {
		case "kilasflow.agent", "kilasflow.chainLlm":
		default:
			continue
		}
		if parsers[nodes[index].ID] {
			parameters["hasOutputParser"] = true
		}
		// A second model is the fallback one: n8n's needsFallback is exactly
		// "there is more than one model attached".
		if models[nodes[index].ID] > 1 {
			parameters["needsFallback"] = true
		}
	}
	return nodes
}

// exportVersion is the typeVersion written back out.
//
// A shared-version mapping writes the node's own: the WAHA node is registered
// at the versions the package it mirrors publishes, and exporting both as one
// number would send a workflow back claiming a version it was not authored at.
func exportVersion(entry mapping, node workflow.Node) float64 {
	return raiseVersion(entry, node, pinnedVersion(entry, node))
}

// pinnedVersion is the version the mapping's own rules choose.
func pinnedVersion(entry mapping, node workflow.Node) float64 {
	if node.TypeVersion.IsZero() {
		return entry.exportTypeVersion
	}
	if entry.sharedVersion {
		return node.TypeVersion.Float()
	}
	// A node sitting at exactly the version this server registers was authored
	// here, and gets the pin — the version whose n8n behaviour matches what
	// this server does. Anything else came in from n8n carrying its own
	// number, which is preserved: a node imported at 2.7 and exported at the
	// pin would go back with different DATE handling than it arrived with, a
	// silent behaviour change with nothing in the diff to show for it.
	if node.TypeVersion == entry.kilasVersion {
		return entry.exportTypeVersion
	}
	// Only if n8n actually publishes it. n8n's getNodeType is an exact map
	// lookup with no resolve-down, so a version it does not have makes it
	// refuse to open the file rather than fall back.
	for _, published := range entry.publishedVersions {
		if published == node.TypeVersion.Float() {
			return published
		}
	}
	return entry.exportTypeVersion
}

// raiseVersion lifts the written version to the oldest one whose n8n node can
// represent this configuration.
//
// Raising only, never lowering: a node imported at a version n8n publishes goes
// back at that version, and the configuration that needs a newer node than the
// pin is the only thing that moves the number.
func raiseVersion(entry mapping, node workflow.Node, version float64) float64 {
	if entry.minimumExportVersion == nil {
		return version
	}
	if required := entry.minimumExportVersion(node); required > version {
		return required
	}
	return version
}

// errorPortName is the extra output a node that continues on a separate error
// branch declares. Mirrored from the compiler's own name so an imported error
// edge lands on the port the runner fills.
const errorPortName = "error"

// mainOutputCount is how many positional item outputs a node type declares.
//
// The error output sits in the slot just past them, which is how n8n numbers
// it: a node with one output writes its error branch at index 1.
func mainOutputCount(catalog workflow.Catalog, nodeType string, version workflow.TypeVersion, parameters map[string]any) int {
	return len(outputIndexesFor(catalog, nodeType, version, parameters))
}

// outputIndexesFor maps a canonical node's named output ports onto n8n's
// positional ones. It is the inverse of outputPortsFor, so a round trip lands
// on the same branch it started from.
func outputIndexesFor(catalog workflow.Catalog, nodeType string, version workflow.TypeVersion, parameters map[string]any) map[string]int {
	indexes := map[string]int{}
	if catalog != nil {
		if definition, found := catalog.Lookup(nodeType, version); found {
			if definition.PortsFor != nil {
				_, definition.Outputs = definition.PortsFor(parameters, version)
			}
			position := 0
			for _, port := range definition.Outputs {
				// Only the item channel is positional; a typed AI channel
				// always writes slot zero.
				if port.Kind != workflow.ConnectionMain {
					continue
				}
				indexes[port.Name] = position
				position++
			}
			return indexes
		}
	}
	for index, port := range outputPortsFor(nodeType) {
		indexes[port] = index
	}
	return indexes
}

// inputIndexesFor maps a canonical node's named input ports onto n8n's
// positional ones.
//
// It asks the catalogue for the same reason the output side does, and the
// defect it closes is the same shape: the static table only knew input1 and
// input2, so every edge into a Merge's third and later inputs was written to
// slot 0. A nine-input Merge exported as a Merge with one input carrying all
// nine wires — a workflow that looks complete and combines the wrong items.
func inputIndexesFor(catalog workflow.Catalog, nodeType string, version workflow.TypeVersion, parameters map[string]any) map[string]int {
	indexes := map[string]int{}
	if catalog != nil {
		if definition, found := catalog.Lookup(nodeType, version); found {
			if definition.PortsFor != nil {
				definition.Inputs, _ = definition.PortsFor(parameters, version)
			}
			position := 0
			for _, port := range definition.Inputs {
				// Only the item channel is positional; a typed AI channel
				// always reads slot zero.
				if port.Kind != workflow.ConnectionMain {
					continue
				}
				indexes[port.Name] = position
				position++
			}
			return indexes
		}
	}
	for index, port := range inputPortsFor(nodeType) {
		indexes[port] = index
	}
	return indexes
}

func inputIndexFor(nodeType, port string) int {
	for index, candidate := range inputPortsFor(nodeType) {
		if candidate == port {
			return index
		}
	}
	return 0
}

// nodeArity is how many input and output slots a workflow's connections
// actually use on one node.
type nodeArity struct {
	inputs  int
	outputs int
}

// observedArity counts the slots each node is wired on.
//
// n8n identifies a slot positionally, so the only evidence of how many a node
// has is the highest index some edge uses. A node wired on outputs 0 and 2 has
// at least three, even though nothing touches output 1.
func observedArity(connections Connections) map[string]nodeArity {
	arity := make(map[string]nodeArity, len(connections))
	widen := func(name string, inputs, outputs int) {
		current := arity[name]
		if inputs > current.inputs {
			current.inputs = inputs
		}
		if outputs > current.outputs {
			current.outputs = outputs
		}
		arity[name] = current
	}
	for sourceName, kinds := range connections {
		for _, slots := range kinds {
			for outputIndex, targets := range slots {
				if len(targets) > 0 {
					widen(sourceName, 0, outputIndex+1)
				}
				for _, target := range targets {
					widen(target.Node, target.Index+1, 0)
				}
			}
		}
	}
	return arity
}

// unsupportedArities mirrors nodes.UnsupportedArities. The adapter must not
// depend on the node pack, so the family is duplicated and pinned by
// TestPlaceholderArityFamilyMatchesTheNodePack.
var unsupportedArities = []int{1, 2, 4, 8}

// unsupportedArityFor is the smallest registered placeholder arity covering a
// node wired on this many slots. A node beyond the largest member is clamped;
// import reports the truncation rather than emitting an edge the compiler will
// reject.
func unsupportedArityFor(inputs, outputs int) int {
	needed := inputs
	if outputs > needed {
		needed = outputs
	}
	if needed < 1 {
		needed = 1
	}
	for _, arity := range unsupportedArities {
		if arity >= needed {
			return arity
		}
	}
	return unsupportedArities[len(unsupportedArities)-1]
}

// capsule is the whole source node as a structured value.
//
// It is stored as an object rather than a marshalled string: the previous shape
// escaped JSON inside JSON, which made the value unreadable in the editor and
// bought nothing. Round-tripping through the node's own JSON tags keeps the
// field names identical to what n8n wrote, so an export can hand them straight
// back.
func capsule(node Node) map[string]any {
	encoded, err := json.Marshal(node)
	if err != nil {
		// Node holds only JSON-native types, so this cannot fail; falling back
		// to identity alone still preserves more than dropping the node.
		return map[string]any{"type": node.Type, "typeVersion": node.TypeVersion}
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return map[string]any{"type": node.Type, "typeVersion": node.TypeVersion}
	}
	return decoded
}

// placeholderFor preserves a node that cannot run here as a visible
// placeholder that blocks activation. The whole source node stays in the
// capsule so a round trip returns it intact; the reason names what was
// refused and why.
func placeholderFor(converted *workflow.Node, name, id string, node Node, arityByName map[string]nodeArity, reason string) ImportIssue {
	arity := arityByName[name]
	converted.Type = UnsupportedNodeType
	converted.TypeVersion = workflow.V(unsupportedArityFor(arity.inputs, arity.outputs))
	converted.Parameters = map[string]any{
		"originalType":        node.Type,
		"originalTypeVersion": node.TypeVersion,
		"original":            capsule(node),
	}
	return ImportIssue{
		Severity: SeverityBlocking,
		NodeName: name, NodeID: id, Type: node.Type, TypeVersion: sourceTypeVersion(node.TypeVersion),
		Reason: reason,
	}
}

// restoreCapsule turns a stored capsule back into the node n8n wrote.
//
// It round-trips through the same JSON tags the capsule was built from, so a
// field added to Node is carried in both directions without a second list to
// keep in step. An unreadable capsule yields a zero Node and the caller falls
// back to the identity parameters, which is why those are still stored
// separately.
func restoreCapsule(stored any) Node {
	if stored == nil {
		return Node{}
	}
	// Workflows imported before the capsule became structured hold it as a
	// JSON *string*. Reading both shapes costs one type switch and is the
	// difference between those workflows exporting whole and exporting
	// stripped of their parameters.
	if text, ok := stored.(string); ok {
		var node Node
		if err := json.Unmarshal([]byte(text), &node); err != nil {
			return Node{}
		}
		return node
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return Node{}
	}
	var node Node
	if err := json.Unmarshal(encoded, &node); err != nil {
		return Node{}
	}
	return node
}

// placeholderOutputIndexes maps a placeholder's port names back to n8n output
// slots. Its arity is its type version, which is how the family is registered.
func placeholderOutputIndexes(node workflow.Node) map[string]int {
	arity, err := strconv.Atoi(node.TypeVersion.String())
	if err != nil || arity < 1 {
		arity = 1
	}
	indexes := make(map[string]int, arity)
	for index := 0; index < arity; index++ {
		indexes[outputPortName(UnsupportedNodeType, index)] = index
	}
	return indexes
}

// formatN8NVersion renders an n8n typeVersion as the decimal text
// workflow.ParseTypeVersion reads.
//
// n8n's typeVersion arrives as a JSON number and is held as a float64, which is
// the one place a float is unavoidable. Formatting it with %g rather than
// reading the float directly keeps 4.2 from becoming 4.199999999999999 on the
// way into a fixed-point version.
func formatN8NVersion(version float64) string {
	if version <= 0 {
		return ""
	}
	return strconv.FormatFloat(version, 'g', -1, 64)
}

// sourceTypeVersion reads an n8n typeVersion into KilasFlow's fixed-point form,
// yielding the unset version when n8n gave nothing usable.
func sourceTypeVersion(version float64) workflow.TypeVersion {
	parsed, err := workflow.ParseTypeVersion(formatN8NVersion(version))
	if err != nil {
		return workflow.TypeVersion{}
	}
	return parsed
}

// nodeIssues reports the per-node elements a mapped node loses on import.
//
// The error-handling set is `dropped`, not `lossy`, and the distinction is the
// point: the runner does not honour continueOnFail or retryOnFail yet, so
// calling them lossy would imply a retry policy was applied in some reduced
// form when it was ignored entirely. When the runner learns them, these become
// carried and the diagnostics go away — which is exactly the signal a later
// ticket wants.
func nodeIssues(name, id string, node Node) []ImportIssue {
	var issues []ImportIssue
	add := func(field, reason string) {
		issues = append(issues, ImportIssue{
			Severity: SeverityDropped,
			NodeName: name, NodeID: id, Field: field,
			Type: node.Type, TypeVersion: sourceTypeVersion(node.TypeVersion),
			Reason: reason,
		})
	}

	if strings.TrimSpace(node.Notes) != "" {
		add("notes", "the node's note was parsed but has no KilasFlow equivalent and was not carried")
	}
	if strings.TrimSpace(node.WebhookID) != "" {
		add("webhookId", "n8n's per-node webhook identity is meaningless in this installation; KilasFlow assigns its own webhook binding on activation")
	}

	// continueOnFail, retryOnFail, maxTries, waitBetweenTries, alwaysOutputData,
	// executeOnce, onError and the disabled flag are all carried onto the
	// canonical node — the settings by errorHandlingSettings, the flag on the
	// node itself — so none of them is reported here. A diagnostic that says a
	// setting was dropped when it crossed intact is worse than no diagnostic.
	return issues
}

// errorHandlingSettings carries n8n's error handling onto the canonical
// settings the runner now honours.
//
// Mapping these before the runner read them would have turned a silent drop
// into a documented lie, which is why the importer deliberately did not read
// them until this point.
func errorHandlingSettings(node Node) map[string]any {
	settings := map[string]any{}
	if node.ContinueOnFail {
		settings["continueOnFail"] = true
	}
	// Current n8n writes `onError` instead of the legacy boolean. The mode
	// crosses verbatim — the runner executes all three — and continueOnFail
	// keeps its own key for a document that carries the old boolean.
	if mode := strings.TrimSpace(node.OnError); mode != "" {
		settings["onError"] = mode
		if mode == "continueRegularOutput" {
			settings["continueOnFail"] = true
		}
	}
	if node.AlwaysOutputData {
		settings["alwaysOutputData"] = true
	}
	if node.ExecuteOnce {
		settings["executeOnce"] = true
	}
	if node.RetryOnFail {
		settings["retryOnFail"] = true
	}
	if node.MaxTries > 0 {
		// Clamped rather than refused: an n8n workflow with a higher retry
		// budget should still import, and the cap is what stops one typo
		// becoming thousands of calls.
		tries := node.MaxTries
		if tries > workflow.MaxRetryAttempts {
			tries = workflow.MaxRetryAttempts
		}
		settings["maxTries"] = tries
	}
	if node.WaitBetweenTries > 0 {
		wait := node.WaitBetweenTries
		if wait > workflow.MaxRetryWaitMilliseconds {
			wait = workflow.MaxRetryWaitMilliseconds
		}
		settings["waitBetweenTries"] = wait
	}
	return settings
}

// exportSettings writes the workflow settings n8n understands.
//
// Only the timezone, which is the one the importer deliberately carries — a
// scheduled workflow whose zone is dropped runs at the wrong hour, every day,
// and nothing about the file says why. Export initialised this map empty and
// never filled it, so the zone survived the journey in and was thrown away on
// the journey out.
//
// Keys this server invented are not written back: n8n would ignore them, and a
// document carrying settings the receiving system does not understand is how
// two formats start diverging.
func exportSettings(settings map[string]any) map[string]any {
	exported := map[string]any{}
	if zone, _ := settings["timezone"].(string); strings.TrimSpace(zone) != "" {
		exported["timezone"] = zone
	}
	if timeout := numberSetting(settings["executionTimeout"]); timeout > 0 {
		exported["executionTimeout"] = timeout
	}
	// The error workflow, as the ID this workspace holds. n8n reads it the same
	// way it wrote it; a workflow that references a workflow of this workspace
	// is only meaningful to an n8n instance that holds it, which is the same
	// caveat the import reports in the other direction.
	if target, _ := settings["errorWorkflow"].(string); strings.TrimSpace(target) != "" {
		exported["errorWorkflow"] = strings.TrimSpace(target)
	}
	return exported
}

// applyErrorHandling writes the canonical error settings back onto an n8n node.
//
// The exact inverse of errorHandlingSettings, and deliberately next to it: the
// two read and write the same four keys, and a rename in one that missed the
// other would lose a retry policy silently — which is the defect this pair
// exists to close.
func applyErrorHandling(exported *Node, settings map[string]any) {
	if settings == nil {
		return
	}
	if mode, _ := settings["onError"].(string); mode != "" {
		exported.OnError = mode
	}
	if flag, _ := settings["continueOnFail"].(bool); flag {
		// n8n 1.x reads the legacy boolean and 2.x reads onError. Writing the
		// modern spelling as well is what makes the round trip a no-op on a
		// current instance: a node that arrived with continueRegularOutput must
		// not go back with a setting current n8n ignores.
		exported.ContinueOnFail = true
		if exported.OnError == "" {
			exported.OnError = "continueRegularOutput"
		}
	}
	if flag, _ := settings["alwaysOutputData"].(bool); flag {
		exported.AlwaysOutputData = true
	}
	if flag, _ := settings["executeOnce"].(bool); flag {
		exported.ExecuteOnce = true
	}
	if flag, _ := settings["retryOnFail"].(bool); flag {
		exported.RetryOnFail = true
	}
	if tries := numberSetting(settings["maxTries"]); tries > 0 {
		exported.MaxTries = tries
	}
	if wait := numberSetting(settings["waitBetweenTries"]); wait > 0 {
		exported.WaitBetweenTries = wait
	}
}

// numberSetting reads a stored number whatever shape JSON left it in.
//
// A document that has been through storage carries float64; one built in
// memory may carry an int. Reading only one of them would work in a test and
// lose the value in production, which is the wrong way round.
func numberSetting(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

// versionIssue reports a source typeVersion the catalogue does not register.
//
// Only for a shared-version mapping, and that restriction is the whole design.
// KilasFlow's core nodes are registered at 1 while the n8n nodes they mirror are
// at 3.4 and 1.2, so the numbers mean different things and resolving down is
// exactly right there — reporting it would put a blocking issue on every
// perfectly good import. A shared-version mapping is the case where the two
// sides use the *same* numbering, so a version the catalogue does not have is a
// genuinely different node: a WAHA workflow authored at 202409 that quietly
// landed on 202502 would be wired against a different event order.
func versionIssue(catalog workflow.Catalog, entry mapping, name, id string, node Node, converted workflow.Node) (ImportIssue, bool) {
	if catalog == nil || !entry.sharedVersion || converted.TypeVersion.IsZero() {
		return ImportIssue{}, false
	}
	// Lookup resolves *down* to the highest registered version, which is right
	// for a workflow authored against a newer minor of an unchanged node and
	// silently wrong for one registered at distinct, non-additive versions. The
	// returned definition's own version is what says which happened.
	if definition, found := catalog.Lookup(converted.Type, converted.TypeVersion); found &&
		definition.Version.Compare(converted.TypeVersion) == 0 {
		return ImportIssue{}, false
	}
	typed, reports := catalog.(workflow.TypeCatalog)
	if !reports || !typed.HasType(converted.Type) {
		// The type itself is unknown here; the unsupported branch already
		// reported that, or the catalogue is too thin to say more.
		return ImportIssue{}, false
	}
	return ImportIssue{
		Severity: SeverityBlocking,
		NodeName: name, NodeID: id, Field: "typeVersion",
		Type: node.Type, TypeVersion: sourceTypeVersion(node.TypeVersion),
		Reason: fmt.Sprintf(
			"this workflow uses %s version %s, which this installation does not have. It was imported at that version and will not run until the version is installed or the node is changed.",
			node.Type, converted.TypeVersion),
	}, true
}

// credentialIssue names the credential a node expected without carrying it.
//
// An n8n credential reference is `{id, name}` scoped to the instance it came
// from: the id names a row in somebody else's database and means nothing here.
// Dropping it is right. Dropping it *silently* is not — the node then looks
// configured and fails at run time — so the name the workflow was authored
// against is reported, and the node arrives visibly unbound.
func credentialIssue(name, id string, node Node) (ImportIssue, bool) {
	if len(node.Credentials) == 0 {
		return ImportIssue{}, false
	}
	described := make([]string, 0, len(node.Credentials))
	for credentialType, value := range node.Credentials {
		display := ""
		if reference, ok := value.(map[string]any); ok {
			display, _ = reference["name"].(string)
		}
		if display == "" {
			described = append(described, credentialType)
			continue
		}
		described = append(described, fmt.Sprintf("%s %q", credentialType, display))
	}
	sort.Strings(described)
	return ImportIssue{
		Severity: SeverityBlocking,
		NodeName: name, NodeID: id, Field: "credentials",
		Type: node.Type, TypeVersion: sourceTypeVersion(node.TypeVersion),
		Reason: fmt.Sprintf(
			"this node used %s in n8n. Credential identifiers belong to the instance they came from, so the node was imported unbound: attach a local credential before activating.",
			strings.Join(described, ", ")),
	}, true
}

// webhookPathIssue gives an imported trigger the route label KilasFlow needs.
//
// n8n mints its own webhook route and stores it as an opaque `webhookId`, so a
// trigger imported from it carries no path at all — and a KilasFlow trigger with
// no path binds no route, which means the workflow activates and receives
// nothing. That is the exact failure this whole phase is about, so a label is
// invented from the node's own name rather than left empty.
//
// The label is only a label: the public URL is a minted opaque route either way.
// It is reported because a value the user did not write should never appear in
// their workflow silently.
func webhookPathIssue(catalog workflow.Catalog, name, id string, node Node, converted *workflow.Node) (ImportIssue, bool) {
	if catalog == nil {
		return ImportIssue{}, false
	}
	definition, found := catalog.Lookup(converted.Type, converted.TypeVersion)
	if !found || definition.WebhookPathParameter == "" {
		return ImportIssue{}, false
	}
	key := definition.WebhookPathParameter
	if existing, present := converted.Parameters[key]; present {
		if text, ok := existing.(string); !ok || strings.TrimSpace(text) != "" {
			return ImportIssue{}, false
		}
	}
	label := routeLabel(name)
	if label == "" {
		label = routeLabel(id)
	}
	if label == "" {
		return ImportIssue{}, false
	}
	if converted.Parameters == nil {
		converted.Parameters = map[string]any{}
	}
	converted.Parameters[key] = label
	return ImportIssue{
		Severity: SeverityDropped,
		NodeName: name, NodeID: id, Field: key,
		Type: node.Type, TypeVersion: sourceTypeVersion(node.TypeVersion),
		Reason: fmt.Sprintf(
			"n8n mints its own webhook route and carries no path, so this trigger was given the label %q. The public URL is a route KilasFlow mints on activation; rename the label freely.",
			label),
	}, true
}

// routeLabel turns a node name into a path-shaped label.
func routeLabel(name string) string {
	var builder strings.Builder
	previousDash := true
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			previousDash = false
		case !previousDash:
			builder.WriteByte('-')
			previousDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
