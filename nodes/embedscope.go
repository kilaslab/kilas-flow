package nodes

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
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
		case DatastoreNodeType:
			issues = append(issues, datastoreOperationIssues(node)...)
			issues = append(issues, datastoreTargetIssues(node, confinement)...)
		case DatastoreToolNodeType:
			// An agent tool binds one table and answers filtered reads. It has
			// no operation of its own — reads are the whole of it — so only the
			// table it names has to be inside the confinement.
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
		if !confinement.AllowsDatastoreName(target) {
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
