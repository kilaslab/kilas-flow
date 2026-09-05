package corpus_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
	"github.com/kilaslabs/kilas-flow/internal/interop/n8n/corpus"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
	"github.com/kilaslabs/kilas-flow/packs/waha"
)

// The scoreboard is a measuring instrument, not a pass/fail suite. Its job is to
// say how much of the real n8n ecosystem imports, activates and runs today, and
// to make a later ticket's diff show exactly which workflows it moved.
//
// The three tiers are defined here so nobody re-argues them:
//
//	imported     n8n.Import returned no error.
//	activatable  workflow.Compile returned no error — the same authority
//	             activation goes through, not a second weaker one.
//	runnable     the workflow ran to completion with every outbound call
//	             refused by policy, so no network or database is touched.
//
// Tier three never makes a network call. A regression instrument that needs a
// live WAHA server is an instrument that gets disabled.
//
// That has a consequence worth stating in the report rather than hiding in the
// numbers: a workflow whose graph executes perfectly still fails tier three if
// any node would have called out, because the offline policy refuses it. That
// is a limit of the instrument, not a defect in the workflow, so the two are
// distinguished — `blocked` marks a run stopped by the offline policy, and only
// a fixture that is neither runnable nor blocked actually failed to execute.
// Engine tickets should watch `blocked` fall as nodes stop needing the network,
// and watch genuine failures fall as the engine improves.
//
// Fixture payloads never reach the report or the test log. A real client
// workflow holds live phone numbers, WAHA hostnames and credential names, and
// the private overlay is scored by exactly this code.

var updateBaseline = flag.Bool("update-baseline", false, "rewrite BASELINE.md and baseline.json from this run")

const (
	tierImported    = "imported"
	tierActivatable = "activatable"
	tierRunnable    = "runnable"
)

// score is one fixture's result. Reason explains the first tier it failed at,
// and is drawn from KilasFlow's own error text, never from fixture content.
type score struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Nodes       int    `json:"nodes"`
	Imported    bool   `json:"imported"`
	Activatable bool   `json:"activatable"`
	Runnable    bool   `json:"runnable"`
	// Blocked marks a run that the offline policy stopped — an outbound HTTP
	// or database call the corpus deliberately refuses — rather than an engine
	// failure. It is a property of the measurement, not of the workflow.
	Blocked     bool   `json:"blocked,omitempty"`
	Unsupported int    `json:"unsupported"`
	Reason      string `json:"reason,omitempty"`
}

// baseline is the committed golden. Unexplained drift fails the test; a ticket
// that moves a workflow rewrites it with -update-baseline and says why.
type baseline struct {
	Comment string         `json:"comment"`
	Tiers   map[string]int `json:"tiers"`
	Total   int            `json:"total"`
	// Blocked counts fixtures that compiled and began running but were stopped
	// by the offline policy. See the note on score.Blocked.
	Blocked   int            `json:"blocked"`
	NodeTypes map[string]int `json:"nodeTypeInventory"`
	Scores    []score        `json:"scores"`
}

