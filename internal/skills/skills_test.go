// Package skills_test holds the drift gates of design §5.7 over the bundle the
// binary carries: the four assertions that keep a skill from teaching a verb,
// an operation, a node type or an expression root the product does not have.
//
// They live here, and not in the package under test, because internal/skills is
// a leaf: it reads markdown and JSON and reaches nothing else, so that a
// distroless image can install the bundle with no checkout to read. The product
// facts these gates resolve against are the product's, and a test package is
// where the two are allowed to meet. These are test-only imports, the same
// allowance internal/cli/openapi_contract_test.go takes when it imports
// internal/api: the CLI must never depend on the server in production code, and
// neither must a checker.
//
// Nothing here is a hand-maintained list. Every gate asks the product at run
// time, so a verb, an operation, a node type or a root another ticket adds is
// in scope the moment it exists, and a declaration naming one the product
// dropped fails the build. Which surface each gate reads, and why:
//
//	G1 commands          the registry the binary runs, through the `help --json`
//	                     document cli.Run prints from it (the registry itself is
//	                     unexported, which is the same reason
//	                     scripts/skills-command-reference reads that document)
//	G2 operations        the OpenAPI document of an in-process server built the
//	                     way the binary builds it, where every operation id a
//	                     handler registers through huma appears
//	G3 nodes             nodes.RegisterAll into a fresh node.Registry, the call
//	                     the server makes at startup
//	G4 expression roots  expression.Roots(), the allowlist the API serves to the
//	                     editor
//
// The bundle is loaded with skills.LoadBundle — the binary's own copy, not the
// checkout — because §5.7 gates what ships, and a reader of a distroless image
// has no checkout to consult instead.
package skills_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/cli"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/skills"
	"github.com/kilaslab/kilas-flow/nodes"
)

// binaryName is the word a fenced command starts with.
const binaryName = "kilasflow"

// fencedCommandFloor is how many fenced commands the gates must examine in the
// embedded bundle before they are allowed to report no drift.
//
// A gate that reads nothing passes for the same reason an empty test suite
// does, and the extraction below is the part of this file most able to stop
// matching silently: one bad prefix rule and every command disappears from the
// scan. The bundle names 130 of them today, so the floor is a floor rather than
// a count — adding or removing a skill does not have to touch this number,
// while an extractor that has stopped matching does.
const fencedCommandFloor = 100

// product is the four surfaces §5.7 resolves a declaration against, each as the
// set the declaration has to be a member of.
type product struct {
	verbs      map[string]bool
	operations map[string]bool
	nodes      map[string]bool
	roots      map[string]bool
}

// gateResult is one gate's answer: the declarations it refused, and how many
// it examined on the way.
type gateResult struct {
	drift    []string
	examined int
}

// gate is one of §5.7's four assertions.
type gate struct {
	name   string
	report func([]skills.Skill, product) gateResult
}

// gates is §5.7's table, in the order the design lists it. G1 appears once and
// covers both halves of its assertion — a declared command and a fenced one are
// two ways of naming the same verb.
var gates = []gate{
	{name: "G1 commands", report: commandDrift},
	{name: "G2 operations", report: operationDrift},
	{name: "G3 nodes", report: nodeDrift},
	{name: "G4 expression roots", report: rootDrift},
}

// commandDrift is G1 as the design states it: the declared commands and the
// commands the fences show.
func commandDrift(loaded []skills.Skill, facts product) gateResult {
	declared := declaredCommandDrift(loaded, facts)
	fenced := fencedCommandDrift(loaded, facts)

	return gateResult{
		drift:    append(declared.drift, fenced.drift...),
		examined: declared.examined + fenced.examined,
	}
}

// Every command the frontmatter declares is a verb this binary implements.
func TestG1DeclaredCommandsResolve(t *testing.T) {
	report(t, declaredCommandDrift(embeddedBundle(t), loadProduct(t)))
}

