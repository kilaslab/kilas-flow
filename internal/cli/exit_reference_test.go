package cli

// BUG-b4cb1c: cli.md stated the exit codes by hand, and its error-code
// vocabulary had already fallen behind the binary (mcp_catalogue, mcp_transport
// and unexpected_shape were raised and never named). The tables are now written
// from this package's own table by scripts/docs-facts, and a second test reads
// the source for every error code the CLI can raise.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const cliReferencePath = "../../docs/src/content/docs/reference/cli.md"

const (
	exitCodesBegin = "<!-- generated:exit-codes -->"
	exitCodesEnd   = "<!-- /generated:exit-codes -->"
)

func TestCliMdExitCodeSectionIsGenerated(t *testing.T) {
	raw, err := os.ReadFile(cliReferencePath)
	if err != nil {
		t.Fatalf("read cli.md: %v", err)
	}
	page := string(raw)
	if strings.Count(page, exitCodesBegin) != 1 || strings.Count(page, exitCodesEnd) != 1 {
		t.Fatalf("cli.md must carry exactly one %s ... %s block around the exit-code tables; "+
			"run `make generate-docs-facts`", exitCodesBegin, exitCodesEnd)
	}
	from := strings.Index(page, exitCodesBegin) + len(exitCodesBegin)
	to := strings.Index(page, exitCodesEnd)
	if to < from {
		t.Fatalf("cli.md has %s before %s", exitCodesEnd, exitCodesBegin)
	}

	if got, want := strings.TrimSpace(page[from:to]), strings.TrimSpace(RenderExitCodeReference()); got != want {
		t.Errorf("cli.md's exit-code tables are not the ones the binary renders; run `make generate-docs-facts`\n"+
			"committed:\n%s\n\nrendered:\n%s", got, want)
	}
}

// The table is the contract's own list, so a constant added to exit.go without a
// row, or a row without a constant, is the drift this exists to catch.
func TestExitCodeTableHasARowForEveryExitConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "exit.go", nil, 0)
	if err != nil {
		t.Fatalf("parse exit.go: %v", err)
	}
	constants := map[int]string{}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			value := spec.(*ast.ValueSpec)
			for index, name := range value.Names {
				if !strings.HasPrefix(name.Name, "Exit") || index >= len(value.Values) {
					continue
				}
				literal, ok := value.Values[index].(*ast.BasicLit)
				if !ok {
					continue
				}
				number, err := strconv.Atoi(literal.Value)
				if err != nil {
					t.Fatalf("%s = %s is not an integer", name.Name, literal.Value)
				}
				constants[number] = name.Name
			}
		}
	}
	if len(constants) < 7 {
		t.Fatalf("found %d Exit* constants in exit.go, want at least 7; the test is reading the wrong thing", len(constants))
	}

	rows := map[int]ExitCodeDoc{}
	for _, row := range ExitCodes() {
		if _, repeated := rows[row.Code]; repeated {
			t.Errorf("ExitCodes() lists code %d twice", row.Code)
		}
		if strings.TrimSpace(row.Meaning) == "" || strings.TrimSpace(row.Response) == "" {
			t.Errorf("ExitCodes() row %d has an empty meaning or response", row.Code)
		}
		rows[row.Code] = row
	}
	for number, name := range constants {
		if _, found := rows[number]; !found {
			t.Errorf("%s = %d has no row in ExitCodes()", name, number)
		}
	}
	for number := range rows {
		if _, found := constants[number]; !found {
			t.Errorf("ExitCodes() documents code %d, which no Exit* constant names", number)
		}
	}
}

// The status table is built by asking exitForStatus, not by restating it, so the
// check here is that the rendering carries what the function answers.
func TestStatusExitsAreWhatExitForStatusAnswers(t *testing.T) {
	rendered := RenderExitCodeReference()
	for _, want := range []string{
		"| `200`, `201`, `202`, `204` | 0 | — |",
		"| `400`, `422` | 2 | `bad_request` |",
		"| `401` | 3 | `unauthenticated` |",
		"| `403` | 3 | `scope_denied` |",
		"| `404` | 4 | `not_found` |",
		"| `409` | 5 | `conflict` |",
		"| `429` | 6 | `rate_limited` |",
		"| `503` | 6 | `not_ready` |",
		"| any other `5xx` | 1 | `server_error` |",
		"| any other status | 1 | `http_error` |",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderExitCodeReference() has no row %q", want)
		}
	}

	for _, row := range StatusExits() {
		if strings.TrimSpace(row.Statuses) == "" {
			t.Errorf("StatusExits() has a row with no statuses: %+v", row)
		}
	}
}

