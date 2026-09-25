package nodes

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// This file is the one place that knows how a document reaches outside itself.
//
// It exists because an embed session's scopes bound *what it may call*, not
// *what the document it saves may reach*. A session holding workflow:write can
// save any graph into its own workflow, and the Data table node and a node's
// credential reference then act with the tenant's authority rather than the
// session's: a guest editor could aim a save at a sibling table, enumerate or
// drop every table in the tenant, or attach a credential the workflow never
// referenced, and run it. Two functions close that, and they are deliberately
// each other's inverse:
//
//   - DocumentReferences answers "what does this graph use?", and is what the
//     session mint derives a confinement from — the published revision, which
//     only a trusted caller authored.
//   - EmbedScopeIssues answers "what does this graph use that it may not?",
//     and is what a save, a publish, and a run are checked against.

// DocumentReferences collects every tenant resource a document points at.
//
// It is the inverse of EmbedScopeIssues: whatever it collects for a document is
// what that document would be allowed to reference, which is how a confinement
// is derived from the one revision a workflow's owner published.
func DocumentReferences(document workflow.Document) embed.Confinement {
	confinement := embed.Confinement{}
	for _, node := range document.Nodes {
		for _, credentialID := range credentialIDs(node) {
			confinement.Credentials = append(confinement.Credentials, credentialID)
		}
		if node.Type == DatastoreNodeType || node.Type == DatastoreToolNodeType {
			if datastore := datastoreReference(node); datastore != nil {
				confinement.Datastores = append(confinement.Datastores, *datastore)
			}
		}
	}
	// The workflows the graph calls come from the one walk that knows which node
	// types call one — the same walk the check below and the activation gate
	// read. Reading them here instead would be a second list of node types, and
	// a second list is one that gets forgotten: the Workflow Tool was missing
	// from this function while the activation gate knew about it.
	for _, call := range WorkflowCalls(document) {
		if call.Target == "" || call.Expression {
			continue
		}
		confinement.Workflows = append(confinement.Workflows, call.Target)
	}
	return confinement
}

// EmbedScopeIssues reports everything a document references that its
// confinement does not allow, and every operation no embed session may perform.
//
// One line per problem, naming the node, so a caller answers 403 with something
// an integrator can act on rather than a bare refusal. An empty slice means the
// document is inside its confinement. A document that references an expression
// where a data table or a sub-workflow is required is refused rather than
// skipped: the value is only knowable at run time, and a check that cannot see
// the target cannot bound it.
//
// A disabled node is checked like any other. Unlike the unscoped-credential
// rule, which asks whether a secret could leave and so skips a node that never
// runs, this asks what the session may reference at all, and a disabled node
// is one save from running. The owner's published revision grants whatever
// its own disabled nodes reference, so saving that document back still passes.
func EmbedScopeIssues(document workflow.Document, confinement embed.Confinement) []string {
	var issues []string
	for _, node := range document.Nodes {
		for _, credentialID := range credentialIDs(node) {
			if !confinement.AllowsCredential(credentialID) {
				issues = append(issues, fmt.Sprintf(
					"node %s attaches credential %s, which this embed session was not granted",
					embedNodeLabel(node), credentialID))
			}
		}
		switch node.Type {
		case DatastoreNodeType, DatastoreToolNodeType:
			// The agent tool writes as the step node does — insert, update,
			// upsert and delete — so it answers to the same two checks: no
			// table operation, and one table inside the confinement.
			issues = append(issues, datastoreOperationIssues(node)...)
			issues = append(issues, datastoreTargetIssues(node, confinement)...)
		}
	}
	// The same walk the confinement is minted from, so a node type that calls a
	// workflow cannot be bounded at mint time and unchecked here, or the other
	// way round.
	for _, call := range WorkflowCalls(document) {
		issues = append(issues, subworkflowConfinementIssues(call, confinement)...)
	}
	return issues
}

// CredentialLookup reads one stored credential — its type and its scope, never
// its secret — by id. found is false for an id the caller may not see or that no
// longer exists; err is for a store that could not answer.
type CredentialLookup func(credentialID string) (record credentials.Record, found bool, err error)