// Every command a fenced block names is a verb this binary implements, and the
// gate examined a plausible number of them rather than none.
func TestG1FencedCommandsResolve(t *testing.T) {
	result := fencedCommandDrift(embeddedBundle(t), loadProduct(t))

	report(t, result)

	if result.examined < fencedCommandFloor {
		t.Fatalf("the gate examined %d fenced commands, want at least %d: the extraction has stopped matching",
			result.examined, fencedCommandFloor)
	}
}

// Every declared operation answers to an operation id the server registers.
func TestG2OperationsExist(t *testing.T) {
	report(t, operationDrift(embeddedBundle(t), loadProduct(t)))
}

// Every declared node type is one nodes.RegisterAll registers.
func TestG3NodeTypesExist(t *testing.T) {
	report(t, nodeDrift(embeddedBundle(t), loadProduct(t)))
}

// Every declared expression root is one the grammar allows.
func TestG4ExpressionRootsExist(t *testing.T) {
	report(t, rootDrift(embeddedBundle(t), loadProduct(t)))
}

// report fails the test for every declaration a gate refused, and refuses to
// pass a gate that examined nothing.
func report(t *testing.T, result gateResult) {
	t.Helper()

	for _, message := range result.drift {
		t.Error(message)
	}
	if result.examined == 0 {
		t.Fatal("the gate examined no declarations at all")
	}
}

// embeddedBundle parses the bundle the binary carries, reference text included:
// a command written in references/FILTERS.md is a command an agent will type.
func embeddedBundle(t *testing.T) []skills.Skill {
	t.Helper()

	loaded, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("load the embedded bundle: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("the embedded bundle holds no skills")
	}

	return loaded
}

// loadProduct reads the product's own answer for each of the four surfaces.
func loadProduct(t *testing.T) product {
	t.Helper()

	return product{
		verbs:      verbPaths(t),
		operations: operationIDs(t),
		nodes:      nodeTypes(t),
		roots:      stringSet(expression.Roots()),
	}
}

// verbPaths reads the command tree the way an agent reads it: the document
// `help --json` prints, produced by cli.Run in-process.
//
// In-process rather than by building a binary, so the gate stays an ordinary
// test that runs in `go test ./...` with no toolchain and no second copy of the
// verb list: Run is the composition cmd/kilasflow executes, and this reads the
// registry the binary itself would run.
func verbPaths(t *testing.T) map[string]bool {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code, handled := cli.Run(cli.Env{
		Args:   []string{"help", "--json"},
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(string) string { return "" },
	})
	if !handled {
		t.Fatal("`help --json` was not claimed by the CLI")
	}
	if code != cli.ExitOK {
		t.Fatalf("`help --json` exited %d: %s", code, strings.TrimSpace(stderr.String()))
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data []struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode `help --json`: %v\n%s", err, stdout.String())
	}
	if !envelope.OK {
		t.Fatalf("`help --json` reported a failure: %s", stdout.String())
	}

	paths := make(map[string]bool, len(envelope.Data))
	for _, verb := range envelope.Data {
		paths[verb.Path] = true
	}
	if len(paths) == 0 {
		t.Fatal("`help --json` listed no verbs")
	}

	return paths
}

// gatePinger stands in for the database: a Pinger is all NewServer needs to
// register the whole route tree without one.
type gatePinger struct{}

func (gatePinger) Ping(context.Context) error { return nil }

// operationIDs reads every operation id the server registers, out of the
// OpenAPI document of an in-process server built the way the binary builds it.
//
// The document rather than a scan of internal/api/handlers: it is what the
// product publishes, it carries the id huma took from each registration rather
// than a literal someone typed, and it costs one httptest server.
func operationIDs(t *testing.T) map[string]bool {
	t.Helper()

	server := httptest.NewServer(api.NewServer(api.Deps{
		Config:  config.Default(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:      gatePinger{},
		Version: "0.0.0-skills-gates",
	}).Handler())
	t.Cleanup(server.Close)

	documentPath := api.OpenAPIPath + ".json"

	response, err := http.Get(server.URL + documentPath)
	if err != nil {
		t.Fatalf("read %s: %v", documentPath, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %s", documentPath, response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", documentPath, err)
	}

	var document struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode %s: %v", documentPath, err)
	}

	ids := map[string]bool{}
	for _, methods := range document.Paths {
		for _, operation := range methods {
			if operation.OperationID != "" {
				ids[operation.OperationID] = true
			}
		}
	}
	if len(ids) == 0 {
		t.Fatalf("%s names no operation ids", documentPath)
	}

	return ids
}

