package n8n_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// BUG-5bgx5c: n8n's Switch v1 and v2 keep the left-hand value and its type
// once, at node level, and each `rules.rules` row holds only the comparison,
// the right-hand value and — in v1 — the output it routes to. The importer
// read `value1` and `dataType` from each row, found neither, and imported a
// node with no rules: every branch after the first was held back.

// legacySwitchFixture wraps one Switch between a Manual Trigger and one No
// Operation node per output, wired output N to target N.
func legacySwitchFixture(version float64, parameters string, targets ...string) string {
	nodes := []string{
		`{"id":"t","name":"Start","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}}`,
		fmt.Sprintf(`{"id":"s","name":"Switch","type":"n8n-nodes-base.switch","typeVersion":%v,"position":[220,0],"parameters":%s}`,
			version, parameters),
	}
	outputs := make([]string, 0, len(targets))
	for index, target := range targets {
		nodes = append(nodes, fmt.Sprintf(
			`{"id":"n%d","name":%q,"type":"n8n-nodes-base.noOp","typeVersion":1,"position":[440,%d],"parameters":{}}`,
			index, target, index*120))
		outputs = append(outputs, fmt.Sprintf(`[{"node":%q,"type":"main","index":0}]`, target))
	}
	return fmt.Sprintf(`{"name":"Legacy switch","nodes":[%s],"connections":{
	  "Start":{"main":[[{"node":"Switch","type":"main","index":0}]]},
	  "Switch":{"main":[%s]}}}`, strings.Join(nodes, ","), strings.Join(outputs, ","))
}

// switchWiring maps each of the Switch's outgoing ports to the node it feeds.
func switchWiring(result n8n.ImportResult) map[string]string {
	names := map[string]string{}
	for _, candidate := range result.Document.Nodes {
		names[candidate.ID] = candidate.Name
	}
	wiring := map[string]string{}
	for _, connection := range result.Document.Connections {
		if connection.Source.NodeID == "s" {
			wiring[connection.Source.Port] = names[connection.Target.NodeID]
		}
	}
	return wiring
}

// switchRouting runs the imported Switch on the given items and returns, per
// output port, the `id` of each item that arrived there.
func switchRouting(t *testing.T, imported workflow.Node, inputs ...map[string]any) [][]any {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := executors.Lookup(nodes.SwitchExecutorID)
	if !found {
		t.Fatalf("executor %q is not registered", nodes.SwitchExecutorID)
	}
	catalogue := registry(t)
	definition, found := catalogue.Lookup(imported.Type, imported.TypeVersion)
	if !found {
		t.Fatalf("%s is not registered", imported.Type)
	}
	catalogued, _ := catalogue.Get(imported.Type, imported.TypeVersion)
	definition.Inputs, definition.Outputs = catalogued.PortsFor(imported.Parameters, imported.TypeVersion)

	items := make([]workflow.Item, 0, len(inputs))
	for _, input := range inputs {
		items = append(items, workflow.Item{JSON: input})
	}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: imported.ID, Name: imported.Name, Type: imported.Type, TypeVersion: imported.TypeVersion,
		Parameters: imported.Parameters, Definition: definition,
	}, workflow.NodeInput{"main": items}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	routed := make([][]any, len(output))
	for port, stream := range output {
		routed[port] = []any{}
		for _, item := range stream {
			routed[port] = append(routed[port], item.JSON["id"])
		}
	}
	return routed
}

// switchRuleConditions returns rule `index`'s condition rows.
func switchRuleConditions(t *testing.T, imported workflow.Node, index int) []map[string]any {
	t.Helper()
	rules, _ := imported.Parameters["rules"].([]any)
	if index >= len(rules) {
		t.Fatalf("rules = %#v, want a rule %d", imported.Parameters["rules"], index)
	}
	rule, _ := rules[index].(map[string]any)
	filter, _ := rule["conditions"].(map[string]any)
	list, _ := filter["conditions"].([]any)
	rows := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		row, _ := entry.(map[string]any)
		rows = append(rows, row)
	}
	return rows
}