// corpusRuntime is the catalogue and the runtime that scores against it.
//
// They are built together because a generated node pack registers into both at
// once — a definition bound to an executor this runtime has not installed is
// refused — and because the routing interpreter needs the same routing registry
// the packs wrote into. Two independently built registries would give a WAHA
// workflow a definition it could compile against and no executor to run.
func corpusRuntime(t *testing.T) (*node.Registry, *engine.Registry) {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	// The HTTP policy allows exactly one host that cannot resolve, so any
	// outbound call is refused by policy before a socket is opened rather than
	// trusted not to be attempted.
	offline := safehttp.Policy{
		AllowedHosts:     []string{"corpus.invalid"},
		MaxRedirects:     0,
		MaxResponseBytes: 1 << 10,
		Timeout:          time.Second,
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, offline, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	routes := routing.NewRegistry()
	triggers := nodepack.NewTriggerRegistry()
	for id, executor := range map[string]engine.Executor{
		routing.ExecutorID:         routing.NewExecutor(offline, routes, catalog),
		nodepack.TriggerExecutorID: nodepack.NewTriggerExecutor(triggers, offline),
	} {
		if err := executors.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	if err := waha.Register(waha.Deps{
		Definitions: catalog, Routes: routes, Triggers: triggers,
		Deliveries: webhook.NewRegistry(), Lifecycles: webhook.NewLifecycleRegistry(),
		Executors: executors, Options: loadoptions.NewResolver(offline, 0),
	}); err != nil {
		t.Fatalf("waha.Register() error = %v", err)
	}
	return catalog, executors
}

// firstLine keeps a reason to one readable line. Compiler errors aggregate, and
// a multi-line reason turns the report into something nobody scans.
func firstLine(err error) string {
	text := strings.TrimSpace(err.Error())
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	if len(text) > 160 {
		text = text[:157] + "…"
	}
	return text
}

func scoreFixture(t *testing.T, fixture corpus.Fixture, catalog workflow.Catalog, executors *engine.Registry) score {
	t.Helper()
	result := score{Name: fixture.Name, Source: string(fixture.Source)}

	imported, err := n8n.Import(fixture.Payload, catalog)
	if err != nil {
		// A refusal is often correct — duplicate node names, for one — so the
		// reason is recorded, not just the boolean. At corpus scale a bare
		// false would read as an importer regression.
		result.Reason = firstLine(err)
		return result
	}
	result.Imported = true
	result.Nodes = len(imported.Document.Nodes)
	result.Unsupported = len(imported.Unsupported)

	document := imported.Document
	document.ID = "wf_corpus"
	compiled, err := workflow.Compile(document, catalog)
	if err != nil {
		result.Reason = firstLine(err)
		return result
	}
	result.Activatable = true

	if _, err := engine.NewRunner(executors).Run(context.Background(), compiled, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	}); err != nil {
		result.Reason = firstLine(err)
		result.Blocked = errors.Is(err, safehttp.ErrBlocked) || errors.Is(err, sqlnode.ErrForbiddenTarget)
		return result
	}
	result.Runnable = true
	return result
}

// nodeTypeInventory counts raw n8n node types across the corpus. It is what p3
// and p4 pick their targets from, so it is measured rather than asserted.
func nodeTypeInventory(fixtures []corpus.Fixture) map[string]int {
	inventory := map[string]int{}
	for _, fixture := range fixtures {
		var document struct {
			Nodes []struct {
				Type string `json:"type"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(fixture.Payload, &document); err != nil {
			continue
		}
		for _, current := range document.Nodes {
			// A bare type name is format, not content: it names a node kind,
			// never a client's data.
			inventory[current.Type]++
		}
	}
	return inventory
}

func TestCorpusScoreboard(t *testing.T) {
	if !corpus.Available() {
		t.Skipf("no corpus materialised; fetch it with: %s", corpus.SyncCommand)
	}

	fixtures, err := corpus.Load()
	if err != nil {
		t.Fatalf("corpus.Load() error = %v", err)
	}

	catalog, executors := corpusRuntime(t)

	scores := make([]score, 0, len(fixtures))
	tiers := map[string]int{tierImported: 0, tierActivatable: 0, tierRunnable: 0}
	blocked := 0
	var public []corpus.Fixture
	for _, fixture := range fixtures {
		// The private overlay is scored but never recorded: its rows would put
		// a client's workflow names into a committed file.
		current := scoreFixture(t, fixture, catalog, executors)
		if fixture.Source == corpus.SourcePrivate {
			t.Logf("private fixture scored: imported=%t activatable=%t runnable=%t",
				current.Imported, current.Activatable, current.Runnable)
			continue
		}
		public = append(public, fixture)
		if current.Imported {
			tiers[tierImported]++
		}
		if current.Activatable {
			tiers[tierActivatable]++
		}
		if current.Runnable {
			tiers[tierRunnable]++
		}
		if current.Blocked {
			blocked++
		}
		scores = append(scores, current)
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Source != scores[j].Source {
			return scores[i].Source < scores[j].Source
		}
		return scores[i].Name < scores[j].Name
	})

	measured := baseline{
		Comment: "Baseline for the n8n importer regression corpus. Tiers: imported = n8n.Import " +
			"returned no error; activatable = workflow.Compile returned no error; runnable = the " +
			"workflow ran to completion with HTTP, SQL and model calls stubbed. Regenerate with " +
			"go test ./internal/interop/n8n/corpus -update-baseline, and say in the commit which " +
			"workflows moved and why.",
		Tiers:     tiers,
		Total:     len(scores),
		Blocked:   blocked,
		NodeTypes: nodeTypeInventory(public),
		Scores:    scores,
	}

	if *updateBaseline {
		writeBaseline(t, measured)
		return
	}
	compareBaseline(t, measured)
}

func baselinePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(".", name)
}

func writeBaseline(t *testing.T, measured baseline) {
	t.Helper()
	encoded, err := json.MarshalIndent(measured, "", "  ")
	if err != nil {
		t.Fatalf("encoding baseline: %v", err)
	}
	if err := os.WriteFile(baselinePath(t, "baseline.json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("writing baseline.json: %v", err)
	}
	if err := os.WriteFile(baselinePath(t, "BASELINE.md"), []byte(renderMarkdown(measured)), 0o644); err != nil {
		t.Fatalf("writing BASELINE.md: %v", err)
	}
	t.Logf("wrote baseline: %d imported, %d activatable, %d runnable of %d",
		measured.Tiers[tierImported], measured.Tiers[tierActivatable], measured.Tiers[tierRunnable], measured.Total)
}

func compareBaseline(t *testing.T, measured baseline) {
	t.Helper()
	raw, err := os.ReadFile(baselinePath(t, "baseline.json"))
	if err != nil {
		t.Fatalf("reading baseline.json (regenerate with -update-baseline): %v", err)
	}
	var committed baseline
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("parsing baseline.json: %v", err)
	}

	previous := map[string]score{}
	for _, entry := range committed.Scores {
		previous[entry.Name] = entry
	}
	current := map[string]score{}
	for _, entry := range measured.Scores {
		current[entry.Name] = entry
	}

	for name, entry := range current {
		was, ok := previous[name]
		if !ok {
			t.Errorf("%s is not in the baseline; regenerate with -update-baseline and say why it appeared", name)
			continue
		}
		if was.Imported != entry.Imported || was.Activatable != entry.Activatable || was.Runnable != entry.Runnable || was.Blocked != entry.Blocked {
			t.Errorf("%s moved: imported %t→%t, activatable %t→%t, runnable %t→%t, blocked %t→%t — regenerate with -update-baseline and say why",
				name, was.Imported, entry.Imported, was.Activatable, entry.Activatable, was.Runnable, entry.Runnable, was.Blocked, entry.Blocked)
		}
	}
	for name := range previous {
		if _, ok := current[name]; !ok {
			t.Errorf("%s vanished from the corpus; the sync or the pin changed", name)
		}
	}
}

func renderMarkdown(measured baseline) string {
	var out strings.Builder
	out.WriteString("# n8n importer corpus — baseline\n\n")
	out.WriteString("<!-- Generated by go test ./internal/interop/n8n/corpus -update-baseline. Do not edit by hand. -->\n\n")
	out.WriteString("The corpus measures how much of the real n8n ecosystem KilasFlow can take.\n")
	out.WriteString("Fixtures are third-party and are never committed — only these numbers are.\n")
	out.WriteString("Materialise them with `" + corpus.SyncCommand + "`.\n\n")

	out.WriteString("## Tiers\n\n")
	out.WriteString("| Tier | Meaning |\n| --- | --- |\n")
	out.WriteString("| imported | `n8n.Import` returned no error. |\n")
	out.WriteString("| activatable | `workflow.Compile` returned no error — the authority activation goes through. |\n")
	out.WriteString("| runnable | The workflow ran to completion with every outbound call refused by policy. |\n\n")
	out.WriteString("A fixture that is **blocked** compiled and began running, and was stopped by the\n")
	out.WriteString("offline policy refusing an outbound HTTP or database call. That is a limit of the\n")
	out.WriteString("instrument, not a defect in the workflow: only a fixture that is neither runnable\n")
	out.WriteString("nor blocked actually failed to execute.\n\n")

	out.WriteString("## Score\n\n")
	fmt.Fprintf(&out, "| Tier | Count | of %d |\n| --- | ---: | ---: |\n", measured.Total)
	for _, tier := range []string{tierImported, tierActivatable, tierRunnable} {
		count := measured.Tiers[tier]
		percent := 0.0
		if measured.Total > 0 {
			percent = float64(count) / float64(measured.Total) * 100
		}
		fmt.Fprintf(&out, "| %s | %d | %.0f%% |\n", tier, count, percent)
	}
	fmt.Fprintf(&out, "| _(blocked by the offline policy)_ | %d | %.0f%% |\n",
		measured.Blocked, percentOf(measured.Blocked, measured.Total))

	out.WriteString("\n## Per fixture\n\n")
	out.WriteString("| Fixture | Source | Nodes | Imported | Activatable | Runnable | First failure |\n")
	out.WriteString("| --- | --- | ---: | :---: | :---: | :---: | --- |\n")
	for _, entry := range measured.Scores {
		reason := entry.Reason
		if reason == "" {
			reason = "—"
		}
		runnable := tick(entry.Runnable)
		if entry.Blocked {
			runnable = "blocked"
		}
		fmt.Fprintf(&out, "| `%s` | %s | %d | %s | %s | %s | %s |\n",
			entry.Name, entry.Source, entry.Nodes,
			tick(entry.Imported), tick(entry.Activatable), runnable,
			strings.ReplaceAll(reason, "|", "\\|"))
	}

	out.WriteString("\n## Node type inventory\n\n")
	out.WriteString("What p3 and p4 pick their targets from, measured across the public corpus.\n\n")
	out.WriteString("| n8n node type | Instances |\n| --- | ---: |\n")
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(measured.NodeTypes))
	for name, count := range measured.NodeTypes {
		pairs = append(pairs, pair{name, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	for _, entry := range pairs {
		fmt.Fprintf(&out, "| `%s` | %d |\n", entry.name, entry.count)
	}
	return out.String()
}

func percentOf(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total) * 100
}

func tick(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// TestControlFixturesPass proves the instrument works. Every authored fixture
// must reach every tier; if they all failed alongside the upstream ones, the
// baseline would be measuring KilasFlow's own breakage rather than n8n's
// difficulty, and nothing in the numbers would say so.
func TestControlFixturesPass(t *testing.T) {
	fixtures, err := corpus.Load()
	if err != nil {
		t.Fatalf("corpus.Load() error = %v", err)
	}
	catalog, executors := corpusRuntime(t)

	seen := 0
	for _, fixture := range fixtures {
		if fixture.Source != corpus.SourceKilasFlow {
			continue
		}
		seen++
		result := scoreFixture(t, fixture, catalog, executors)
		if !result.Imported || !result.Activatable || !result.Runnable {
			t.Errorf("control fixture %s did not reach every tier (imported=%t activatable=%t runnable=%t): %s",
				fixture.Name, result.Imported, result.Activatable, result.Runnable, result.Reason)
		}
	}
	if seen == 0 {
		t.Fatal("no authored control fixtures are embedded; the scoreboard has no positive control")
	}
}

// TestCorpusLoadsWithoutTheThirdPartyFixtures proves a clean clone works. The
// authored fixtures are embedded, so Load must succeed and return them even
// when nothing has been synced.
func TestCorpusLoadsWithoutTheThirdPartyFixtures(t *testing.T) {
	t.Setenv(corpus.DirEnv, filepath.Join(t.TempDir(), "absent"))
	t.Setenv(corpus.PrivateDirEnv, filepath.Join(t.TempDir(), "absent-private"))

	if corpus.Available() {
		t.Error("Available() = true with no corpus directory")
	}
	fixtures, err := corpus.Load()
	if err != nil {
		t.Fatalf("corpus.Load() error = %v on a clean clone", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("Load() returned nothing; the embedded control fixtures should always be present")
	}
	for _, fixture := range fixtures {
		if fixture.Source != corpus.SourceKilasFlow {
			t.Errorf("fixture %s came from %s with no corpus materialised", fixture.Name, fixture.Source)
		}
	}
}

// TestManifestPinsEveryFetchedFixture proves the pin file and the sync agree,
// so a fixture cannot be added to the corpus without being pinned.
func TestManifestPinsEveryFetchedFixture(t *testing.T) {
	manifest, err := corpus.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if len(manifest.Fixtures) == 0 {
		t.Fatal("the manifest pins nothing")
	}
	for name, source := range manifest.Sources {
		if len(source.Commit) != 40 {
			t.Errorf("source %s is pinned to %q, which is not a full commit", name, source.Commit)
		}
		if strings.TrimSpace(source.Licence) == "" {
			t.Errorf("source %s does not record why its fixtures may not be committed", name)
		}
	}
	if !corpus.Available() {
		t.Skipf("no corpus materialised; fetch it with: %s", corpus.SyncCommand)
	}
	pinned := map[string]bool{}
	for _, fixture := range manifest.Fixtures {
		pinned[strings.TrimSuffix(fixture.Path, ".json")] = true
	}
	fixtures, err := corpus.Load()
	if err != nil {
		t.Fatalf("corpus.Load() error = %v", err)
	}
	for _, fixture := range fixtures {
		if fixture.Source == corpus.SourceKilasFlow || fixture.Source == corpus.SourcePrivate {
			continue
		}
		if !pinned[fixture.Name] {
			t.Errorf("%s is in the corpus but not pinned in MANIFEST.json", fixture.Name)
		}
	}
}

// TestPrivateOverlayIsScoredButNeverReported proves the overlay works and that
// nothing about it reaches a committed file or the test log.
//
// The overlay holds the owner's own exported client workflows: live phone
// numbers, WAHA hostnames, credential names. They are the most valuable input
// to the corpus and the most dangerous to print, so this asserts both halves —
// that a dropped-in workflow is picked up with no code change, and that its
// name never appears in the rendered report.
func TestPrivateOverlayIsScoredButNeverReported(t *testing.T) {
	private := t.TempDir()
	const secretName = "acme-client-live-whatsapp-bot"
	payload := []byte(`{
	  "name": "` + secretName + `",
	  "nodes": [{"parameters":{},"id":"n1","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0]}],
	  "connections": {}
	}`)
	if err := os.WriteFile(filepath.Join(private, secretName+".json"), payload, 0o600); err != nil {
		t.Fatalf("writing private fixture: %v", err)
	}
	t.Setenv(corpus.PrivateDirEnv, private)

	fixtures, err := corpus.Load()
	if err != nil {
		t.Fatalf("corpus.Load() error = %v", err)
	}
	found := false
	for _, fixture := range fixtures {
		if fixture.Source == corpus.SourcePrivate {
			found = true
		}
	}
	if !found {
		t.Fatal("the private overlay was not picked up; it must score alongside the public corpus with no code change")
	}

	// Render a report from every fixture, private included, and prove the
	// private one contributed nothing to it.
	catalog, executors := corpusRuntime(t)
	var scores []score
	var public []corpus.Fixture
	for _, fixture := range fixtures {
		if fixture.Source == corpus.SourcePrivate {
			continue
		}
		public = append(public, fixture)
		scores = append(scores, scoreFixture(t, fixture, catalog, executors))
	}
	rendered := renderMarkdown(baseline{Tiers: map[string]int{}, Total: len(scores), NodeTypes: nodeTypeInventory(public), Scores: scores})
	if strings.Contains(rendered, secretName) {
		t.Error("the rendered report names a private fixture")
	}
}