// nodeTypes registers the built-ins the way the server does at startup and
// reads the catalogue back.
func nodeTypes(t *testing.T) map[string]bool {
	t.Helper()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("register the built-in nodes: %v", err)
	}

	definitions := registry.List()
	if len(definitions) == 0 {
		t.Fatal("the registry holds no node types")
	}

	types := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		types[definition.Type] = true
	}

	return types
}

// stringSet turns a product's list into the set a lookup is made against.
func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}

	return set
}

// declaredCommandDrift reports every declared command that is not a verb path.
//
// A declaration is the verb and nothing else — `kilasflow credential list` — so
// the comparison is exact: `kilasflow workflow get <id>` would declare an
// invocation, and an invocation is not a claim a command tree can confirm.
func declaredCommandDrift(loaded []skills.Skill, facts product) gateResult {
	result := gateResult{}

	for _, skill := range loaded {
		for _, command := range skill.Commands {
			result.examined++

			verb := strings.TrimSpace(strings.TrimPrefix(command, binaryName))
			if facts.verbs[verb] {
				continue
			}

			result.drift = append(result.drift, fmt.Sprintf("%s: kilasflow_commands declares %q, which is not a verb in the binary's command tree",
				skill.Path, command))
		}
	}

	return result
}

// fencedCommandDrift reports every command a fenced block names whose verb the
// command tree does not have.
//
// This is the strict half of G1: a fence is where a reader copies a command
// from, so a verb that does not exist there is an agent typing something the
// binary will refuse, which is exactly the drift the audit of 2026-09-20 found
// in the n8n material.
func fencedCommandDrift(loaded []skills.Skill, facts product) gateResult {
	result := gateResult{}

	for _, document := range bundleDocuments(loaded) {
		for _, command := range fencedCommands(document.text) {
			result.examined++

			if resolvesVerb(strings.Fields(strings.TrimPrefix(command, binaryName+" ")), facts.verbs) {
				continue
			}

			result.drift = append(result.drift, fmt.Sprintf("%s: the fenced command %q names no verb this binary implements",
				document.path, command))
		}
	}

	return result
}

// operationDrift reports every declared operation id the server does not serve.
func operationDrift(loaded []skills.Skill, facts product) gateResult {
	return declarationDrift(loaded, facts.operations, "kilasflow_operations",
		func(skill skills.Skill) []string { return skill.Operations })
}

// nodeDrift reports every declared node type the catalogue does not register.
func nodeDrift(loaded []skills.Skill, facts product) gateResult {
	return declarationDrift(loaded, facts.nodes, "kilasflow_nodes",
		func(skill skills.Skill) []string { return skill.Nodes })
}

// rootDrift reports every declared expression root the grammar does not allow.
func rootDrift(loaded []skills.Skill, facts product) gateResult {
	return declarationDrift(loaded, facts.roots, "kilasflow_expression_roots",
		func(skill skills.Skill) []string { return skill.ExpressionRoots })
}

// declarationDrift is the shape the three list-shaped gates share: every entry
// of one frontmatter list has to be a member of one product set.
func declarationDrift(loaded []skills.Skill, known map[string]bool, key string, values func(skills.Skill) []string) gateResult {
	result := gateResult{}

	for _, skill := range loaded {
		for _, value := range values(skill) {
			result.examined++

			if known[value] {
				continue
			}

			result.drift = append(result.drift, fmt.Sprintf("%s: %s declares %q, which the product does not have",
				skill.Path, key, value))
		}
	}

	return result
}