func assertNoHeldBackSwitchBranch(t *testing.T, result n8n.ImportResult) {
	t.Helper()
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking || strings.Contains(issue.Reason, "held back") ||
			strings.Contains(issue.Reason, "no readable rules") {
			t.Errorf("issue = %+v, want every rule read and every branch kept", issue)
		}
	}
}

// TestSwitchV1FromTemplate1934KeepsEveryBranch is the CheckCommand node of
// template 1934 ("Telegram AI chatbot") as n8n stores it: node-level `value1`
// and `dataType`, rows holding `operation`, `value2` and an `output` that n8n
// omits when it is 0, and a fallback on output 3. The four targets are No
// Operation nodes here; in the template they are the chat, greeting, image
// and error branches.
func TestSwitchV1FromTemplate1934KeepsEveryBranch(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacySwitchFixture(1, `{
	  "rules": {"rules": [
	    {"value2": "/", "operation": "notStartsWith"},
	    {"output": 1, "value2": "/start", "operation": "startsWith"},
	    {"output": 2, "value2": "=/image ", "operation": "startsWith"}
	  ]},
	  "value1": "={{ $json.message?.text }}",
	  "dataType": "string",
	  "fallbackOutput": 3
	}`, "Chat_mode", "Greeting", "Create an image", "Send error message"))
	assertNoHeldBackSwitchBranch(t, result)

	imported := nodeByName(result.Document, "Switch")
	rules, _ := imported.Parameters["rules"].([]any)
	if len(rules) != 3 {
		t.Fatalf("rules = %#v, want one rule per output the rows route to", imported.Parameters["rules"])
	}
	operations := []string{"notStartsWith", "startsWith", "startsWith"}
	for index, operation := range operations {
		rows := switchRuleConditions(t, imported, index)
		if len(rows) != 1 {
			t.Fatalf("rule %d = %#v, want its one row", index, rows)
		}
		left, _ := rows[0]["leftValue"].(map[string]any)
		if left["value"] != "{{ $json.message?.text }}" {
			t.Errorf("rule %d leftValue = %#v, want the node-level value1 expression", index, rows[0]["leftValue"])
		}
		operator, _ := rows[0]["operator"].(map[string]any)
		if operator["type"] != "string" || operator["operation"] != operation {
			t.Errorf("rule %d operator = %#v, want string %s from the node-level dataType", index, operator, operation)
		}
	}
	// Output 3 is the fallback, and it is the output right after the rules',
	// which is exactly where this server's extra fallback output sits.
	if imported.Parameters["fallbackOutput"] != "extra" {
		t.Errorf("fallbackOutput = %#v, want extra for n8n's output 3", imported.Parameters["fallbackOutput"])
	}

	want := map[string]string{"0": "Chat_mode", "1": "Greeting", "2": "Create an image", "3": "Send error message"}
	if got := switchWiring(result); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("wiring = %v, want %v", got, want)
	}

	// And it routes the way n8n does: the first matching row wins, and an
	// item no row matches takes the fallback.
	message := func(text string) map[string]any {
		return map[string]any{"id": text, "message": map[string]any{"text": text}}
	}
	routed := switchRouting(t, imported, message("hello"), message("/start"), message("/image a cat"), message("/help"))
	wantRouted := [][]any{{"hello"}, {"/start"}, {"/image a cat"}, {"/help"}}
	if fmt.Sprint(routed) != fmt.Sprint(wantRouted) {
		t.Errorf("routing = %v, want %v", routed, wantRouted)
	}
}

