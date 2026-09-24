package guardrails_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Node visibility is decided in exactly one place: the catalogue a document is
// compiled against. A compile site that forgets to narrow that catalogue is
// silent — the workflow compiles, runs, and uses a node its tenant was never
// given — and the sites are about to multiply (a validate endpoint, a
// run-revision endpoint, an execution retry), each of them written months after
// this rule was.
//
// So this is a rule, not a list of sites: any future `workflow.Compile` must
// obtain its catalogue through `workflow.CatalogFor`, and the repository
// functions that take a catalogue are pinned by name so a new one cannot appear
// without somebody deciding to allow it. A correctly scoped new call site needs
// no edit here.
//
// The failure message is addressed to whoever trips it, because the fix is not
// obvious from the assertion alone.

const workflowImportPath = "github.com/kilaslab/kilas-flow/internal/workflow"

// pinnedCatalogFunctions are the repository functions, methods and interface
// methods that compile a document against a caller-supplied catalogue. Their
// callers carry the obligation to narrow it.
//
// `publish` is here because Activate and PublishVersion both forward to it;
// it is excluded from the caller rule because its only callers are those two,
// which pass their own catalogue parameter straight through.
var pinnedCatalogFunctions = map[string]bool{
	"Activate":          true,
	"PublishVersion":    true,
	"publish":           true,
	"QueueManualLatest": true,
	// QueueManualVersion is the same queue path pinned to a named revision
	// (`run --revision`, and the retry of a finished execution), and queueManual
	// is the transaction both forward to.
	"QueueManualVersion": true,
	// QueueRetry queues a finished execution's own revision again, through
	// queueManual.
	"QueueRetry":  true,
	"queueManual": true,
}

// callersObligatedToScope are the pinned names whose call sites must pass
// workflow.CatalogFor. `publish` is not one of them: it is unexported and its
// callers forward the catalogue they were given.
var callersObligatedToScope = []string{"Activate", "PublishVersion", "QueueManualLatest", "QueueManualVersion", "QueueRetry"}

// skippedDirectories are trees that hold no first-party Go source this rule is
// about: generated clients, the reference checkout, build output and scratch
// data.
var skippedDirectories = map[string]bool{
	".git": true, ".pine": true, "node_modules": true, "web": true, "docs": true,
	"sdk": true, "design-refs": true, "third_party": true, "dist": true,
	"bin": true, "data": true,
}

// goFile is one parsed file plus the local name it gives the workflow package,
// which is empty when it does not import it.
type goFile struct {
	path            string
	relative        string
	file            *ast.File
	workflowName    string
	underRepository bool
}

// repositoryCatalogDecls is one declaration that takes a workflow.Catalog,
// with the parameter index the catalogue sits at.
type repositoryCatalogDecl struct {
	name  string
	index int
	pos   token.Position
}

func parseGoFiles(t *testing.T, root string) []goFile {
	t.Helper()
	fset := token.NewFileSet()
	files := []goFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".") || skippedDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, goFile{
			path:            path,
			relative:        filepath.ToSlash(relative),
			file:            parsed,
			workflowName:    localNameFor(parsed, workflowImportPath),
			underRepository: strings.HasPrefix(filepath.ToSlash(relative), "internal/repository/"),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository failed: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found; this rule cannot check anything")
	}
	return files
}

// localNameFor resolves the name a file uses for an import path, so an aliased
// import cannot evade the match. It answers the empty string when the file does
// not import it at all.
func localNameFor(file *ast.File, importPath string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return importPath[strings.LastIndex(importPath, "/")+1:]
	}
	return ""
}

// catalogParamNames lists the parameters of one function, method or interface
// method that are typed workflow.Catalog, under the file's local package name.
func catalogParamNames(fn *ast.FuncType, workflowName string) []int {
	if fn == nil || fn.Params == nil || workflowName == "" {
		return nil
	}
	indices := []int{}
	index := 0
	for _, field := range fn.Params.List {
		names := len(field.Names)
		if names == 0 {
			names = 1
		}
		if isCatalogType(field.Type, workflowName) {
			for offset := 0; offset < names; offset++ {
				indices = append(indices, index+offset)
			}
		}
		index += names
	}
	return indices
}