// bundleDocument is one markdown document the gates read: a skill body, or one
// of its reference files, named the way the repository names it.
type bundleDocument struct {
	path string
	text string
}

// bundleDocuments lists every document of every skill: the body first, then the
// references in the order the loader read them, so a finding names a file
// rather than a skill.
func bundleDocuments(loaded []skills.Skill) []bundleDocument {
	documents := make([]bundleDocument, 0, len(loaded))
	for _, skill := range loaded {
		documents = append(documents, bundleDocument{path: skill.Path, text: skill.Body})

		for _, reference := range skill.References {
			documents = append(documents, bundleDocument{
				path: path.Join(path.Dir(skill.Path), reference),
				text: skill.ReferenceText[reference],
			})
		}
	}

	return documents
}

// fencedCommands returns every command a document's fenced blocks name, as the
// word `kilasflow` followed by the words written after it.
func fencedCommands(document string) []string {
	var commands []string

	for _, block := range fencedBlocks(document) {
		for _, line := range strings.Split(block, "\n") {
			commands = append(commands, lineCommands(line)...)
		}
	}

	return commands
}

// fencedBlocks returns the text of every fenced block, in order.
//
// The reading is the checker's, in check.go: a line whose trimmed form opens
// with ``` toggles the state, and an unterminated block still counts as one —
// a gate that skipped the text after a fence somebody forgot to close would be
// a gate a typo could hide behind.
func fencedBlocks(document string) []string {
	var (
		blocks []string
		block  []string
		inside bool
	)

	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inside {
				blocks = append(blocks, strings.Join(block, "\n"))
			}
			block, inside = nil, !inside

			continue
		}
		if inside {
			block = append(block, line)
		}
	}
	if inside {
		blocks = append(blocks, strings.Join(block, "\n"))
	}

	return blocks
}

// lineCommands returns every command one line of a fence names.
func lineCommands(line string) []string {
	var commands []string

	for offset := 0; ; {
		found := strings.Index(line[offset:], binaryName)
		if found < 0 {
			return commands
		}

		start := offset + found
		offset = start + len(binaryName)

		if !commandPosition(line, start) {
			continue
		}

		words := commandWords(line[offset:])
		if len(words) == 0 {
			continue
		}

		commands = append(commands, binaryName+" "+strings.Join(words, " "))
	}
}

// commandPosition reports whether the `kilasflow` at start is a command rather
// than a value some other program was given.
//
// Everything a fence names is a command except the two words of the upgrade
// notes in kilasflow-operations/references/UPGRADE_ORDER.md, where
// `pg_dump -U kilasflow kilasflow` happens to spell the database user and the
// database name the way the binary is spelled. A value is recognisable without
// knowing that line: it follows a flag, or it follows the value that flag was
// given. Nothing else is skipped.
//
// That asymmetry is deliberate. A gate that skipped a `kilasflow` it could not
// place would miss a renamed verb silently, which is the drift §5.7 exists to
// catch; a gate that reads one as a command when it is prose fails loudly and
// names the line, and the author either fixes the verb or moves the sentence
// out of the fence.
func commandPosition(line string, start int) bool {
	// A longer word is not the binary: `kilasflow.sql`, `kilasflow-credentials`.
	if start > 0 && (isWordByte(line[start-1]) || line[start-1] == '.') {
		return false
	}

	// A command is followed by a word, never by punctuation, a file extension
	// or the `(` of a mention like `kilasflow(1)`.
	end := start + len(binaryName)
	if end >= len(line) || (line[end] != ' ' && line[end] != '\t') {
		return false
	}

	previous := previousWord(line[:start])

	// The value of a flag, as in `-U kilasflow`.
	if isFlag(previous) {
		return false
	}

	// The word after such a value, as in `-U kilasflow kilasflow`: the first is
	// the user, the second the database, and neither is a command.
	return previous != binaryName
}