// Code 3 is three refusals told apart by error.code, so each code in its table
// has to be one the CLI really uses with that exit.
func TestRefusalTableNamesTheCodesCodeThreeCarries(t *testing.T) {
	var codes []string
	for _, row := range refusals() {
		codes = append(codes, row.ErrCode)
		if strings.TrimSpace(row.Means) == "" {
			t.Errorf("refusal %q has no meaning", row.ErrCode)
		}
	}
	sort.Strings(codes)
	want := []string{confirmationCode, scopeDeniedCode, "unauthenticated"}
	sort.Strings(want)
	if strings.Join(codes, ",") != strings.Join(want, ",") {
		t.Fatalf("refusals() names %v, want %v", codes, want)
	}
	if exit, code := exitForStatus(401); exit != ExitRefused || code != "unauthenticated" {
		t.Errorf("exitForStatus(401) = %d %q, want %d unauthenticated", exit, code, ExitRefused)
	}
	if exit, code := exitForStatus(403); exit != ExitRefused || code != scopeDeniedCode {
		t.Errorf("exitForStatus(403) = %d %q, want %d %s", exit, code, ExitRefused, scopeDeniedCode)
	}
}

// raisedErrorCodes reads the CLI's own source for every error code it can put in
// error.code: each `ErrCode:` field of a composite literal (a string, or a
// constant the package declares) and each code exitForStatus returns.
func raisedErrorCodes(t *testing.T) map[string]string {
	t.Helper()
	fileSet := token.NewFileSet()
	packages, err := parser.ParseDir(fileSet, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse internal/cli: %v", err)
	}
	pkg, found := packages["cli"]
	if !found {
		t.Fatal("package cli not found in the working directory")
	}

	constants := map[string]string{}
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for index, name := range value.Names {
					if index >= len(value.Values) {
						continue
					}
					if literal, ok := value.Values[index].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						text, _ := strconv.Unquote(literal.Value)
						constants[name.Name] = text
					}
				}
			}
		}
	}

	raised := map[string]string{}
	record := func(expression ast.Expr, where string) {
		switch value := expression.(type) {
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return
			}
			text, _ := strconv.Unquote(value.Value)
			if text != "" {
				raised[text] = where
			}
		case *ast.Ident:
			if text, ok := constants[value.Name]; ok {
				if text != "" {
					raised[text] = where
				}

				return
			}
			// A variable (client.go builds one from a status) is covered by
			// the exitForStatus codes below; an unknown name is a code this
			// test cannot see, which is worth failing for.
			if value.Name != "errCode" && value.Name != "code" {
				t.Errorf("%s: ErrCode is %s, which is neither a string literal nor a constant this test can resolve; "+
					"give it one or teach the test", where, value.Name)
			}
		}
	}

	for name, file := range pkg.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.CompositeLit:
				// Only an ExitError's field is an error code the CLI raises; the
				// documentation tables in exit.go carry the same field name.
				if named, ok := current.Type.(*ast.Ident); !ok || named.Name != "ExitError" {
					return true
				}
				for _, element := range current.Elts {
					pair, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "ErrCode" {
						record(pair.Value, fileSet.Position(pair.Pos()).String())
					}
				}
			case *ast.FuncDecl:
				if current.Name.Name != "exitForStatus" {
					return true
				}
				ast.Inspect(current, func(inner ast.Node) bool {
					if returned, ok := inner.(*ast.ReturnStmt); ok && len(returned.Results) == 2 {
						record(returned.Results[1], name+": exitForStatus")
					}

					return true
				})
			}

			return true
		})
	}

	return raised
}

func TestEveryErrorCodeTheCLIRaisesIsDocumented(t *testing.T) {
	raw, err := os.ReadFile(cliReferencePath)
	if err != nil {
		t.Fatalf("read cli.md: %v", err)
	}
	page := string(raw)

	raised := raisedErrorCodes(t)
	if len(raised) < 15 {
		t.Fatalf("found only %d error codes in the CLI's source (%v); the test is reading the wrong thing", len(raised), raised)
	}
	codes := make([]string, 0, len(raised))
	for code := range raised {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if !strings.Contains(page, "`"+code+"`") {
			t.Errorf("cli.md never names the error code `%s` (raised at %s) in backticks; add it to the vocabulary "+
				"under \"Exit codes\" or to the section of the verb that raises it", code, raised[code])
		}
	}
}
