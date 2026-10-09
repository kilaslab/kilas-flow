package main

// internal/shippedcatalogue.Register assembles the catalogue a default install
// serves without a server, and it does so by repeating the calls run() makes on
// the node registry. A copy is the drift the docs' generated catalogue size
// exists to catch, and nothing but this test notices the copy falling behind
// when a track adds a registration call to run(): the docs-facts check compares
// the pages with Register, not with run(). So this reads both functions and
// holds them together.
//
// A call "registers" when the node registry is handed to it as an argument, or
// as the Definitions field of the dependency struct it takes (the name every
// pack loader's dependencies use for it), or when it is a Register*, Apply*,
// Add* or Load* method on the registry itself. Reading it — List, Scopes,
// LifecycleIDs, or passing it as a Catalog or NodeRegistry — does not.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// runCallsThatAddNoShippedNode are the calls run() hands the registry to that
// put no node into a default install's catalogue, and why. Anything else run()
// does to the registry has to be mirrored in shippedcatalogue.Register.
var runCallsThatAddNoShippedNode = map[string]string{
	"nodepack.LoadDir":           "installs the operator's own packs from packs.dir; the shipped catalogue is what a default install has before any",
	"setupSidecar":               "loads the opt-in JavaScript sidecar's community nodes, an operator's installation that is off by default",
	"wasmpack.NewExecutor":       "builds the executor that runs module packs against definitions LoadDir registered; it adds none itself",
	"webhook.Extract":            "reads the definitions to find webhook triggers",
	"(registry).ApplyVisibility": "narrows who may see definitions that are already registered",
}

// registryCalls lists, sorted and without repeats, the calls in the function
// that can register nodes into the registry held in the variable named ident.
// A method on the registry itself is named "(registry).Method", so the two
// functions compare whatever they call their variable.
func registryCalls(function *ast.FuncDecl, ident string) []string {
	seen := map[string]bool{}
	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !handsOverRegistry(call, ident) {
			return true
		}
		name := types.ExprString(call.Fun)
		if strings.HasPrefix(name, ident+".") {
			name = "(registry)." + strings.TrimPrefix(name, ident+".")
		}
		seen[name] = true

		return true
	})

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

func handsOverRegistry(call *ast.CallExpr, ident string) bool {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == ident {
			for _, prefix := range []string{"Register", "Apply", "Add", "Load"} {
				if strings.HasPrefix(selector.Sel.Name, prefix) {
					return true
				}
			}
		}
	}
	for _, argument := range call.Args {
		if isIdent(argument, ident) {
			return true
		}
		if unary, ok := argument.(*ast.UnaryExpr); ok {
			argument = unary.X
		}
		literal, ok := argument.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if ok && isIdent(pair.Key, "Definitions") && isIdent(pair.Value, ident) {
				return true
			}
		}
	}

	return false
}

func isIdent(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)

	return ok && identifier.Name == name
}

// compareRegistrations says how the two functions disagree: calls run() makes
// that Register does not (and that are not allowed), calls Register makes that
// run() never does, and allowed calls run() no longer makes.
func compareRegistrations(run, shipped []string, allowed map[string]string) (missing, extra, stale []string) {
	inRun, inShipped := map[string]bool{}, map[string]bool{}
	for _, name := range run {
		inRun[name] = true
		if _, ok := allowed[name]; !ok && !contains(shipped, name) {
			missing = append(missing, name)
		}
	}
	for _, name := range shipped {
		inShipped[name] = true
		if !inRun[name] {
			extra = append(extra, name)
		}
	}
	for name := range allowed {
		if !inRun[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)

	return missing, extra, stale
}

func contains(list []string, name string) bool {
	for _, entry := range list {
		if entry == name {
			return true
		}
	}

	return false
}

func parseFunction(t *testing.T, path string, source any, name string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == name && function.Recv == nil {
			return function
		}
	}
	t.Fatalf("%s has no function %s", path, name)

	return nil
}