// UnscopedCredentialIssues reports every node that attaches a credential which
// could be sent to any host: one whose secret travels on a request whose URL a
// node chooses, and whose author and type named no scope for it.
//
// It is the confinement rule for a caller who may edit a document but must not
// be able to read a secret — an embed session and a scoped API key. Checking
// which credential ids a document attaches is not enough for them: a granted id
// on an HTTP node whose URL they typed, or on a model node whose base URL they
// set, sends the key wherever they like. The rule is deliberately the simplest
// sound one. It does not try to prove which host each node will reach, which an
// expression in a URL makes unknowable before the run; it asks instead that
// every credential such a caller's document carries be bounded by a scope, and
// the scope is enforced wherever the credential is applied, whatever the URL
// turns out to be. A credential with a default scope — OpenAI, OpenRouter,
// Telegram, WAHA, Google — passes; so does a database credential, which dials
// the host its own fields name, and so does a credential a Webhook or Form
// trigger uses only to verify requests arriving at it.
//
// One line per offending node. The credential is named by its id, which is what
// the document attached, and not by its name, which the caller may not be
// entitled to. A lookup failure is returned rather than read as "nothing to
// check", so the caller fails closed.
func UnscopedCredentialIssues(document workflow.Document, lookup CredentialLookup) ([]string, error) {
	var issues []string
	for _, node := range document.Nodes {
		for _, credentialID := range sendableCredentialIDs(node) {
			record, found, err := lookup(credentialID)
			if err != nil {
				return nil, err
			}
			if !found || !record.Unscoped() {
				continue
			}
			issues = append(issues, fmt.Sprintf(
				"node %s attaches credential %s, which has no allowed domains and could be sent to any host; "+
					"its owner must scope it to the hosts it is for before this document can use it",
				embedNodeLabel(node), credentialID))
		}
	}
	return issues, nil
}

