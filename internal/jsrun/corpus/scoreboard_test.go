package corpus

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// The scoreboard says how much of the JavaScript in real n8n templates the
// runtime takes, and makes a later change's diff show exactly which bodies it
// moved. The three measures are defined here so nobody re-argues them:
//
//	parsed    jsrun.Analyze did not report a syntax error.
//	accepted  jsrun.Analyze returned no error at all: nothing is refused.
//	ran       Runner.Run, with the shipped DefaultLimits, returned no error on
//	          the synthesised input.
//
// "Ran" is measured on stand-in data whenever a template pinned none, so a
// failure there may be the stand-in's fault rather than the runtime's. That is
// why every failure is reported by what went wrong, and why the gate on the
// numbers is on parse and analysis only.

var updateBaseline = flag.Bool("update-baseline", false, "rewrite BASELINE.md and baseline.json from this run")

// acceptFloor is the share of the bodies that must parse and pass analysis.
// The spike found none of the refused constructs in the 500 most-viewed
// templates, so anything below it is a regression, or a corpus that changed
// under the pins.
const acceptFloor = 0.97

// measured is one body's result. Only KilasFlow's own words reach it: an
// outcome, a reason class and the analyser's refusal subjects, never the
// author's code, names or data, module names excepted: a refusal of an
// unshipped module names the module.
type measured struct {
	Key      string   `json:"key"`
	Mode     string   `json:"mode"`
	Stubbed  bool     `json:"stubbed,omitempty"`
	Parsed   bool     `json:"parsed"`
	Accepted bool     `json:"accepted"`
	Ran      bool     `json:"ran"`
	Outcome  string   `json:"outcome"`
	Reason   string   `json:"reason,omitempty"`
	Refused  []string `json:"refused,omitempty"`

	elapsed time.Duration
	detail  string // the error as the user would read it; logged, never committed
}

// Outcomes.
const (
	outcomeOK       = "ok"
	outcomeNoSource = "no-source"
	outcomeSyntax   = "syntax"
	outcomeRefused  = "refused"
)

// measure analyses one body and, when it is accepted, runs it.
func measure(t *testing.T, runner *jsrun.Runner, current body) measured {
	t.Helper()
	result := measured{Key: current.Key, Mode: string(current.Task.Mode), Stubbed: current.Stubbed}
	if !current.HasSource {
		result.Outcome = outcomeNoSource
		return result
	}
	_, err := jsrun.Analyze(current.Task.Source, current.Task.Mode)
	var syntax *jsrun.SyntaxError
	var refused *jsrun.UnsupportedError
	switch {
	case errors.As(err, &syntax):
		result.Outcome, result.detail = outcomeSyntax, err.Error()
		return result
	case errors.As(err, &refused):
		result.Parsed, result.Outcome, result.detail = true, outcomeRefused, err.Error()
		seen := map[string]bool{}
		for _, found := range refused.Found {
			if !seen[found.Subject] {
				seen[found.Subject] = true
				result.Refused = append(result.Refused, found.Subject)
			}
		}
		sort.Strings(result.Refused)
		return result
	case err != nil:
		t.Fatalf("%s: Analyze returned an error of no known kind: %v", current.Key, err)
	}
	result.Parsed, result.Accepted = true, true

	// A generous outer deadline only guards the test; the limit that counts is
	// the runner's own.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	started := time.Now()
	_, err = runner.Run(ctx, current.Task)
	result.elapsed = time.Since(started)
	if err == nil {
		result.Ran, result.Outcome = true, outcomeOK
		return result
	}
	result.Outcome, result.Reason = classify(err)
	result.detail = err.Error()
	return result
}

var (
	dateLocale = regexp.MustCompile(`date formatting in locale (\S+) is not supported`)
	// helperMissing is a helper the instrument does not wire: it makes no
	// network call and keeps no files.
	helperMissing = regexp.MustCompile(`this\.helpers\.(\w+) is not available here`)
	refusalOf     = regexp.MustCompile(`this node's code (.+?), which this server does not run`)
)