// previousWord is the word before a position, or "" at the start of a line.
func previousWord(before string) string {
	before = strings.TrimRight(before, " \t")
	if cut := strings.LastIndexAny(before, " \t"); cut >= 0 {
		return before[cut+1:]
	}

	return before
}

// isFlag reports whether a word is a flag rather than punctuation that happens
// to start with a dash: `-U` and `--credential` are flags, `->` is an arrow.
func isFlag(word string) bool {
	trimmed := strings.TrimLeft(word, "-")
	if trimmed == word || trimmed == "" {
		return false
	}

	first := trimmed[0]

	return first == '_' ||
		(first >= '0' && first <= '9') ||
		(first >= 'a' && first <= 'z') ||
		(first >= 'A' && first <= 'Z')
}

// commandWords reads the words of a command: the rest of the line, with the
// punctuation a sentence continues with trimmed off each word, so the command
// in `kilasflow workflow get <workflowId>: active=false` still resolves.
func commandWords(rest string) []string {
	fields := strings.Fields(rest)

	words := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimRight(field, ",;:.)`\\"); field != "" {
			words = append(words, field)
		}
	}

	return words
}

// resolvesVerb reports whether the words begin with a verb the tree has.
//
// The longest matching prefix is what counts, and only the prefix: what follows
// the verb is the invocation's own arguments — `kilasflow workflow get <id>`,
// `kilasflow api create-credential --body @cred.json` — which the CLI judges
// when it runs, not something a fence has to spell the way this gate would.
// `kilasflow workflow frobnicate` fails here because no path in the tree is the
// family alone: every verb is the family and its leaf.
func resolvesVerb(words []string, verbs map[string]bool) bool {
	for length := len(words); length > 0; length-- {
		if verbs[strings.Join(words[:length], " ")] {
			return true
		}
	}

	return false
}

// isWordByte reports whether a byte continues a word, which is what keeps
// `kilasflow-credentials` and `kilasflow.sql` out of the scan.
func isWordByte(value byte) bool {
	return value == '-' || value == '_' ||
		(value >= '0' && value <= '9') ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		value >= 0x80
}

// plantedDeclaration is one value a gate has to refuse, and where it is planted.
type plantedDeclaration struct {
	// name is what the case is called in a test run.
	name string
	// gate is the gate's name in `gates`.
	gate string
	// key is the frontmatter list the value is planted in, or "" to plant it in
	// the body's fenced block instead.
	key string
	// value is what the gate must refuse.
	value string
}

// Every gate must refuse a value the product does not have, and every gate must
// have such a case: a gate with no negative proof is a gate nobody has seen
// work. The planted documents go through skills.Parse, the loader the bundle
// itself goes through, so each case also proves the document was one the
// contract accepts — a fixture that failed to load would prove nothing about
// the check that follows it.
var plantedDeclarations = []plantedDeclaration{
	{name: "declared command", gate: "G1 commands", key: "kilasflow_commands", value: "kilasflow workflow frobnicate"},
	{name: "fenced command", gate: "G1 commands", value: "kilasflow workflow frobnicate"},
	{name: "operation", gate: "G2 operations", key: "kilasflow_operations", value: "run-wrkflow"},
	{name: "node type", gate: "G3 nodes", key: "kilasflow_nodes", value: "kilasflow.htpRequest"},
	{name: "expression root", gate: "G4 expression roots", key: "kilasflow_expression_roots", value: "$jsoon"},
}

// Each gate refuses the planted value, and every gate has a case proving it.
func TestGatesRefusePlantedDeclarations(t *testing.T) {
	facts := loadProduct(t)
	proven := map[string]bool{}

	for _, planted := range plantedDeclarations {
		t.Run(planted.name, func(t *testing.T) {
			subject, found := gateNamed(planted.gate)
			if !found {
				t.Fatalf("no gate is named %q", planted.gate)
			}
			proven[planted.gate] = true

			result := subject.report([]skills.Skill{plantedSkill(t, planted)}, facts)

			for _, message := range result.drift {
				if strings.Contains(message, planted.value) {
					return
				}
			}

			t.Fatalf("the %s gate accepted %q; it reported %v", planted.gate, planted.value, result.drift)
		})
	}

	for _, subject := range gates {
		if !proven[subject.name] {
			t.Errorf("the %s gate has no planted value proving it refuses anything", subject.name)
		}
	}
}