// sendableCredentialIDs lists the credential ids a node attaches that it could
// place on an outbound request, sorted like credentialIDs.
//
// A Webhook or Form trigger uses its credential only to check the requests that
// arrive at it — the header, the basic-auth pair, the JWT key — and never sends
// it anywhere, so an unscoped one there is no way to read the secret. It is the
// documented way to protect an embedded workflow's trigger, and refusing it
// would refuse the guest editor every save. The same credential moved onto a
// node that does call out is caught on that node.
//
// A disabled node sends nothing either: it never runs, the same reason the
// compiler does not hold a disabled node's credentials against it. n8n imports
// routinely carry a switched-off node whose credential nobody scoped, and
// counting it would block a narrowed caller from every save of the workflow.
// Switching the node on is a save, and that save is checked. The embed grant
// check is not relaxed the same way — see EmbedScopeIssues.
func sendableCredentialIDs(node workflow.Node) []string {
	if node.Disabled {
		return nil
	}
	inbound := inboundCredentialTypes[node.Type]
	ids := make([]string, 0, len(node.Credentials))
	for credentialType, id := range node.Credentials {
		if inbound[credentialType] {
			continue
		}
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	sort.Strings(ids)
	return ids
}

// inboundCredentialTypes maps each trigger that verifies arriving requests to
// the credential types it verifies them with. Read off the two definitions
// rather than restated, so a mode added to either trigger is covered here
// without a second list to keep in step.
var inboundCredentialTypes = func() map[string]map[string]bool {
	triggers := map[string]map[string]bool{}
	for _, definition := range []node.Definition{webhookTrigger(), formTrigger()} {
		types := map[string]bool{}
		for _, requirement := range definition.Credentials {
			types[requirement.Type] = true
		}
		triggers[definition.Type] = types
	}
	return triggers
}()

// datastoreOperationIssues refuses the table operations no embed session may
// perform, whatever its confinement names.
func datastoreOperationIssues(node workflow.Node) []string {
	operation := textParameter(node.Parameters, "operation")
	if operation == "" {
		operation = DatastoreOperationInsert
	}
	// Refused by operation rather than by resource: a document may declare
	// resource "row" beside a table operation, and the operation is what the
	// executor acts on.
	if datastoreTableOperations[operation] {
		return []string{fmt.Sprintf(
			"node %s performs the data table operation %q, and an embed session cannot manage data tables",
			embedNodeLabel(node), operation)}
	}
	return nil
}

// datastoreTargetIssues bounds the one data table a node addresses.
func datastoreTargetIssues(node workflow.Node, confinement embed.Confinement) []string {
	locator, found := property.ReadLocator(node.Parameters["dataTableId"])
	if !found || !property.LocatorIsSet(node.Parameters["dataTableId"]) {
		// A node with no table chosen cannot address anything, and the
		// compiler refuses it before it can run: nothing to bound here.
		return nil
	}
	if property.ExpressionMarker(locator.Value) {
		return []string{fmt.Sprintf(
			"node %s chooses its data table with an expression, which this embed session cannot be confined to",
			embedNodeLabel(node))}
	}
	target := strings.TrimSpace(fmt.Sprint(locator.Value))
	if target == "" {
		return nil
	}
	// The mode decides which half of the reference is trustworthy. A By-Name
	// locator is resolved against the tenant's live list, so its name is what
	// has to be allowed; every other mode carries the catalogue id already.
	if strings.EqualFold(locator.Mode, "name") {
		if !grantsDatastoreName(confinement, target) {
			return []string{fmt.Sprintf(
				"node %s addresses the data table named %q, which this embed session was not granted",
				embedNodeLabel(node), target)}
		}
		return nil
	}
	if !confinement.AllowsDatastoreID(target) {
		return []string{fmt.Sprintf(
			"node %s addresses data table %s, which this embed session was not granted",
			embedNodeLabel(node), target)}
	}
	return nil
}

// grantsDatastoreName reports whether the confinement grants a By-Name target.
//
// The grants are compared by datastore.ResolveByName, the rule a run resolves
// the name by, so the check and the run cannot disagree about which names are
// the same. The check lives here rather than on embed.Confinement because the
// resolver's package already depends on that one.
//
// A name two grants share is still granted. Each grant that matches would allow
// the name on its own, so allowing it widens nothing, and refusing it would
// refuse a name the owner's own published document used. Whether the name
// picks out one table is a different question — the run's — and it is asked
// against the tenant's live list, where a name two tables share is refused.
func grantsDatastoreName(confinement embed.Confinement, name string) bool {
	granted := make([]datastore.Datastore, 0, len(confinement.Datastores))
	for _, ref := range confinement.Datastores {
		// A grant by id carries no name, and an empty name matches nothing.
		granted = append(granted, datastore.Datastore{ID: ref.ID, Name: ref.Name})
	}
	_, err := datastore.ResolveByName(granted, name)
	return err == nil || errors.Is(err, datastore.ErrAmbiguousName)
}

// subworkflowConfinementIssues bounds one node that calls a workflow.
func subworkflowConfinementIssues(call WorkflowCall, confinement embed.Confinement) []string {
	if call.Expression {
		return []string{fmt.Sprintf(
			"node %s chooses its sub-workflow with an expression, which this embed session cannot be confined to",
			embedNodeLabel(call.Node))}
	}
	if call.Target == "" {
		// A node naming nothing cannot reach anything: the compiler refuses it
		// before it can run.
		return nil
	}
	if !confinement.AllowsWorkflow(call.Target) {
		return []string{fmt.Sprintf(
			"node %s calls workflow %s, which this embed session was not granted",
			embedNodeLabel(call.Node), call.Target)}
	}
	return nil
}

// datastoreReference collects the data table one node addresses, if any.
//
// A node choosing its table with an expression contributes nothing: the mint
// derives a confinement from what it can prove, and a reference it cannot read
// must not widen what a later session is allowed to reach.
func datastoreReference(node workflow.Node) *embed.DatastoreRef {
	locator, found := property.ReadLocator(node.Parameters["dataTableId"])
	if !found || property.ExpressionMarker(locator.Value) {
		return nil
	}
	target := strings.TrimSpace(fmt.Sprint(locator.Value))
	if target == "" {
		return nil
	}
	if strings.EqualFold(locator.Mode, "name") {
		return &embed.DatastoreRef{Name: target}
	}
	return &embed.DatastoreRef{ID: target}
}

// credentialIDs lists the credential ids one node attaches, sorted so a check
// reports them in a stable order — a Go map has none, and an error message that
// reorders itself between runs is a message nobody can diff.
func credentialIDs(node workflow.Node) []string {
	if len(node.Credentials) == 0 {
		return nil
	}
	ids := make([]string, 0, len(node.Credentials))
	for _, id := range node.Credentials {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	sort.Strings(ids)
	return ids
}

// embedNodeLabel names a node the way a person reading the canvas would.
func embedNodeLabel(node workflow.Node) string {
	if name := strings.TrimSpace(node.Name); name != "" {
		return fmt.Sprintf("%q", name)
	}
	return node.ID
}