// isCatalogType reports whether a type expression is workflow.Catalog,
// including a pointer to it.
func isCatalogType(expr ast.Expr, workflowName string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Catalog" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == workflowName
}

// catalogParamNamesOf lists the names of a declaration's workflow.Catalog
// parameters, for the rule that a repository function may forward its own.
func catalogParamNameSet(fn *ast.FuncType, workflowName string) map[string]bool {
	names := map[string]bool{}
	if fn == nil || fn.Params == nil {
		return names
	}
	for _, field := range fn.Params.List {
		if !isCatalogType(field.Type, workflowName) {
			continue
		}
		for _, name := range field.Names {
			names[name.Name] = true
		}
	}
	return names
}

// isCatalogForCall reports whether an argument expression is a
// workflow.CatalogFor(...) call, under the calling file's local package name.
func isCatalogForCall(expr ast.Expr, workflowName string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "CatalogFor" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == workflowName
}

// TestEveryCompileSiteIsTenantScoped walks the repository and holds three rules
// about the one place visibility is decided.
func TestEveryCompileSiteIsTenantScoped(t *testing.T) {
	root := repoRoot(t)
	files := parseGoFiles(t, root)

	catalogIndexByFunction := map[string]int{}
	catalogDecls := map[string]repositoryCatalogDecl{}
	compileSites := 0

	for _, file := range files {
		workflowName := file.workflowName

		for _, decl := range file.file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Body == nil {
				continue
			}
			// A repository function may forward a catalogue it was handed: the
			// obligation moves to that function's callers, which rule 3 holds.
			forwarded := map[string]bool{}
			if file.underRepository {
				forwarded = catalogParamNameSet(funcDecl.Type, workflowName)
			}
			// Interface methods declare the same obligation without a body, so
			// they are collected from the type declarations below instead.
			for _, index := range catalogParamNames(funcDecl.Type, workflowName) {
				decl := repositoryCatalogDecl{name: funcDecl.Name.Name, index: index, pos: token.Position{Filename: file.relative}}
				if file.underRepository {
					if previous, seen := catalogDecls[funcDecl.Name.Name]; seen && previous.index != index {
						t.Errorf("%s: %q declares its workflow.Catalog parameter at index %d and %d; the caller rule cannot be derived",
							file.relative, funcDecl.Name.Name, previous.index, index)
					}
					catalogDecls[funcDecl.Name.Name] = decl
					catalogIndexByFunction[funcDecl.Name.Name] = index
				}
			}

			ast.Inspect(funcDecl.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if workflowName != "" && isWorkflowCall(call, workflowName, "Compile") {
					compileSites++
					checkCompileCatalog(t, file, call, forwarded, workflowName)
					return true
				}
				return true
			})
		}

		// Interface methods: a method set element with no body, which is how
		// the repository declares Activate and QueueManualLatest.
		for _, decl := range file.file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				iface, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok || iface.Methods == nil {
					continue
				}
				for _, method := range iface.Methods.List {
					fn, ok := method.Type.(*ast.FuncType)
					if !ok {
						continue
					}
					indices := catalogParamNames(fn, workflowName)
					if len(indices) == 0 {
						continue
					}
					for _, methodName := range method.Names {
						if !file.underRepository {
							continue
						}
						name := methodName.Name
						if previous, seen := catalogDecls[name]; seen && previous.index != indices[0] {
							t.Errorf("%s: interface method %q declares its workflow.Catalog parameter at index %d and %d",
								file.relative, name, previous.index, indices[0])
						}
						catalogDecls[name] = repositoryCatalogDecl{name: name, index: indices[0]}
						catalogIndexByFunction[name] = indices[0]
					}
				}
			}
		}
	}

	if compileSites == 0 {
		t.Fatal("no workflow.Compile call site was found; this rule is checking nothing")
	}
	checkPinnedSet(t, catalogDecls, catalogIndexByFunction)
	checkPinnedCallers(t, root, files, catalogIndexByFunction)
}