// classify names a run's failure by what went wrong, in the runtime's terms.
func classify(err error) (outcome, reason string) {
	var script *jsrun.ScriptError
	if errors.As(err, &script) {
		switch {
		case dateLocale.MatchString(script.Message):
			return "threw", "date locale " + dateLocale.FindStringSubmatch(script.Message)[1]
		case helperMissing.MatchString(script.Message):
			return "threw", "this.helpers." + helperMissing.FindStringSubmatch(script.Message)[1] + " (not wired in the instrument)"
		case refusalOf.MatchString(script.Message):
			return "threw", "refused at run: " + refusalOf.FindStringSubmatch(script.Message)[1]
		case script.Name != "":
			return "threw", script.Name
		}
		return "threw", "a thrown value"
	}
	for _, named := range []struct {
		sentinel error
		outcome  string
	}{
		{jsrun.ErrInvalidReturn, "invalid-return"},
		{jsrun.ErrTimeLimit, "time-limit"},
		{jsrun.ErrMemoryLimit, "memory-limit"},
		{jsrun.ErrOutputLimit, "output-limit"},
		{jsrun.ErrInputLimit, "input-limit"},
		{jsrun.ErrHostCallLimit, "host-call-limit"},
		{jsrun.ErrCallDepth, "call-depth"},
		{jsrun.ErrNeverSettles, "never-settles"},
		{jsrun.ErrUnsupported, "refused-at-run"},
		{jsrun.ErrEngineFault, "engine-fault"},
	} {
		if errors.Is(err, named.sentinel) {
			return named.outcome, ""
		}
	}
	// A file the code returned inline is stored after the run, through the
	// same helper, so an instrument without one fails there rather than in
	// the code.
	if missing := helperMissing.FindStringSubmatch(err.Error()); missing != nil {
		return "error", "this.helpers." + missing[1] + " (not wired in the instrument)"
	}
	return "error", ""
}

// blocker is the one thing that stopped a body, or "" when it ran or more
// than one thing stopped it. It is how "what would this change unblock" is
// counted.
func (result measured) blocker() string {
	switch {
	case result.Ran || result.Outcome == outcomeNoSource:
		return ""
	case result.Outcome == outcomeSyntax:
		return "syntax error"
	case result.Outcome == outcomeRefused:
		if len(result.Refused) == 1 {
			return "refused: " + result.Refused[0]
		}
		return ""
	case result.Reason != "":
		return result.Outcome + ": " + result.Reason
	}
	return result.Outcome
}

// scoreboard is the committed baseline.
type scoreboard struct {
	Comment  string         `json:"comment"`
	Corpus   corpusInfo     `json:"corpus"`
	Totals   totals         `json:"totals"`
	Timing   timing         `json:"timing"`
	Blockers map[string]int `json:"blockers"`
	Bodies   []measured     `json:"bodies"`
}

type corpusInfo struct {
	RankedAt        string `json:"rankedAt"`
	Ranked          int    `json:"ranked"`
	Templates       int    `json:"templates"`
	PythonCodeNodes int    `json:"pythonCodeNodes"`
}

type totals struct {
	Bodies      int `json:"bodies"`
	NoSource    int `json:"noSource"`
	Measured    int `json:"measured"`
	EachItem    int `json:"eachItem"`
	Comparators int `json:"comparators"`
	Stubbed     int `json:"stubbed"`
	Parsed      int `json:"parsed"`
	Accepted    int `json:"accepted"`
	Ran         int `json:"ran"`
	// Rates are percentages: parsed and accepted of the measured bodies, and
	// runtime errors of the accepted ones.
	ParseRate        float64 `json:"parseRate"`
	AcceptRate       float64 `json:"acceptRate"`
	RuntimeErrorRate float64 `json:"runtimeErrorRate"`
}

