package main

// BUG-b4cb1c: two pages stated the size of the node catalogue and called it
// measured, and the numbers were twelve types stale by the time anyone checked.
// This test is the measurement. It registers what run() registers before a
// directory pack can add anything — the compiled nodes, the vector nodes and
// the two embedded packs — and fails when a page's sentence disagrees, so the
// prose is corrected in the change that moves the count.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/telegram"
	"github.com/kilaslab/kilas-flow/packs/waha"
)

// catalogueCount is a catalogue's size, as GET /api/v1/node-types counts it:
// one entry per type-and-version pair.
type catalogueCount struct{ types, pairs int }

func countCatalogue(registry *node.Registry) catalogueCount {
	definitions := registry.List()
	types := map[string]struct{}{}
	for _, definition := range definitions {
		types[definition.Type] = struct{}{}
	}
	return catalogueCount{types: len(types), pairs: len(definitions)}
}

// shippedCatalogue registers the nodes a default install serves, split into
// the compiled ones and the ones from the declarative packs, in the calls
// run() makes.
func shippedCatalogue(t *testing.T) (compiled, packs catalogueCount) {
	t.Helper()
	compiledRegistry := node.NewRegistry()
	if err := nodes.RegisterAll(compiledRegistry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := nodes.RegisterVectorNodes(compiledRegistry, ""); err != nil {
		t.Fatalf("RegisterVectorNodes() error = %v", err)
	}

	packRegistry := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	options := loadoptions.NewResolver(safehttp.DefaultPolicy(), 0)
	packTriggers := nodepack.NewTriggerRegistry()
	// A pack refuses to register against an executor the server has not
	// installed, so the two it binds to are installed first, as run() does.
	if err := nodes.RegisterRoutingExecutor(executors, safehttp.DefaultPolicy(), routes, packRegistry); err != nil {
		t.Fatalf("RegisterRoutingExecutor() error = %v", err)
	}
	if err := executors.Register(nodepack.TriggerExecutorID,
		nodepack.NewTriggerExecutor(packTriggers, safehttp.DefaultPolicy())); err != nil {
		t.Fatalf("register the pack trigger executor: %v", err)
	}
	if err := telegram.Register(packRegistry, routes, executors, options); err != nil {
		t.Fatalf("telegram.Register() error = %v", err)
	}
	if err := waha.Register(waha.Deps{
		Definitions: packRegistry, Routes: routes, Triggers: packTriggers,
		Deliveries: webhook.NewRegistry(), Lifecycles: webhook.NewLifecycleRegistry(),
		Executors: executors, Options: options,
	}); err != nil {
		t.Fatalf("waha.Register() error = %v", err)
	}
	// The packs' agent-tool forms (pack.telegramTool), as run() registers them.
	if err := nodes.RegisterPackToolForms(packRegistry); err != nil {
		t.Fatalf("RegisterPackToolForms() error = %v", err)
	}
	return countCatalogue(compiledRegistry), countCatalogue(packRegistry)
}

// sentence matches a page's statement of the catalogue's size, written as
// "N distinct node types … across M type-and-version pairs: A types (B pairs)
// compiled into the binary, … and C types (D pairs) from the declarative packs".
var sentence = regexp.MustCompile(`(\d+) distinct node types[^.]*?across (\d+) type-and-version pairs\*{0,2}: (\d+) types \((\d+) pairs\) compiled into the binary.*?(\d+) types \((\d+) pairs\) from the\s+declarative packs`)

func TestTheDocumentedCatalogueSizeIsTheShippedOne(t *testing.T) {
	compiled, packs := shippedCatalogue(t)
	total := catalogueCount{types: compiled.types + packs.types, pairs: compiled.pairs + packs.pairs}
	want := []int{total.types, total.pairs, compiled.types, compiled.pairs, packs.types, packs.pairs}

	for _, page := range []string{
		"../../docs/src/content/docs/concepts/node-registry.md",
		"../../docs/src/content/docs/start/what-kilasflow-is.md",
	} {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		text := strings.Join(strings.Fields(string(raw)), " ")
		match := sentence.FindStringSubmatch(text)
		if match == nil {
			t.Errorf("%s no longer states the catalogue's size in the sentence this test reads; "+
				"keep the sentence or move the check with it", page)
			continue
		}
		for index, value := range want {
			if got, _ := strconv.Atoi(match[index+1]); got != value {
				t.Errorf("%s says %q; the shipped catalogue is %d types across %d pairs: "+
					"%d types (%d pairs) compiled, %d types (%d pairs) from packs",
					page, match[0], total.types, total.pairs, compiled.types, compiled.pairs, packs.types, packs.pairs)
				break
			}
		}
	}
}