// gateNamed finds a gate by the name the planted cases address it by.
func gateNamed(name string) (gate, bool) {
	for _, subject := range gates {
		if subject.name == name {
			return subject, true
		}
	}

	return gate{}, false
}

// plantedSkill parses the document one planted case describes.
func plantedSkill(t *testing.T, planted plantedDeclaration) skills.Skill {
	t.Helper()

	parsed, err := skills.Parse("kilasflow-fixture", []byte(gateDocument(planted)))
	if err != nil {
		t.Fatalf("parse the planted document: %v", err)
	}

	return parsed
}

// declarationKeys are the four lists the gates read, in the order the fixture
// writes them; gateDefaults holds the value each one declares unless the case
// plants something else.
var declarationKeys = []string{
	"kilasflow_commands",
	"kilasflow_operations",
	"kilasflow_nodes",
	"kilasflow_expression_roots",
}

// gateDefaults are the values a fixture declares for the lists it does not
// plant a violation in, so one refusal is one gate's.
var gateDefaults = map[string]string{
	"kilasflow_commands":         "kilasflow workflow get",
	"kilasflow_operations":       "get-workflow",
	"kilasflow_nodes":            "kilasflow.set",
	"kilasflow_expression_roots": "$json",
}

// gateBody is the body a fixture carries when the violation is in its
// frontmatter: a fenced block that names a verb the tree has.
const gateBody = `
## Decision tree

` + "```" + `
kilasflow workflow get <workflowId>
` + "```" + `
`

// plantedBody is the body of a fixture whose violation is a fenced command: the
// case's own value, run as a command with an argument, so the text the gate has
// to refuse is the text the case named.
func plantedBody(planted plantedDeclaration) string {
	return "\n## Decision tree\n\n```\nkilasflow workflow get <workflowId>\n" + planted.value + " <workflowId>\n```\n"
}

// gateDocument renders the SKILL.md one planted case describes.
func gateDocument(planted plantedDeclaration) string {
	values := make(map[string]string, len(declarationKeys))
	for _, key := range declarationKeys {
		values[key] = gateDefaults[key]
	}
	if planted.key != "" {
		values[planted.key] = planted.value
	}

	var document strings.Builder
	document.WriteString("---\n")
	document.WriteString("name: kilasflow-fixture\n")
	document.WriteString("description: Use when a drift gate is exercised. Triggers on \"gate\".\n")
	document.WriteString("kilasflow_skills_version: 1\n")
	for _, key := range declarationKeys {
		fmt.Fprintf(&document, "%s:\n  - %q\n", key, values[key])
	}
	document.WriteString("kilasflow_not_shipped: []\n")
	document.WriteString("---\n")

	body := gateBody
	if planted.key == "" {
		body = plantedBody(planted)
	}
	document.WriteString(body)

	return document.String()
}

// The honesty test of §8 is the checker's not-shipped rule: a non-empty
// kilasflow_not_shipped has to be named in a '## Not shipped yet' section, and
// a section has to have a declaration behind it. It is not re-implemented here
// because it already covers the claim end to end — check.go's checkNotShipped,
// with testdata/planted/not-shipped-missing-section and .../not-shipped-unnamed-entry
// proving it fires in both directions (check_test.go) — and what this asserts is
// the artifact the gates above read: the bundle the binary carries passes every
// rule, so the claim an agent loads is the claim the frontmatter makes.
func TestEmbeddedBundlePassesItsOwnChecker(t *testing.T) {
	for _, finding := range skills.Check(embeddedBundle(t)) {
		t.Errorf("%s: %s: %s", finding.Skill, finding.Rule, finding.Message)
	}
}