// TestSwitchV1RowsWithoutAnOperationCompareForEquality is the Check Status
// node of template 1534 ("Back up your n8n workflows to GitHub"): its rows
// carry no `operation`, because n8n omits a string row's default `equal`, and
// no fallback.
func TestSwitchV1RowsWithoutAnOperationCompareForEquality(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacySwitchFixture(1, `{
	  "rules": {"rules": [
	    {"value2": "same"},
	    {"output": 1, "value2": "different"},
	    {"output": 2, "value2": "new"}
	  ]},
	  "value1": "={{$json.github_status}}",
	  "dataType": "string"
	}`, "Same file - Do nothing", "File is different", "File is new"))
	assertNoHeldBackSwitchBranch(t, result)

	imported := nodeByName(result.Document, "Switch")
	for index, value := range []string{"same", "different", "new"} {
		rows := switchRuleConditions(t, imported, index)
		operator, _ := rows[0]["operator"].(map[string]any)
		if operator["operation"] != "equals" || rows[0]["rightValue"] != value {
			t.Errorf("rule %d = %#v, want equals %q", index, rows[0], value)
		}
	}
	if fallback, present := imported.Parameters["fallbackOutput"]; present {
		t.Errorf("fallbackOutput = %#v, want none: the node declares no fallback", fallback)
	}
	want := map[string]string{"0": "Same file - Do nothing", "1": "File is different", "2": "File is new"}
	if got := switchWiring(result); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("wiring = %v, want %v", got, want)
	}
	status := func(value string) map[string]any { return map[string]any{"id": value, "github_status": value} }
	routed := switchRouting(t, imported, status("new"), status("same"), status("different"), status("gone"))
	if want := [][]any{{"same"}, {"different"}, {"new"}}; fmt.Sprint(routed) != fmt.Sprint(want) {
		t.Errorf("routing = %v, want %v and the unmatched item dropped", routed, want)
	}
}

// TestSwitchV1RoutesEachRowToItsOutput covers what the templates above do not:
// a Switch with no `dataType` compares numbers (n8n's default), two rows can
// share an output, an output no row names still has to exist so the wires
// after it keep their positions, and the fallback output follows the rules'.
func TestSwitchV1RoutesEachRowToItsOutput(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacySwitchFixture(1, `{
	  "rules": {"rules": [
	    {"value2": 0},
	    {"output": 2, "value2": 100, "operation": "larger"},
	    {"output": 2, "value2": 50, "operation": "equal"}
	  ]},
	  "value1": "={{ $json.amount }}",
	  "fallbackOutput": 3
	}`, "Negative", "Never", "Large or fifty", "Other"))
	assertNoHeldBackSwitchBranch(t, result)

	imported := nodeByName(result.Document, "Switch")
	rules, _ := imported.Parameters["rules"].([]any)
	if len(rules) != 3 {
		t.Fatalf("rules = %#v, want outputs 0, 1 and 2", imported.Parameters["rules"])
	}
	first := switchRuleConditions(t, imported, 0)
	operator, _ := first[0]["operator"].(map[string]any)
	if operator["type"] != "number" || operator["operation"] != "smaller" {
		t.Errorf("rule 0 operator = %#v, want n8n's number default, smaller", operator)
	}
	if shared := switchRuleConditions(t, imported, 2); len(shared) != 2 {
		t.Errorf("rule 2 = %#v, want both rows routed to output 2", shared)
	}
	if imported.Parameters["fallbackOutput"] != "extra" {
		t.Errorf("fallbackOutput = %#v, want extra for n8n's output 3", imported.Parameters["fallbackOutput"])
	}
	want := map[string]string{"0": "Negative", "1": "Never", "2": "Large or fifty", "3": "Other"}
	if got := switchWiring(result); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("wiring = %v, want %v", got, want)
	}

	amount := func(id string, value float64) map[string]any { return map[string]any{"id": id, "amount": value} }
	routed := switchRouting(t, imported, amount("minus", -5), amount("big", 150), amount("fifty", 50), amount("ten", 10))
	if want := [][]any{{"minus"}, {}, {"big", "fifty"}, {"ten"}}; fmt.Sprint(routed) != fmt.Sprint(want) {
		t.Errorf("routing = %v, want %v", routed, want)
	}
}

