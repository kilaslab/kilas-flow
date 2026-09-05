package nodes_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// n8nOption is one row of the transcription in testdata/n8n_sql_options.json.
type n8nOption struct {
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	Default           any      `json:"default"`
	Values            []string `json:"values"`
	ShowForOperations []string `json:"showForOperations"`
}

func n8nOptions(t *testing.T) []n8nOption {
	t.Helper()
	raw, err := os.ReadFile("testdata/n8n_sql_options.json")
	if err != nil {
		t.Fatalf("read the transcription: %v", err)
	}
	var file struct {
		Options []n8nOption `json:"options"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode the transcription: %v", err)
	}
	if len(file.Options) == 0 {
		t.Fatal("the transcription lists no options")
	}
	return file.Options
}

// optionsCollection finds the declared Options collection on a registered node.
func optionsCollection(t *testing.T, nodeType string, version workflow.TypeVersion) node.PropertyDefinition {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodeType, version)
	if !found {
		t.Fatalf("%s version %s is not registered", nodeType, version)
	}
	for _, parameter := range definition.Parameters {
		if parameter.Key == "options" {
			return parameter
		}
	}
	t.Fatalf("%s declares no options collection", nodeType)
	return node.PropertyDefinition{}
}

// The collection's names and defaults are n8n's, checked against a committed
// transcription rather than against the reference checkout itself.
//
// It has to be a transcription: internal/guardrails forbids any build input —
// a test included — from reading a path under the reference checkout, because
// reading n8n from a build step would make foreign source a build input. So
// the file records what the reference said and when, and this test is what
// keeps the declaration from drifting away from that record.
func TestTheOptionsCollectionCarriesN8NsOwnNamesAndDefaults(t *testing.T) {
	t.Parallel()

	declared := map[string]node.PropertyDefinition{}
	for _, field := range optionsCollection(t, nodes.PostgresNodeType, nodes.PostgresV2Version).Fields {
		declared[field.Key] = field
	}

	reference := n8nOptions(t)
	if len(declared) != len(reference) {
		got := make([]string, 0, len(declared))
		for key := range declared {
			got = append(got, key)
		}
		sort.Strings(got)
		t.Fatalf("the collection declares %d options %v, want n8n's %d", len(declared), got, len(reference))
	}

	for _, option := range reference {
		field, present := declared[option.Name]
		if !present {
			t.Errorf("option %q is missing", option.Name)
			continue
		}
		// The default is the half that a stored workflow silently depends on.
		// An option nobody opened arrives absent, and absent means whatever
		// the declaration says it means — so a default that differs from
		// n8n's changes what an imported workflow does without changing a
		// single stored byte.
		switch want := option.Default.(type) {
		case bool:
			if field.Default != want {
				t.Errorf("option %q default = %#v, want %#v", option.Name, field.Default, want)
			}
		case string:
			if field.Default != want {
				t.Errorf("option %q default = %#v, want %q", option.Name, field.Default, want)
			}
		case float64:
			if field.Default != int(want) && field.Default != want {
				t.Errorf("option %q default = %#v, want %v", option.Name, field.Default, want)
			}
		case []any:
			// n8n's only list default is the empty one. An absent default is
			// the same thing here — nothing selected — and both are accepted
			// so long as neither preselects a column.
			if length := reflect.ValueOf(field.Default); field.Default != nil &&
				(length.Kind() != reflect.Slice || length.Len() != 0) {
				t.Errorf("option %q default = %#v, want nothing selected", option.Name, field.Default)
			}
		default:
			// Reached only by a transcription this test does not know how to
			// check, which must fail rather than pass silently.
			t.Errorf("option %q has a default of an unhandled type %T", option.Name, option.Default)
		}
		if len(option.Values) > 0 {
			got := make([]string, 0, len(field.Options))
			for _, choice := range field.Options {
				got = append(got, choice.Value)
			}
			sort.Strings(got)
			want := append([]string(nil), option.Values...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("option %q values = %v, want %v", option.Name, got, want)
			}
		}
	}
}

// The MySQL node carries the same collection less the one member MySQL cannot
// honour.
//
// Asserted rather than left to the reader because the failure it guards is
// silent in the worst way: MySQL parses CASCADE on DROP TABLE and documents
// that it does nothing, so a node that offered the choice would take the
// instruction, emit valid SQL, and not drop the dependent objects.
func TestTheMySQLOptionsCollectionOmitsWhatMySQLCannotDo(t *testing.T) {
	t.Parallel()

	postgres := map[string]bool{}
	for _, field := range optionsCollection(t, nodes.PostgresNodeType, nodes.PostgresV2Version).Fields {
		postgres[field.Key] = true
	}
	mysql := map[string]bool{}
	for _, field := range optionsCollection(t, nodes.MySQLNodeType, nodes.MySQLV2Version).Fields {
		mysql[field.Key] = true
	}

	if mysql["cascade"] {
		t.Error("the MySQL node offers cascade, which MySQL parses and ignores")
	}
	for key := range postgres {
		if key == "cascade" {
			continue
		}
		if !mysql[key] {
			t.Errorf("the MySQL node is missing option %q", key)
		}
	}
	for key := range mysql {
		if !postgres[key] {
			t.Errorf("the MySQL node declares option %q the PostgreSQL node does not", key)
		}
	}
}

// Every declared option is either applied or refused by name.
//
// This is the acceptance criterion that keeps the collection from becoming
// decoration. Declaring an option is a promise in the panel; the panel cannot
// say "this one does nothing" unless something makes it. So an option that is
// not read by the executor has to name itself in the refusal list, and its
// description has to say so where the user will actually read it.
func TestEveryDeclaredOptionIsAppliedOrRefusedByName(t *testing.T) {
	t.Parallel()

	refused := nodes.UnappliedSQLOptionsForTest()
	for _, field := range optionsCollection(t, nodes.PostgresNodeType, nodes.PostgresV2Version).Fields {
		applied := nodes.SQLOptionIsAppliedForTest(field.Key)
		reason, named := refused[field.Key]
		switch {
		case applied && named:
			t.Errorf("option %q is both applied and listed as refused", field.Key)
		case !applied && !named:
			t.Errorf("option %q is declared but nothing reads it and nothing refuses it by name", field.Key)
		case named && reason == "":
			t.Errorf("option %q is refused with an empty reason", field.Key)
		case named && !strings.HasPrefix(field.Description, "Not applied"):
			// The list is for the test; the description is for the user, and
			// only one of the two is visible from inside the editor.
			t.Errorf("option %q is refused but its description does not say so: %q", field.Key, field.Description)
		}
	}
	for key := range refused {
		if !nodes.SQLOptionIsDeclaredForTest(key) {
			t.Errorf("option %q is refused but never declared", key)
		}
	}
}

// A collection stored as text is refused at save, not at run.
//
// The shape is not hypothetical: the editor rendered a collection as a
// textarea until this ticket gave it a control, so a node configured before
// then holds a string. Reading it as an empty collection would silently reset
// every option the author had set, and doing that at run time would do it in
// production.
func TestOptionsStoredAsTextAreRefusedWhenTheWorkflowIsSaved(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.PostgresNodeType, nodes.PostgresV2Version)

	for name, options := range map[string]any{
		"text":         "[object Object]",
		"unknown key":  map[string]any{"queryBatchingg": "single"},
		"unknown mode": map[string]any{"queryBatching": "parallel"},
	} {
		t.Run(name, func(t *testing.T) {
			err := definition.Validate(workflow.Node{Parameters: map[string]any{
				"operation": "executeQuery", "query": "SELECT 1", "options": options,
			}})
			if err == nil {
				t.Fatalf("options %#v were accepted", options)
			}
		})
	}

	for name, options := range map[string]any{
		"absent":   nil,
		"empty":    map[string]any{},
		"blank":    "",
		"honoured": map[string]any{"queryBatching": "transaction", "largeNumbersOutput": "numbers"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := definition.Validate(workflow.Node{Parameters: map[string]any{
				"operation": "executeQuery", "query": "SELECT 1", "options": options,
			}}); err != nil {
				t.Fatalf("options %#v were refused: %v", options, err)
			}
		})
	}
}