// timing is the wall-clock time of Runner.Run for the accepted bodies,
// including compiling each body once. It depends on the machine, so it is
// recorded and never compared.
type timing struct {
	Runs         int     `json:"runs"`
	MedianMillis float64 `json:"medianMillis"`
	P95Millis    float64 `json:"p95Millis"`
}

func percent(count, of int) float64 {
	if of == 0 {
		return 0
	}
	return float64(int(float64(count)*1000/float64(of)+0.5)) / 10
}

func millis(duration time.Duration) float64 {
	return float64(duration.Microseconds()/10) / 100
}

func quantile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(q*float64(len(sorted)-1) + 0.5)
	return sorted[index]
}

// score runs the instrument over the bodies.
func score(t *testing.T, all []body) scoreboard {
	t.Helper()
	runner := jsrun.NewRunner(jsrun.Options{Limits: jsrun.DefaultLimits()})
	board := scoreboard{Blockers: map[string]int{}}
	var times []time.Duration
	for _, current := range all {
		result := measure(t, runner, current)
		board.Bodies = append(board.Bodies, result)
		board.Totals.Bodies++
		if result.Outcome == outcomeNoSource {
			board.Totals.NoSource++
			continue
		}
		board.Totals.Measured++
		switch jsrun.Mode(result.Mode) {
		case jsrun.ModeEachItem:
			board.Totals.EachItem++
		case jsrun.ModeComparator:
			board.Totals.Comparators++
		}
		if result.Stubbed {
			board.Totals.Stubbed++
		}
		if result.Parsed {
			board.Totals.Parsed++
		}
		if result.Accepted {
			board.Totals.Accepted++
			times = append(times, result.elapsed)
		}
		if result.Ran {
			board.Totals.Ran++
		}
		if blocker := result.blocker(); blocker != "" {
			board.Blockers[blocker]++
		}
		if !result.Ran {
			t.Logf("%s: %s %s — %s", result.Key, result.Outcome, result.Reason, firstLine(result.detail))
		}
	}
	board.Totals.ParseRate = percent(board.Totals.Parsed, board.Totals.Measured)
	board.Totals.AcceptRate = percent(board.Totals.Accepted, board.Totals.Measured)
	board.Totals.RuntimeErrorRate = percent(board.Totals.Accepted-board.Totals.Ran, board.Totals.Accepted)
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	board.Timing = timing{Runs: len(times), MedianMillis: millis(quantile(times, 0.5)), P95Millis: millis(quantile(times, 0.95))}
	return board
}

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// manifest is the committed pin file.
type manifest struct {
	Source struct {
		RankedAt        string `json:"rankedAt"`
		Ranked          int    `json:"ranked"`
		PythonCodeNodes int    `json:"pythonCodeNodes"`
	} `json:"source"`
	Templates []struct {
		Template  int    `json:"template"`
		SHA256    string `json:"sha256"`
		CodeNodes int    `json:"codeNodes"`
	} `json:"templates"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("MANIFEST.json")
	if err != nil {
		t.Fatalf("reading MANIFEST.json: %v", err)
	}
	var pins manifest
	if err := json.Unmarshal(raw, &pins); err != nil {
		t.Fatalf("parsing MANIFEST.json: %v", err)
	}
	return pins
}

func TestCodeCorpusScoreboard(t *testing.T) {
	fixtures := loadFixtures(t, fixtureDir())
	if len(fixtures) == 0 {
		t.Skipf("no code corpus materialised; fetch it with: %s", syncCommand)
	}
	pins := loadManifest(t)
	board := score(t, bodies(fixtures, ""))
	board.Comment = "Baseline for the Code-node compatibility corpus. Bodies are named <template id>/<node position>; " +
		"parsed, accepted and ran are defined in scoreboard_test.go. Regenerate with make js-corpus-baseline, and say " +
		"in the commit which bodies moved and why."
	board.Corpus = corpusInfo{
		RankedAt: pins.Source.RankedAt, Ranked: pins.Source.Ranked,
		Templates: len(fixtures), PythonCodeNodes: pins.Source.PythonCodeNodes,
	}

	if board.Totals.AcceptRate < acceptFloor*100 {
		t.Errorf("%.1f%% of the %d bodies parse and pass analysis, below the %.0f%% floor",
			board.Totals.AcceptRate, board.Totals.Measured, acceptFloor*100)
	}
	t.Logf("%d bodies (%d without source): parsed %.1f%%, accepted %.1f%%, runtime errors %.1f%% of accepted; median %.2f ms, p95 %.2f ms",
		board.Totals.Bodies, board.Totals.NoSource, board.Totals.ParseRate, board.Totals.AcceptRate,
		board.Totals.RuntimeErrorRate, board.Timing.MedianMillis, board.Timing.P95Millis)

	checked := loadVerification(t, fixtureDir(), fixtures)
	if *updateBaseline {
		if len(checked.reasons) > 0 {
			t.Fatalf("%d pinned templates drifted upstream; re-pin with scripts/code-corpus-sync.sh --update before rewriting the baseline", len(checked.reasons))
		}
		writeScoreboard(t, board)
		return
	}
	compareScoreboard(t, board, checked)
}

func writeScoreboard(t *testing.T, board scoreboard) {
	t.Helper()
	encoded, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		t.Fatalf("encoding the baseline: %v", err)
	}
	if err := os.WriteFile("baseline.json", append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("writing baseline.json: %v", err)
	}
	if err := os.WriteFile("BASELINE.md", []byte(renderScoreboard(board)), 0o644); err != nil {
		t.Fatalf("writing BASELINE.md: %v", err)
	}
	t.Logf("wrote the baseline: %d of %d bodies accepted, %d ran", board.Totals.Accepted, board.Totals.Measured, board.Totals.Ran)
}

// compareScoreboard fails on any body that moved. A body that improved fails
// too: the baseline is the record of what the runtime takes, and a change
// that moves it says so by regenerating it. Only bodies of templates the sync
// verified against their pins are held to it; the rest are reported.
func compareScoreboard(t *testing.T, board scoreboard, pins verification) {
	t.Helper()
	raw, err := os.ReadFile("baseline.json")
	if err != nil {
		t.Fatalf("reading baseline.json (regenerate with make js-corpus-baseline): %v", err)
	}
	var committed scoreboard
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("parsing baseline.json: %v", err)
	}
	found := compareBodies(committed.Bodies, board.Bodies, pins)
	for _, note := range found.notes {
		t.Log(note)
	}
	for _, failure := range found.failures {
		t.Error(failure)
	}
}

// comparison is what holding a run to the baseline found: failures, and
// notes about bodies it could not hold to it.
type comparison struct {
	failures []string
	notes    []string
}

// compareBodies holds the measured bodies to the committed ones. A body of a
// template the sync could not verify (it changed, lost its code or was
// withdrawn upstream) is neither compared nor missed: a third party editing
// a template says nothing about the runtime. Everything else must be where
// the baseline left it.
func compareBodies(committed, current []measured, pins verification) comparison {
	var found comparison
	previous := map[string]measured{}
	for _, entry := range committed {
		previous[entry.Key] = entry
	}
	for _, entry := range current {
		if !pins.verified(entry.Key) {
			found.notes = append(found.notes, fmt.Sprintf("%s is not compared: %s", entry.Key, pins.reason(entry.Key)))
			delete(previous, entry.Key)
			continue
		}
		was, ok := previous[entry.Key]
		if !ok {
			found.failures = append(found.failures, fmt.Sprintf("%s is not in the baseline; the pins changed, so regenerate it and say why", entry.Key))
			continue
		}
		delete(previous, entry.Key)
		if was.state() != entry.state() {
			verdict := "moved"
			if was.rank() > entry.rank() {
				verdict = "REGRESSED"
			}
			found.failures = append(found.failures, fmt.Sprintf("%s %s: %s → %s (%s); regenerate the baseline with make js-corpus-baseline and say why",
				entry.Key, verdict, was.state(), entry.state(), firstLine(entry.detail)))
		}
	}
	keys := make([]string, 0, len(previous))
	for key := range previous {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if pins.verified(key) {
			found.failures = append(found.failures, fmt.Sprintf("%s vanished from the corpus; the sync or the pins changed", key))
		} else {
			found.notes = append(found.notes, fmt.Sprintf("%s is not compared: %s", key, pins.reason(key)))
		}
	}
	return found
}

// verification is what the sync verified: verified.json beside the
// fixtures. With no such file, every template whose fixture is present
// counts as verified, which is what a strict sync that succeeded means.
type verification struct {
	templates map[string]bool
	reasons   map[string]string
}

func (pins verification) verified(key string) bool { return pins.templates[templateOf(key)] }

func (pins verification) reason(key string) string {
	if reason, ok := pins.reasons[templateOf(key)]; ok {
		return "template " + templateOf(key) + " " + reason + " since it was pinned; re-pin with scripts/code-corpus-sync.sh --update"
	}
	return "template " + templateOf(key) + " was not verified by the last sync"
}

// templateOf is the template id a body's key names.
func templateOf(key string) string {
	template, _, _ := strings.Cut(key, "/")
	return template
}

// verifiedFile is where the sync records the verified templates.
const verifiedFile = "verified.json"

func loadVerification(t *testing.T, dir string, fixtures []fixture) verification {
	t.Helper()
	pins := verification{templates: map[string]bool{}, reasons: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(dir, verifiedFile))
	if os.IsNotExist(err) {
		for _, current := range fixtures {
			pins.templates[strconv.Itoa(current.Template)] = true
		}
		return pins
	}
	if err != nil {
		t.Fatalf("reading %s: %v", verifiedFile, err)
	}
	var recorded struct {
		Verified   []int `json:"verified"`
		Unverified []struct {
			Template int    `json:"template"`
			Reason   string `json:"reason"`
		} `json:"unverified"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("parsing %s: %v", verifiedFile, err)
	}
	for _, template := range recorded.Verified {
		pins.templates[strconv.Itoa(template)] = true
	}
	for _, entry := range recorded.Unverified {
		pins.reasons[strconv.Itoa(entry.Template)] = entry.Reason
	}
	return pins
}