// TestSwitchV1RowsOutOfOutputOrderAreReported: n8n tries rows in their own
// order, and one rule per output puts them in output order, so an item two
// rows match can change branch. That is named rather than hidden.
func TestSwitchV1RowsOutOfOutputOrderAreReported(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacySwitchFixture(1, `{
	  "rules": {"rules": [
	    {"output": 1, "value2": "vip", "operation": "contains"},
	    {"value2": "v", "operation": "startsWith"}
	  ]},
	  "value1": "={{ $json.tier }}",
	  "dataType": "string"
	}`, "Starts with v", "VIP"))
	assertNoHeldBackSwitchBranch(t, result)

	imported := nodeByName(result.Document, "Switch")
	first := switchRuleConditions(t, imported, 0)
	if first[0]["rightValue"] != "v" {
		t.Errorf("rule 0 = %#v, want the row that routes to output 0", first)
	}
	reported := false
	for _, issue := range result.Unsupported {
		if issue.Field == "rules" && strings.Contains(issue.Reason, "order") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("unsupported = %#v, want the reordering named", result.Unsupported)
	}
}

// TestSwitchV2RulesRouteByPosition covers v2, which routes a matching row to
// its own position and labels it with `outputKey`; its numeric fallback names
// one of those outputs, and -1 means none.
func TestSwitchV2RulesRouteByPosition(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacySwitchFixture(2, `{
	  "dataType": "string",
	  "value1": "={{ $json.channel }}",
	  "rules": {"rules": [
	    {"value2": "email", "outputKey": "Email"},
	    {"operation": "contains", "value2": "sms", "outputKey": "SMS"},
	    {"operation": "notEqual", "value2": "", "outputKey": "Anything else"}
	  ]},
	  "fallbackOutput": -1
	}`, "Send email", "Send SMS", "Log"))
	assertNoHeldBackSwitchBranch(t, result)

	imported := nodeByName(result.Document, "Switch")
	rules, _ := imported.Parameters["rules"].([]any)
	if len(rules) != 3 {
		t.Fatalf("rules = %#v, want one rule per row", imported.Parameters["rules"])
	}
	for index, key := range []string{"Email", "SMS", "Anything else"} {
		rule, _ := rules[index].(map[string]any)
		if rule["outputKey"] != key {
			t.Errorf("rule %d outputKey = %#v, want %q", index, rule["outputKey"], key)
		}
	}
	if fallback, present := imported.Parameters["fallbackOutput"]; present {
		t.Errorf("fallbackOutput = %#v, want none for -1", fallback)
	}
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Field, "fallbackOutput") {
			t.Errorf("issue = %+v, want -1 read as no fallback", issue)
		}
	}
	want := map[string]string{"0": "Send email", "1": "Send SMS", "2": "Log"}
	if got := switchWiring(result); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("wiring = %v, want %v", got, want)
	}

	// A fallback naming one of the rules' own outputs has no equivalent here,
	// and is named with its index rather than written as an inert value.
	named := importFixture(t, legacySwitchFixture(2, `{
	  "dataType": "string",
	  "value1": "={{ $json.channel }}",
	  "rules": {"rules": [{"value2": "email"}, {"value2": "sms"}]},
	  "fallbackOutput": 0
	}`, "Send email", "Send SMS"))
	if fallback := nodeByName(named.Document, "Switch").Parameters["fallbackOutput"]; fallback != nil {
		t.Errorf("fallbackOutput = %#v, want no inert value written", fallback)
	}
	reported := false
	for _, issue := range named.Unsupported {
		if issue.Field == "fallbackOutput" && strings.Contains(issue.Reason, "output 0") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("unsupported = %#v, want the numeric fallback named with its index", named.Unsupported)
	}
}