func TestShippedCatalogueRegistersWhatRunRegisters(t *testing.T) {
	run := registryCalls(parseFunction(t, "main.go", nil, "run"), "nodeRegistry")
	shipped := registryCalls(parseFunction(t, "../../internal/shippedcatalogue/shippedcatalogue.go", nil, "Register"), "reg")
	if len(run) < 5 || len(shipped) < 4 {
		t.Fatalf("found %d registry calls in run() and %d in Register(); the test is reading the wrong thing\nrun: %v\nshipped: %v",
			len(run), len(shipped), run, shipped)
	}

	missing, extra, stale := compareRegistrations(run, shipped, runCallsThatAddNoShippedNode)
	for _, name := range missing {
		t.Errorf("run() hands the node registry to %s, and internal/shippedcatalogue.Register does not make that call.\n"+
			"Add it to Register so the docs' catalogue size and the skills' node checks see what a default install serves; "+
			"or, if it adds no node to a default install, name it in runCallsThatAddNoShippedNode with the reason.", name)
	}
	for _, name := range extra {
		t.Errorf("internal/shippedcatalogue.Register calls %s on its registry, and run() never does: "+
			"the catalogue it builds would hold nodes a default install does not serve", name)
	}
	for _, name := range stale {
		t.Errorf("runCallsThatAddNoShippedNode names %s, which run() no longer hands the registry to: drop the entry", name)
	}
}

// Planted: a call added to run() and left out of Register is reported by name,
// and so is one Register makes that run() does not.
func TestRegistryCallComparisonSeesAMissingAndAnExtraCall(t *testing.T) {
	runSource := `package p
func run() {
	nodeRegistry := node.NewRegistry()
	_ = nodes.RegisterAll(nodeRegistry)
	_ = nodes.RegisterPackToolForms(nodeRegistry)
	_ = telegram.Register(nodeRegistry, routes)
	_ = waha.Register(waha.Deps{Definitions: nodeRegistry, Routes: routes})
	_ = nodepack.LoadDir(nodepack.DirDeps{Definitions: nodeRegistry}, dir)
	_ = nodeRegistry.ApplyVisibility(grants)
	_ = nodeRegistry.List()
	_ = api.NewServer(api.Deps{NodeRegistry: nodeRegistry})
	_ = webhook.VerifyLifecycleBindings(nodeRegistry.LifecycleIDs(), lifecycles)
}`
	registerSource := `package p
func Register(reg *node.Registry) error {
	_ = nodes.RegisterAll(reg)
	_ = telegram.Register(reg, routes)
	_ = waha.Register(waha.Deps{Definitions: reg})
	_ = nodes.RegisterExperimental(reg)
	return nil
}`
	run := registryCalls(parseFunction(t, "run.go", runSource, "run"), "nodeRegistry")
	shipped := registryCalls(parseFunction(t, "register.go", registerSource, "Register"), "reg")

	wantRun := []string{"(registry).ApplyVisibility", "nodepack.LoadDir", "nodes.RegisterAll", "nodes.RegisterPackToolForms", "telegram.Register", "waha.Register"}
	if strings.Join(run, ",") != strings.Join(wantRun, ",") {
		t.Fatalf("registryCalls(run) = %v, want %v (reads and Catalog/NodeRegistry fields are not registrations)", run, wantRun)
	}

	missing, extra, stale := compareRegistrations(run, shipped, map[string]string{
		"nodepack.LoadDir":           "operator packs",
		"(registry).ApplyVisibility": "visibility",
		"wasmpack.NewExecutor":       "no longer in run",
	})
	if strings.Join(missing, ",") != "nodes.RegisterPackToolForms" {
		t.Errorf("missing = %v, want [nodes.RegisterPackToolForms]", missing)
	}
	if strings.Join(extra, ",") != "nodes.RegisterExperimental" {
		t.Errorf("extra = %v, want [nodes.RegisterExperimental]", extra)
	}
	if strings.Join(stale, ",") != "wasmpack.NewExecutor" {
		t.Errorf("stale = %v, want [wasmpack.NewExecutor]", stale)
	}
}