// state is what the comparison compares.
func (result measured) state() string {
	return fmt.Sprintf("%s/%s [%s]", result.Outcome, result.Reason, strings.Join(result.Refused, "; "))
}

// rank orders outcomes from worst to best, so a move can be called a
// regression.
func (result measured) rank() int {
	switch {
	case result.Ran:
		return 3
	case result.Accepted:
		return 2
	case result.Parsed:
		return 1
	}
	return 0
}

func renderScoreboard(board scoreboard) string {
	var out strings.Builder
	total := board.Totals
	out.WriteString("# Code-node compatibility corpus — baseline\n\n")
	out.WriteString("<!-- Generated by make js-corpus-baseline. Do not edit by hand. -->\n\n")
	fmt.Fprintf(&out, "The JavaScript Code nodes and Sort code comparators of the %d most-viewed n8n templates "+
		"(ranked %s), measured against the embedded runtime. %d of those templates carry code. The bodies are "+
		"fetched by `scripts/code-corpus-sync.sh`, pinned in `MANIFEST.json` and never committed; a body is named "+
		"here by template id and node position only, and the only words taken from a template are the names of "+
		"modules the analyser refuses. %d Python Code nodes in the same templates are not measured: "+
		"Python stays refused.\n\n", board.Corpus.Ranked, board.Corpus.RankedAt, board.Corpus.Templates, board.Corpus.PythonCodeNodes)

	out.WriteString("| Measure | Bodies | Rate |\n|---|---:|---:|\n")
	fmt.Fprintf(&out, "| JavaScript bodies | %d | |\n", total.Bodies)
	fmt.Fprintf(&out, "| … with no code in the template (an empty body, which fails in n8n too; not measured) | %d | |\n", total.NoSource)
	fmt.Fprintf(&out, "| Measured | %d | |\n", total.Measured)
	fmt.Fprintf(&out, "| Parse | %d | %.1f%% |\n", total.Parsed, total.ParseRate)
	fmt.Fprintf(&out, "| Parse and pass analysis | %d | %.1f%% |\n", total.Accepted, total.AcceptRate)
	fmt.Fprintf(&out, "| Run without an error on synthesised input | %d | %.1f%% of accepted |\n", total.Ran, percent(total.Ran, total.Accepted))
	fmt.Fprintf(&out, "| Runtime-error rate | %d | %.1f%% of accepted |\n\n", total.Accepted-total.Ran, total.RuntimeErrorRate)

	fmt.Fprintf(&out, "Of the measured bodies, %d run once for each item and %d are Sort comparators. %d had no pinned "+
		"data upstream and ran on a stand-in item shaped from the fields the body reads, so a failure among them "+
		"may be the stand-in's, not the runtime's.\n\n", total.EachItem, total.Comparators, total.Stubbed)
	fmt.Fprintf(&out, "Run time (wall clock of `Runner.Run`, including compiling the body once, on the machine that "+
		"wrote this file): median **%.2f ms**, p95 **%.2f ms** over %d runs.\n\n", board.Timing.MedianMillis, board.Timing.P95Millis, board.Timing.Runs)

	out.WriteString("## What stops the rest\n\n")
	out.WriteString("Bodies stopped by exactly one thing, by that thing. A body refused for several reasons is counted in none.\n\n")
	out.WriteString("| Blocker | Bodies |\n|---|---:|\n")
	names := make([]string, 0, len(board.Blockers))
	for name := range board.Blockers {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if board.Blockers[names[i]] != board.Blockers[names[j]] {
			return board.Blockers[names[i]] > board.Blockers[names[j]]
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		fmt.Fprintf(&out, "| %s | %d |\n", strings.ReplaceAll(name, "|", "\\|"), board.Blockers[name])
	}

	out.WriteString("\n## Bodies\n\n| Body | Mode | Input | Parsed | Accepted | Ran | Outcome |\n|---|---|---|:-:|:-:|:-:|---|\n")
	for _, entry := range board.Bodies {
		input := "pinned"
		if entry.Stubbed {
			input = "stand-in"
		}
		outcome := entry.Outcome
		if entry.Reason != "" {
			outcome += ": " + entry.Reason
		}
		if len(entry.Refused) > 0 {
			outcome += ": " + strings.Join(entry.Refused, "; ")
		}
		fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %s | %s |\n", entry.Key, entry.Mode, input,
			tick(entry.Parsed), tick(entry.Accepted), tick(entry.Ran), strings.ReplaceAll(outcome, "|", "\\|"))
	}
	return out.String()
}