// isWorkflowCall reports whether a call is <workflowName>.<name>(...).
func isWorkflowCall(call *ast.CallExpr, workflowName, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == workflowName
}

// checkCompileCatalog holds rule 1: the document is compiled against a
// catalogue narrowed to a tenant, or against a catalogue this function was
// handed by somebody who narrowed it.
func checkCompileCatalog(t *testing.T, file goFile, call *ast.CallExpr, forwarded map[string]bool, workflowName string) {
	t.Helper()
	if len(call.Args) < 2 {
		return
	}
	argument := call.Args[1]
	if isCatalogForCall(argument, workflowName) {
		return
	}
	if file.underRepository {
		if ident, ok := argument.(*ast.Ident); ok && forwarded[ident.Name] {
			return
		}
	}
	t.Errorf(`%s: workflow.Compile is given a catalogue that was not narrowed to a tenant.
Bring the catalogue through workflow.CatalogFor(catalog, tenantID) with the tenant you compile for, or — inside internal/repository — forward a catalogue parameter of the enclosing function, whose callers rule 3 then holds.`,
		file.relative)
}

// checkPinnedSet holds rule 2: the repository functions taking a catalogue are
// exactly the pinned ones. A new one is a decision, and a rename must be loud.
func checkPinnedSet(t *testing.T, decls map[string]repositoryCatalogDecl, indices map[string]int) {
	t.Helper()
	declared := make([]string, 0, len(decls))
	for name := range decls {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	unexpected := []string{}
	for _, name := range declared {
		if !pinnedCatalogFunctions[name] {
			unexpected = append(unexpected, name)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf(`internal/repository declares functions taking a workflow.Catalog that this rule does not pin: %v.
A repository function that compiles a document must have its callers narrowed to a tenant. Add its name to pinnedCatalogFunctions (and to callersObligatedToScope unless it only forwards its own catalogue) once you have decided that, and the rule will hold its call sites to it.`,
			unexpected)
	}

	missing := []string{}
	for name := range pinnedCatalogFunctions {
		if _, found := decls[name]; !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("pinnedCatalogFunctions names %v, which no repository declaration takes a workflow.Catalog under any more. A rename has to update this list in the same commit.", missing)
	}
}

// checkPinnedCallers holds rule 3: every call to a pinned function passes a
// narrowed catalogue at the position its declaration puts the catalogue.
func checkPinnedCallers(t *testing.T, root string, files []goFile, catalogIndex map[string]int) {
	t.Helper()
	for _, file := range files {
		for _, decl := range file.file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Body == nil {
				continue
			}
			// A caller inside internal/repository that forwards its own
			// catalogue parameter is the publication path, not a call site.
			forwarded := map[string]bool{}
			if file.underRepository {
				forwarded = catalogParamNameSet(funcDecl.Type, file.workflowName)
			}
			ast.Inspect(funcDecl.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := selector.Sel.Name
				if !contains(callersObligatedToScope, name) {
					return true
				}
				index, pinned := catalogIndex[name]
				if !pinned {
					return true
				}
				// A call with too few arguments is some other method that
				// happens to share the name.
				if len(call.Args) <= index {
					return true
				}
				argument := call.Args[index]
				if file.workflowName != "" && isCatalogForCall(argument, file.workflowName) {
					return true
				}
				if ident, ok := argument.(*ast.Ident); ok && forwarded[ident.Name] {
					return true
				}
				t.Errorf(`%s: %s(...) is called with a catalogue that was not narrowed to a tenant.
Pass workflow.CatalogFor(catalog, tenantID) — the tenant whose document is being compiled — so a node type scoped to other tenants is refused instead of resolving.`,
					file.relative, name)
				return true
			})
		}
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