func tick(value bool) string {
	if value {
		return "✓"
	}
	return "·"
}

// TestManifestPinsEveryFixture proves the pin file and the fixtures agree, so
// a body cannot enter the scoreboard without being pinned.
func TestManifestPinsEveryFixture(t *testing.T) {
	pins := loadManifest(t)
	if len(pins.Templates) == 0 {
		t.Fatal("the manifest pins nothing")
	}
	pinned := map[int]int{}
	for _, entry := range pins.Templates {
		if len(entry.SHA256) != 64 {
			t.Errorf("template %d is pinned to %q, which is not a SHA-256 digest", entry.Template, entry.SHA256)
		}
		pinned[entry.Template] = entry.CodeNodes
	}
	fixtures := loadFixtures(t, fixtureDir())
	if len(fixtures) == 0 {
		t.Skipf("no code corpus materialised; fetch it with: %s", syncCommand)
	}
	for _, current := range fixtures {
		nodes, ok := pinned[current.Template]
		if !ok {
			t.Errorf("template %d is in the corpus but not pinned in MANIFEST.json", current.Template)
		} else if nodes != len(current.Nodes) {
			t.Errorf("template %d has %d code nodes, but the manifest pins %d", current.Template, len(current.Nodes), nodes)
		}
	}
}

// TestTheScoreboardRecordsNoUserContent renders a board over a body whose
// every word is the author's and proves none of them reaches the committed
// files. Module names are the documented exception and are not in this body.
func TestTheScoreboardRecordsNoUserContent(t *testing.T) {
	const secret = "acmeSecretFieldName"
	current := fixture{
		Template:  42,
		Nodes:     []codeNode{{Index: 3, Name: "Node " + secret, Type: "n8n-nodes-base.code", Parameters: map[string]any{"jsCode": "const none = null\nreturn [{ json: { v: none." + secret + " } }]"}}},
		NodeNames: []string{"Node " + secret},
	}
	board := score(t, bodies([]fixture{current}, ""))
	encoded, err := json.Marshal(board)
	if err != nil {
		t.Fatal(err)
	}
	if board.Bodies[0].Ran {
		t.Fatal("the body was meant to fail, so its error text is in play")
	}
	for name, text := range map[string]string{"baseline.json": string(encoded), "BASELINE.md": renderScoreboard(board)} {
		if strings.Contains(text, secret) {
			t.Errorf("%s carries the author's words:\n%s", name, text)
		}
	}
	if board.Bodies[0].Key != "42/3" || board.Bodies[0].Reason != "TypeError" {
		t.Errorf("body = %+v, want 42/3 failing with a TypeError", board.Bodies[0])
	}
}

func TestTheBaselineHoldsOnlyVerifiedTemplates(t *testing.T) {
	body := func(key, outcome string) measured {
		return measured{Key: key, Parsed: true, Accepted: true, Ran: outcome == outcomeOK, Outcome: outcome}
	}
	committed := []measured{body("1/0", outcomeOK), body("1/1", outcomeOK), body("2/0", outcomeOK), body("3/0", outcomeOK), body("4/0", outcomeOK)}
	pins := verification{
		templates: map[string]bool{"1": true, "3": true, "5": true},
		reasons:   map[string]string{"2": "changed upstream", "4": "withdrawn upstream"},
	}
	current := []measured{
		body("1/0", outcomeOK), // verified, unchanged
		body("1/1", "threw"),   // verified, regressed
		body("2/0", "threw"),   // unverified: its template changed, so its move is not the runtime's
		body("5/0", outcomeOK), // verified, but new
		// 3/0 is verified and absent: it vanished.
		// 4/0 is unverified and absent: its template was withdrawn.
	}
	found := compareBodies(committed, current, pins)
	wantFailures := []string{"1/1 REGRESSED", "5/0 is not in the baseline", "3/0 vanished"}
	wantNotes := []string{"2/0 is not compared: template 2 changed upstream", "4/0 is not compared: template 4 withdrawn upstream"}
	if len(found.failures) != len(wantFailures) {
		t.Fatalf("failures = %q, want %d", found.failures, len(wantFailures))
	}
	for index, want := range wantFailures {
		if !strings.HasPrefix(found.failures[index], want) {
			t.Errorf("failure %d = %q, want it to start %q", index, found.failures[index], want)
		}
	}
	if len(found.notes) != len(wantNotes) {
		t.Fatalf("notes = %q, want %d", found.notes, len(wantNotes))
	}
	for index, want := range wantNotes {
		if !strings.HasPrefix(found.notes[index], want) {
			t.Errorf("note %d = %q, want it to start %q", index, found.notes[index], want)
		}
	}
}

func TestWithoutAVerifiedFileEveryPresentFixtureIsVerified(t *testing.T) {
	pins := loadVerification(t, t.TempDir(), []fixture{{Template: 7}})
	if !pins.verified("7/2") || pins.verified("8/0") {
		t.Errorf("pins = %+v, want only template 7 verified", pins)
	}
	found := compareBodies([]measured{{Key: "8/0", Outcome: outcomeOK}}, nil, pins)
	if len(found.failures) != 0 || len(found.notes) != 1 {
		t.Errorf("an absent fixture with no record of why = %+v, want one note", found)
	}
}
