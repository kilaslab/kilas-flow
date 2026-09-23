package sidecar

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// The tests in this file run the embedded runner in a real Node process
// against the hand-written packages in testdata/packages. They need Node on
// PATH and skip with a named reason without it, the same posture as the
// fixture test: a deployment that declines the sidecar gets the diagnostic,
// not a broken build.

// catalogue mirrors what the runner answers a describe frame with. It is
// parsed rather than compared as bytes so the assertions talk about packages,
// nodes and errors.
type catalogue struct {
	NodeVersion string             `json:"nodeVersion"`
	Packages    []cataloguePackage `json:"packages"`
}

type cataloguePackage struct {
	Name        string                `json:"name"`
	Version     string                `json:"version"`
	Nodes       []catalogueNode       `json:"nodes"`
	Credentials []catalogueCredential `json:"credentials"`
	Errors      []catalogueError      `json:"errors"`
}

type catalogueNode struct {
	File        string          `json:"file"`
	Name        string          `json:"name"`
	Version     json.RawMessage `json:"version"`
	Execute     bool            `json:"execute"`
	Unsupported []string        `json:"unsupported"`
	Description map[string]any  `json:"description"`
}

type catalogueCredential struct {
	File             string           `json:"file"`
	Name             string           `json:"name"`
	DisplayName      string           `json:"displayName"`
	DocumentationURL string           `json:"documentationUrl"`
	Properties       []map[string]any `json:"properties"`
}

type catalogueError struct {
	Package  string `json:"package"`
	File     string `json:"file"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// runnerFixture is one extracted runner plus the fixture packages directory.
type runnerFixture struct {
	node        string
	runnerPath  string
	packagesDir string
}

func newRunnerFixture(t *testing.T) runnerFixture {
	t.Helper()
	node := sidecartest.Node(t)
	runnerPath, err := ExtractRunner(t.TempDir())
	if err != nil {
		t.Fatalf("ExtractRunner() error = %v", err)
	}
	return runnerFixture{node: node, runnerPath: runnerPath, packagesDir: sidecartest.PackagesDir(t)}
}

func runnerLimits() Limits {
	limits := DefaultLimits()
	limits.Timeout = 20 * time.Second
	limits.SpawnTimeout = 20 * time.Second
	return limits
}

func (fixture runnerFixture) spawn(packages []string, diagnostics *lockedWriter, adjust func(*RunnerConfig)) SpawnFunc {
	config := RunnerConfig{
		NodePath:    fixture.node,
		RunnerPath:  fixture.runnerPath,
		PackagesDir: fixture.packagesDir,
		Packages:    packages,
	}
	if diagnostics != nil {
		config.Diag = diagnostics
	}
	if adjust != nil {
		adjust(&config)
	}
	return RunnerSpawn(config)
}

// describe runs the runner once in the describe role and returns its
// catalogue. Every catalogue test goes through here, so they all exercise the
// same manifest loading path a boot would.
func (fixture runnerFixture) describe(t *testing.T, packages ...string) catalogue {
	t.Helper()
	raw, err := Discover(context.Background(), fixture.spawn(packages, nil, nil), runnerLimits())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	var parsed catalogue
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshalling catalogue %s: %v", raw, err)
	}
	return parsed
}

// pool is the ordinary shape of a sidecar test: a pool whose child
// diagnostics land in the test's transcript.
func (fixture runnerFixture) pool(diagnostics *lockedWriter, packages []string, adjust func(*RunnerConfig)) *Pool {
	pool := NewPool(fixture.spawn(packages, diagnostics, adjust), runnerLimits())
	pool.SetDiagLog(diagnostics)
	return pool
}

// packageNamed finds one package in a catalogue and fails when it is absent.
func packageNamed(t *testing.T, cat catalogue, name string) cataloguePackage {
	t.Helper()
	for _, entry := range cat.Packages {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("catalogue has no package %q: %+v", name, cat.Packages)
	return cataloguePackage{}
}

func errorCodes(issues []catalogueError) []string {
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

func requireCallError(t *testing.T, err error, code string) *CallError {
	t.Helper()
	callErr, ok := err.(*CallError)
	if !ok {
		t.Fatalf("error = %T (%v), want *CallError", err, err)
	}
	if callErr.ChildCode != code {
		t.Fatalf("child code = %q (%s), want %q", callErr.ChildCode, callErr.ChildMessage, code)
	}
	return callErr
}

// TestExtractRunnerIsContentAddressedAndIdempotent pins the properties the
// extraction has to have: the file is named after its content so two hosts and
// an upgrade cannot collide, it is written 0600, and extracting twice is a
// no-op rather than a rewrite.
func TestExtractRunnerIsContentAddressedAndIdempotent(t *testing.T) {
	source, digest, err := RunnerContent()
	if err != nil {
		t.Fatalf("RunnerContent() error = %v", err)
	}
	if !strings.Contains(string(source), "KilasFlow JavaScript sidecar runner") {
		t.Errorf("the embedded runner does not look like the runner: %q", string(source[:64]))
	}
	if len(source) < 1024 {
		t.Errorf("the embedded runner is %d bytes, which cannot be the runner", len(source))
	}

	runtimeDir := t.TempDir()
	first, err := ExtractRunner(runtimeDir)
	if err != nil {
		t.Fatalf("ExtractRunner() error = %v", err)
	}
	if filepath.Base(first) != "runner-"+digest[:12]+".cjs" {
		t.Errorf("extracted name = %q, want it to carry the content digest", filepath.Base(first))
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat extracted runner: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("extracted runner mode = %o, want 600", mode)
	}
	if err := os.Chtimes(first, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("aging the extracted runner: %v", err)
	}
	aged, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat the aged runner: %v", err)
	}

	second, err := ExtractRunner(runtimeDir)
	if err != nil {
		t.Fatalf("second ExtractRunner() error = %v", err)
	}
	if second != first {
		t.Errorf("second extraction = %q, want the same path %q", second, first)
	}
	again, err := os.Stat(second)
	if err != nil {
		t.Fatalf("stat re-extracted runner: %v", err)
	}
	if !again.ModTime().Equal(aged.ModTime()) {
		t.Errorf("the second extraction rewrote the file: modtime %v became %v", aged.ModTime(), again.ModTime())
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatalf("reading the runtime dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("runtime dir holds %d entries, want only the runner", len(entries))
	}

	if _, err := ExtractRunner(""); err == nil {
		t.Error("ExtractRunner(\"\") succeeded, want a named refusal")
	}
}

// TestRunnerSpawnRefusesAnUnusableConfiguration covers the diagnostics an
// operator sees before any process starts: nothing extracted, no packages
// directory, a directory that does not exist.
func TestRunnerSpawnRefusesAnUnusableConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		config RunnerConfig
		want   string
	}{
		{name: "no runner", config: RunnerConfig{PackagesDir: t.TempDir()}, want: "has not been extracted"},
		{name: "no packages dir", config: RunnerConfig{RunnerPath: "/tmp/runner.cjs"}, want: "no community packages directory"},
		{name: "missing packages dir", config: RunnerConfig{RunnerPath: "/tmp/runner.cjs", PackagesDir: filepath.Join(t.TempDir(), "absent")}, want: "not readable"},
		{name: "no packages", config: RunnerConfig{RunnerPath: "/tmp/runner.cjs", PackagesDir: t.TempDir()}, want: "no community packages are allowlisted"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := RunnerSpawn(testCase.config)(context.Background(), "tenant-a", "")
			callErr, ok := err.(*CallError)
			if !ok {
				t.Fatalf("spawn error = %T (%v), want *CallError", err, err)
			}
			if callErr.Code != CodeSpawnFailed {
				t.Errorf("code = %q, want %q", callErr.Code, CodeSpawnFailed)
			}
			if !strings.Contains(callErr.Detail, testCase.want) {
				t.Errorf("detail = %q, want it to mention %q", callErr.Detail, testCase.want)
			}
		})
	}
}

// TestRunnerSpawnPutsTheSocketInTheRuntimeDir proves the runtime directory
// reaches the spawner: the socket is the one thing a deployment chooses the
// location of, because the operator's runtime directory is the only place the
// child is granted.
func TestRunnerSpawnPutsTheSocketInTheRuntimeDir(t *testing.T) {
	fixture := newRunnerFixture(t)
	// A short path on purpose: the spawner falls back to the platform temp
	// directory when a socket path would approach the unix limit.
	runtimeDir, err := os.MkdirTemp("/tmp", "kf-sidecar-rt-")
	if err != nil {
		t.Fatalf("creating the runtime directory: %v", err)
	}
	defer os.RemoveAll(runtimeDir)

	spawn := fixture.spawn([]string{"kf-fixture-nodes"}, nil, func(config *RunnerConfig) {
		config.RuntimeDir = runtimeDir
	})
	child, err := spawn(context.Background(), "runtime-dir", "")
	if err != nil {
		t.Fatalf("spawn() error = %v", err)
	}
	defer func() { _ = child.Kill() }()

	if filepath.Dir(child.SocketDir) != runtimeDir {
		t.Errorf("socket directory = %q, want it under %q", child.SocketDir, runtimeDir)
	}
}

// TestDescribeLoadsAPackageThroughItsManifest is the manifest contract: the
// packages named on the command line are the only ones loaded, and each one
// contributes the nodes and the credentials its package.json declares.
func TestDescribeLoadsAPackageThroughItsManifest(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-nodes")

	if cat.NodeVersion == "" {
		t.Error("the catalogue carries no node version")
	}
	if len(cat.Packages) != 1 {
		t.Fatalf("loaded %d packages, want only the allowlisted one: %+v", len(cat.Packages), cat.Packages)
	}
	pkg := packageNamed(t, cat, "kf-fixture-nodes")
	if pkg.Version != "1.4.0" {
		t.Errorf("package version = %q, want 1.4.0 from the manifest", pkg.Version)
	}
	if len(pkg.Errors) != 0 {
		t.Errorf("loading the fixture reported %+v, want no errors", pkg.Errors)
	}

	want := map[string]string{
		"dist/nodes/Greet/Greet.node.js": "fixtureGreet",
		"dist/nodes/Relay/Relay.node.js": "fixtureRelay",
	}
	if len(pkg.Nodes) != len(want) {
		t.Fatalf("loaded %d nodes, want %d: %+v", len(pkg.Nodes), len(want), pkg.Nodes)
	}
	for _, node := range pkg.Nodes {
		name, ok := want[node.File]
		if !ok {
			t.Errorf("unexpected node file %q", node.File)
			continue
		}
		if node.Name != name {
			t.Errorf("%s name = %q, want %q", node.File, node.Name, name)
		}
		if !node.Execute {
			t.Errorf("%s reports no execute()", node.File)
		}
		if len(node.Unsupported) != 0 {
			t.Errorf("%s unsupported = %v, want none", node.File, node.Unsupported)
		}
		if node.Description["displayName"] == "" {
			t.Errorf("%s carries no description", node.File)
		}
	}

	// Two outputs and an expression default have to survive the round trip:
	// the converter maps them, so the catalogue must not flatten them.
	var greet map[string]any
	for _, node := range pkg.Nodes {
		if node.Name == "fixtureGreet" {
			greet = node.Description
		}
	}
	if outputs, ok := greet["outputs"].([]any); !ok || len(outputs) != 2 {
		t.Errorf("fixtureGreet outputs = %v, want two ports", greet["outputs"])
	}
	properties, ok := greet["properties"].([]any)
	if !ok || len(properties) == 0 {
		t.Fatalf("fixtureGreet properties = %v, want the property list", greet["properties"])
	}
	found := map[string]bool{}
	for _, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		found[property["name"].(string)] = true
	}
	for _, name := range []string{"name", "mode", "providerId", "loud", "extras", "headers", "hiddenValue"} {
		if !found[name] {
			t.Errorf("fixtureGreet lost the %q property", name)
		}
	}

	if len(pkg.Credentials) != 1 {
		t.Fatalf("loaded %d credentials, want 1: %+v", len(pkg.Credentials), pkg.Credentials)
	}
	credential := pkg.Credentials[0]
	if credential.Name != "fixtureApi" || credential.DisplayName != "Fixture API" {
		t.Errorf("credential = %+v, want fixtureApi", credential)
	}
	if credential.DocumentationURL == "" {
		t.Error("the credential carries no documentationUrl")
	}
	if len(credential.Properties) != 2 {
		t.Fatalf("credential has %d properties, want apiKey and baseUrl", len(credential.Properties))
	}
	if typeOptions, ok := credential.Properties[0]["typeOptions"].(map[string]any); !ok || typeOptions["password"] != true {
		t.Errorf("credential apiKey typeOptions = %v, want password: true", credential.Properties[0]["typeOptions"])
	}
}

// TestManifestApiVersionAndPathEscapeAreRefused is the manifest-level policy: a
// package that declares the wrong API version, names a path outside itself, or
// reaches outside through a symbolic link is refused whole, with one named
// error per defect.
func TestManifestApiVersionAndPathEscapeAreRefused(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-badmanifest")

	pkg := packageNamed(t, cat, "kf-fixture-badmanifest")
	if len(pkg.Nodes) != 0 {
		t.Errorf("loaded %+v, want the package refused before any file was required", pkg.Nodes)
	}
	want := map[string]string{
		"manifest-api-version": "n8nNodesApiVersion is 2",
		"path-escape":          "resolves outside the package directory",
		"symlink-escape":       "through a symbolic link",
	}
	for code, mention := range want {
		var found *catalogueError
		for index, issue := range pkg.Errors {
			if issue.Code == code {
				found = &pkg.Errors[index]
			}
		}
		if found == nil {
			t.Errorf("no %q error among %v", code, errorCodes(pkg.Errors))
			continue
		}
		if found.Severity != "fatal" {
			t.Errorf("%s severity = %q, want fatal: a manifest defect refuses the package", code, found.Severity)
		}
		if found.Package != "kf-fixture-badmanifest" {
			t.Errorf("%s names package %q, want the refused package", code, found.Package)
		}
		if !strings.Contains(found.Message, mention) {
			t.Errorf("%s message = %q, want it to mention %q", code, found.Message, mention)
		}
	}
}

// TestPackageThatThrowsAtLoadIsReportedByName pins the per-file failure policy
// from the other side: a file that throws while it is evaluated does not
// refuse the package, and the error names both the package and the file.
func TestPackageThatThrowsAtLoadIsReportedByName(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-badload")

	pkg := packageNamed(t, cat, "kf-fixture-badload")
	if len(pkg.Nodes) != 0 {
		t.Errorf("loaded %+v, want the throwing file excluded", pkg.Nodes)
	}
	if len(pkg.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly the file failure", pkg.Errors)
	}
	issue := pkg.Errors[0]
	if issue.Code != "load-failed" || issue.Severity != "file" {
		t.Errorf("issue = %+v, want a file-severity load-failed", issue)
	}
	if issue.File != "dist/nodes/Bad/Bad.node.js" {
		t.Errorf("issue file = %q, want the manifest path of the failing file", issue.File)
	}
	if !strings.Contains(issue.Message, "refuses to load") {
		t.Errorf("issue message = %q, want the package's own error", issue.Message)
	}
}

// TestPerFileLoadFailureExcludesOnlyThatNode is the policy the ticket fixes: a
// node file whose peer dependency is missing excludes that node, with a
// message the operator can act on, and neither its siblings nor another
// package are affected.
func TestPerFileLoadFailureExcludesOnlyThatNode(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-versions", "kf-fixture-nodes")

	versioned := packageNamed(t, cat, "kf-fixture-versions")
	if len(versioned.Nodes) != 2 {
		t.Fatalf("loaded %+v, want the two loadable nodes", versioned.Nodes)
	}
	if len(versioned.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly the missing peer", versioned.Errors)
	}
	issue := versioned.Errors[0]
	if issue.Code != "missing-module" || issue.Severity != "file" {
		t.Errorf("issue = %+v, want a file-severity missing-module", issue)
	}
	if issue.File != "dist/nodes/Trigger/Trigger.node.js" {
		t.Errorf("issue file = %q, want the trigger file", issue.File)
	}
	if !strings.Contains(issue.Message, `"kf-absent-peer"`) || !strings.Contains(issue.Message, "peer dependency") {
		t.Errorf("issue message = %q, want it to name the module and say what to install", issue.Message)
	}
	neighbours := packageNamed(t, cat, "kf-fixture-nodes")
	if len(neighbours.Nodes) != 2 || len(neighbours.Errors) != 0 {
		t.Errorf("the neighbouring package changed: %+v", neighbours)
	}
}

// TestPackageReachingTheNetworkAtLoadIsRefused is the fatal side of the load
// policy: a manifest problem, a missing directory or a network attempt while a
// package is being evaluated refuses the package (the boot, for the host that
// asked for it) instead of loading half of it. Requiring a file is as
// untrusted as running it, so the guard is installed before the first require.
func TestPackageReachingTheNetworkAtLoadIsRefused(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-netload")

	pkg := packageNamed(t, cat, "kf-fixture-netload")
	if len(pkg.Nodes) != 0 {
		t.Errorf("loaded %+v, want no node from a package that opened a socket at load", pkg.Nodes)
	}
	if len(pkg.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly the refused load", pkg.Errors)
	}
	issue := pkg.Errors[0]
	if issue.Code != "network-refused" || issue.Severity != "fatal" {
		t.Errorf("issue = %+v, want a fatal network-refused", issue)
	}
	if issue.Package != "kf-fixture-netload" || !strings.Contains(issue.Message, "net.connect") {
		t.Errorf("issue = %+v, want it to name the package and the route", issue)
	}
}

// TestDescribeRecordsShapesTheConverterRefuses proves the catalogue is a
// faithful record of what a package declares even where the shape cannot be
// converted: the runner reports it and the conversion stage decides, so an
// unsupported shape is never silently half-emitted.
func TestDescribeRecordsShapesTheConverterRefuses(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-unsupported")

	pkg := packageNamed(t, cat, "kf-fixture-unsupported")
	if len(pkg.Errors) != 0 {
		t.Fatalf("loading the fixture reported %+v", pkg.Errors)
	}
	byName := map[string]catalogueNode{}
	for _, node := range pkg.Nodes {
		byName[node.Name] = node
	}
	distinct, ok := byName["fixtureDistinct"]
	if !ok {
		t.Fatalf("catalogue = %+v, want the node with the unsupported shapes", pkg.Nodes)
	}
	if !distinct.Execute {
		t.Error("fixtureDistinct reports no execute()")
	}
	inputs, _ := distinct.Description["inputs"].([]any)
	if len(inputs) != 1 || inputs[0] != "ai_tool" {
		t.Errorf("inputs = %v, want the declared ai_tool connection type", distinct.Description["inputs"])
	}
	properties, _ := distinct.Description["properties"].([]any)
	if len(properties) != 2 {
		t.Fatalf("properties = %v, want the resourceLocator and the numeric options", distinct.Description["properties"])
	}
	locator, _ := properties[0].(map[string]any)
	if locator["type"] != "resourceLocator" {
		t.Errorf("first property type = %v, want resourceLocator", locator["type"])
	}
	numeric, _ := properties[1].(map[string]any)
	options, _ := numeric["options"].([]any)
	if len(options) != 2 {
		t.Fatalf("options = %v, want the numeric values", numeric["options"])
	}
	first, _ := options[0].(map[string]any)
	if first["value"] != float64(1) {
		t.Errorf("option value = %#v, want the declared number rather than a stringified one", first["value"])
	}

	trigger, ok := byName["fixtureTrigger"]
	if !ok {
		t.Fatalf("catalogue = %+v, want the trigger node too", pkg.Nodes)
	}
	if trigger.Execute {
		t.Error("the trigger node reports an execute()")
	}
	if len(trigger.Unsupported) != 1 || trigger.Unsupported[0] != "webhook" {
		t.Errorf("trigger unsupported = %v, want the webhook method to be recorded", trigger.Unsupported)
	}
}

// TestVersionedNodesShareANameAndDispatchByVersion covers the layout the
// owner's own package ships: one node name in two files, at versions 1 and 2,
// dispatched on (name, version).
func TestVersionedNodesShareANameAndDispatchByVersion(t *testing.T) {
	fixture := newRunnerFixture(t)
	cat := fixture.describe(t, "kf-fixture-versions")
	pkg := packageNamed(t, cat, "kf-fixture-versions")

	versions := map[string]string{}
	for _, node := range pkg.Nodes {
		versions[node.File] = string(node.Version)
	}
	if versions["dist/nodes/Foo.node.js"] != "1" || versions["dist/nodes/v2/FooV2.node.js"] != "2" {
		t.Fatalf("versions = %v, want 1 and 2 under one node name", versions)
	}
	if pkg.Nodes[0].Name != pkg.Nodes[1].Name {
		t.Fatalf("names = %q and %q, want the same name", pkg.Nodes[0].Name, pkg.Nodes[1].Name)
	}

	pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-versions"}, nil)
	defer pool.Close()
	for _, testCase := range []struct {
		version float64
		want    float64
	}{{1, 1}, {2, 2}} {
		result, err := pool.Execute(context.Background(), Request{
			Tenant: "versioned", Node: "fixtureVersioned", NodeVersion: testCase.version,
			Params: map[string]any{"text": "hello"},
			Items:  []Item{{JSON: map[string]any{}}},
		})
		if err != nil {
			t.Fatalf("Execute(version %v) error = %v", testCase.version, err)
		}
		if got := result.Items[0].JSON["version"]; got != testCase.want {
			t.Errorf("version %v dispatched to the node that answered %v", testCase.version, got)
		}
		if got := result.Items[0].JSON["text"]; got != "hello" {
			t.Errorf("version %v lost its parameter: %v", testCase.version, got)
		}
	}

	_, err := pool.Execute(context.Background(), Request{
		Tenant: "versioned", Node: "fixtureVersioned", NodeVersion: 9,
		Items: []Item{{JSON: map[string]any{}}},
	})
	callErr := requireCallError(t, err, "unknown-node")
	if !strings.Contains(callErr.ChildMessage, "changed since") {
		t.Errorf("message = %q, want it to say the packages may have changed since boot", callErr.ChildMessage)
	}
}

// TestExecuteRunsAProgrammaticNodePerItem runs a node the way the engine runs
// one per item when a run continues on failure: per-item parameters, the
// decrypted credential, two output ports and the pairedItem the package
// stamped.
func TestExecuteRunsAProgrammaticNodePerItem(t *testing.T) {
	fixture := newRunnerFixture(t)
	pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-nodes"}, nil)
	defer pool.Close()

	credentials := map[string]map[string]string{
		"fixtureApi": {"apiKey": "secret-key", "baseUrl": "https://relay.invalid"},
	}
	result, err := pool.Execute(context.Background(), Request{
		Tenant: "per-item", Node: "fixtureGreet", NodeVersion: 1,
		Credentials: credentials,
		Items:       []Item{{JSON: map[string]any{}}, {JSON: map[string]any{}}, {JSON: map[string]any{}}},
		ParamsByItem: []map[string]any{
			{"name": "ada", "mode": "plain", "loud": false, "providerId": "fixture"},
			{"name": "grace", "mode": "shout", "loud": false, "providerId": "other", "extras": map[string]any{"suffix": "!"}},
			{"name": "alan", "mode": "plain", "loud": true, "providerId": "fixture"},
		},
		Context: &RunContext{NodeName: "Greet", NodeType: "sidecar.kf-fixture-nodes.fixtureGreet", Mode: "manual", Timezone: "UTC"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Outputs) != 2 {
		t.Fatalf("outputs = %d ports, want 2", len(result.Outputs))
	}
	if len(result.Outputs[0]) != 3 {
		t.Fatalf("first port = %+v, want one item per input", result.Outputs[0])
	}
	wantGreetings := []string{"ada via https://relay.invalid", "GRACE! via https://relay.invalid", "ALAN via https://relay.invalid"}
	for index, want := range wantGreetings {
		if got := result.Outputs[0][index].JSON["greeting"]; got != want {
			t.Errorf("item %d greeting = %v, want %q", index, got, want)
		}
	}
	if got := result.Outputs[0][1].JSON["provider"]; got != "other" {
		t.Errorf("item 1 provider = %v, want the second item's own parameter", got)
	}
	if result.Items[0].PairedItem == nil {
		t.Error("the pair the package stamped did not survive the run")
	}
	if got := result.Outputs[1][0].JSON["count"]; got != float64(3) {
		t.Errorf("second port count = %v, want 3", got)
	}
	if got := result.Outputs[1][0].JSON["mode"]; got != "manual" {
		t.Errorf("second port mode = %v, want the run context's mode", got)
	}
}

// TestRelayNodeHTTPRequestGoesThroughTheHostHandler proves the egress rule:
// the package cannot make the request itself, and what it asks the host for
// arrives intact — method, URL, credential header and JSON body — with the
// host's answer back in the node's output.
func TestRelayNodeHTTPRequestGoesThroughTheHostHandler(t *testing.T) {
	fixture := newRunnerFixture(t)
	pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-nodes"}, nil)
	defer pool.Close()

	var got HTTPRequest
	handler := &fakeHost{serve: func(_ context.Context, request HTTPRequest) (HTTPResponse, error) {
		got = request
		return HTTPResponse{Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: []byte(`{"echoed":"pong"}`)}, nil
	}}
	result, err := pool.Execute(context.Background(), Request{
		Tenant: "relay", Node: "fixtureRelay", NodeVersion: 1,
		Credentials: map[string]map[string]string{"fixtureApi": {"apiKey": "secret-key", "baseUrl": "https://relay.invalid"}},
		Items:       []Item{{JSON: map[string]any{"hello": "world"}}},
		Params:      map[string]any{"path": "/v1/echo"},
		Host:        handler,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.Method != "POST" || got.URL != "https://relay.invalid/v1/echo" {
		t.Errorf("host saw %s %s, want POST https://relay.invalid/v1/echo", got.Method, got.URL)
	}
	if got.Headers["X-API-Key"] != "secret-key" {
		t.Errorf("host saw headers %v, want the credential header", got.Headers)
	}
	if string(got.Body) != `{"hello":"world"}` {
		t.Errorf("host saw body %s, want the item's json", got.Body)
	}
	if handler.calls() != 1 {
		t.Errorf("host served %d calls, want 1", handler.calls())
	}
	if got := result.Items[0].JSON["relayed"]; got != "pong" {
		t.Errorf("output = %v, want the host's parsed body", result.Items[0].JSON)
	}
}

// TestDirectNetworkAttemptFailsTheRunEvenWhenSwallowed is the guard's real
// test: every route a package can reach the network through fails the run with
// the same named code, whether the package let the exception escape or
// swallowed it. The run failing is what makes the guard usable — the host must
// not have to trust the package's own error handling.
func TestDirectNetworkAttemptFailsTheRunEvenWhenSwallowed(t *testing.T) {
	fixture := newRunnerFixture(t)
	for _, mode := range []string{"net", "http", "https", "fetch", "dns", "dgram", "websocket"} {
		for _, swallow := range []bool{false, true} {
			name := mode
			if swallow {
				name += "/swallowed"
			}
			t.Run(name, func(t *testing.T) {
				pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-hostile"}, nil)
				defer pool.Close()
				_, err := pool.Execute(context.Background(), Request{
					Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
					Params: map[string]any{"mode": mode, "swallow": swallow},
					Items:  []Item{{JSON: map[string]any{}}},
				})
				callErr := requireCallError(t, err, "network-refused")
				if !strings.Contains(callErr.ChildMessage, "helpers.httpRequest") {
					t.Errorf("message = %q, want it to point at the host helper", callErr.ChildMessage)
				}
				if !strings.Contains(callErr.ChildMessage, "through") {
					t.Errorf("message = %q, want it to name the route", callErr.ChildMessage)
				}
			})
		}
	}
}

// TestRunnerRefusesToSignalOtherProcesses covers the escape Node's permission
// model does not stop: a package can signal the host or another tenant's
// process, so the runner refuses it. The victim here is a process this test
// started, which is still alive afterwards — a fixture that could not signal
// it, not a fixture that was trusted not to.
func TestRunnerRefusesToSignalOtherProcesses(t *testing.T) {
	fixture := newRunnerFixture(t)
	victim := exec.Command("/bin/sh", "-c", "sleep 300")
	if err := victim.Start(); err != nil {
		t.Fatalf("starting the victim process: %v", err)
	}
	victimPid := victim.Process.Pid
	defer func() { _ = victim.Process.Kill(); _, _ = victim.Process.Wait() }()

	pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-hostile"}, nil)
	defer pool.Close()
	_, err := pool.Execute(context.Background(), Request{
		Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
		Params: map[string]any{"mode": "signal", "victimPid": victimPid},
		Items:  []Item{{JSON: map[string]any{}}},
	})
	callErr := requireCallError(t, err, "process-refused")
	if !strings.Contains(callErr.ChildMessage, "process.kill") {
		t.Errorf("message = %q, want it to name the member the package used", callErr.ChildMessage)
	}
	if err := victim.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the victim process was signalled: %v", err)
	}
}

// TestChildProcessAndNativeAddonAreRefusedByThePermissionModel covers the two
// escapes Node refuses for us. Both are execute-time attempts: the modules load
// and the calls do not.
func TestChildProcessAndNativeAddonAreRefusedByThePermissionModel(t *testing.T) {
	fixture := newRunnerFixture(t)
	for _, testCase := range []struct{ mode, want string }{
		{mode: "child-process", want: "permission-denied"},
		{mode: "addon", want: "addon-disabled"},
		{mode: "fs-outside", want: "permission-denied"},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-hostile"}, nil)
			defer pool.Close()
			_, err := pool.Execute(context.Background(), Request{
				Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
				Params: map[string]any{"mode": testCase.mode},
				Items:  []Item{{JSON: map[string]any{}}},
			})
			callErr := requireCallError(t, err, testCase.want)
			if callErr.ChildMessage == "" {
				t.Error("the failure carries no diagnostic")
			}
		})
	}
}

// TestUnsupportedHelperNamesItself and
// TestUnsupportedHttpRequestOptionNamesItself pin the refusal-by-name rule: an
// unimplemented member or option is reported, never silently ignored, because
// a silently ignored `auth` or `proxy` is a boundary wider than the caller
// believes.
func TestUnsupportedHelperNamesItself(t *testing.T) {
	fixture := newRunnerFixture(t)
	for _, testCase := range []struct{ mode, want string }{
		{mode: "unsupported-helper", want: "httpRequestWithAuthentication"},
		{mode: "unsupported-this", want: "this.someMemberThisSidecarDoesNotImplement"},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-hostile"}, nil)
			defer pool.Close()
			_, err := pool.Execute(context.Background(), Request{
				Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
				Params: map[string]any{"mode": testCase.mode},
				Items:  []Item{{JSON: map[string]any{}}},
			})
			callErr := requireCallError(t, err, "unsupported")
			if !strings.Contains(callErr.ChildMessage, testCase.want) {
				t.Errorf("message = %q, want it to name %q", callErr.ChildMessage, testCase.want)
			}
		})
	}
}

func TestUnsupportedHttpRequestOptionNamesItself(t *testing.T) {
	fixture := newRunnerFixture(t)
	pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-hostile"}, nil)
	defer pool.Close()
	_, err := pool.Execute(context.Background(), Request{
		Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
		Params: map[string]any{"mode": "unsupported-http"},
		Items:  []Item{{JSON: map[string]any{}}},
	})
	callErr := requireCallError(t, err, "unsupported")
	if !strings.Contains(callErr.ChildMessage, "skipSslCertificateValidation") {
		t.Errorf("message = %q, want it to name the unsupported option", callErr.ChildMessage)
	}
}

// TestNodeCannotSeeHostEnvironment puts a canary in this process's environment
// and asks the node to dump what it can see. The child environment is an
// explicit allowlist, so the canary must not appear — the credential master key
// and the database DSN are the same shape of secret.
func TestNodeCannotSeeHostEnvironment(t *testing.T) {
	fixture := newRunnerFixture(t)
	const canary = "KILASFLOW_SIDECAR_ENV_CANARY"
	t.Setenv(canary, "must-not-cross-the-socket")

	pool := fixture.pool(&lockedWriter{}, []string{"kf-fixture-hostile"}, nil)
	defer pool.Close()
	result, err := pool.Execute(context.Background(), Request{
		Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
		Params: map[string]any{"mode": "env"},
		Items:  []Item{{JSON: map[string]any{}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	env, ok := result.Items[0].JSON["env"].(map[string]any)
	if !ok {
		t.Fatalf("the node reported no environment: %+v", result.Items[0].JSON)
	}
	if _, leaked := env[canary]; leaked {
		t.Errorf("the child environment carried %s", canary)
	}
	if env["LANG"] != "C.UTF-8" {
		t.Errorf("the child environment = %v, want the allowlisted LANG", env)
	}
	for _, key := range []string{"PATH", "HOME", "KILASFLOW_MASTER_KEY", "DATABASE_URL"} {
		if _, leaked := env[key]; leaked {
			t.Errorf("the child environment carried %s", key)
		}
	}
}

// TestTenantMismatchFrameIsRefusedByTheRunner is the defence in depth behind
// the pool's keying: even if a frame reached the wrong process, the process
// refuses it rather than running one tenant's node with another tenant's
// credentials in scope.
func TestTenantMismatchFrameIsRefusedByTheRunner(t *testing.T) {
	fixture := newRunnerFixture(t)
	spawn := fixture.spawn([]string{"kf-fixture-nodes"}, nil, nil)
	child, err := spawn(context.Background(), "tenant-a", "")
	if err != nil {
		t.Fatalf("spawn() error = %v", err)
	}
	defer func() { _ = child.Kill() }()

	frame := executeFrame{
		Type: frameExecute, ID: "mismatch-1", Tenant: "tenant-b", Node: "fixtureGreet", NodeVersion: 1,
		Credentials: map[string]map[string]string{"fixtureApi": {"baseUrl": "https://relay.invalid"}},
		Items:       []Item{{JSON: map[string]any{}}},
	}
	if err := writeFrame(child.Conn, frame); err != nil {
		t.Fatalf("writing the frame: %v", err)
	}
	reader := newFrameReader(child.Conn, DefaultLimits().MaxFrameBytes)
	line, err := reader.next()
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	answer, err := decodeTerminal(line)
	if err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answer.Type != frameError || answer.Code != "tenant-mismatch" {
		t.Fatalf("answer = %+v, want a tenant-mismatch error", answer)
	}
	if !strings.Contains(answer.Message, "tenant-b") {
		t.Errorf("message = %q, want it to name the tenant it refused", answer.Message)
	}
}

// TestBannerAtRequireTimeDoesNotCorruptTheProtocol covers the third acceptance
// criterion: a package that prints a banner and, worse, a forged protocol
// frame on stdout cannot desynchronise the socket. The forged lines reach the
// diagnostics log and nothing else.
func TestBannerAtRequireTimeDoesNotCorruptTheProtocol(t *testing.T) {
	fixture := newRunnerFixture(t)
	diagnostics := &lockedWriter{}
	cat, err := Discover(context.Background(), fixture.spawn([]string{"kf-fixture-hostile"}, diagnostics, nil), runnerLimits())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	var parsed catalogue
	if err := json.Unmarshal(cat, &parsed); err != nil {
		t.Fatalf("the catalogue was not readable: %v", err)
	}
	pkg := packageNamed(t, parsed, "kf-fixture-hostile")
	var noisy *catalogueNode
	for index, node := range pkg.Nodes {
		if node.Name == "fixtureNoisy" {
			noisy = &pkg.Nodes[index]
		}
	}
	if noisy == nil {
		t.Fatalf("the noisy node is missing from %+v", pkg.Nodes)
	}
	transcript := diagnostics.String()
	if !strings.Contains(transcript, "this banner is diagnostics only") {
		t.Errorf("diagnostics = %q, want the banner", transcript)
	}
	if !strings.Contains(transcript, `"id":"forged"`) {
		t.Errorf("diagnostics = %q, want the forged frame to have landed in the log", transcript)
	}

	pool := fixture.pool(diagnostics, []string{"kf-fixture-hostile"}, nil)
	defer pool.Close()
	result, err := pool.Execute(context.Background(), Request{
		Tenant: "noisy", Node: "fixtureNoisy", NodeVersion: 1,
		Items: []Item{{JSON: map[string]any{"kept": true}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].JSON["kept"] != true {
		t.Errorf("result = %+v, want the node's own item", result.Items)
	}
}

// TestHostilePackageCrashAndHangFailOnlyThatRun runs the process-level escapes
// through the runner rather than through a bare script: a package that crashes,
// hangs or exhausts its memory fails that run with a named code, and the pool
// replaces the process for the next one.
func TestHostilePackageCrashAndHangFailOnlyThatRun(t *testing.T) {
	fixture := newRunnerFixture(t)
	cases := []struct {
		name    string
		mode    string
		code    string
		adjust  func(*RunnerConfig)
		timeout time.Duration
	}{
		{name: "crash", mode: "crash", code: CodeSidecarCrash},
		{name: "hang", mode: "hang", code: CodeSidecarTimeout, timeout: 2 * time.Second},
		{name: "heap", mode: "heap", code: CodeMemoryLimit, adjust: func(config *RunnerConfig) { config.HeapMB = 64 }},
		{name: "buffer", mode: "buffer", code: CodeMemoryLimit, adjust: func(config *RunnerConfig) { config.MaxRSSMB = 128 }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			limits := runnerLimits()
			if testCase.timeout > 0 {
				limits.Timeout = testCase.timeout
			}
			pool := NewPool(fixture.spawn([]string{"kf-fixture-hostile"}, nil, testCase.adjust), limits)
			defer pool.Close()

			_, err := pool.Execute(context.Background(), Request{
				Tenant: "hostile", Node: "fixtureHostile", NodeVersion: 1,
				Params: map[string]any{"mode": testCase.mode},
				Items:  []Item{{JSON: map[string]any{}}},
			})
			callErr, ok := err.(*CallError)
			if !ok {
				t.Fatalf("error = %T (%v), want *CallError", err, err)
			}
			if callErr.Code != testCase.code {
				t.Fatalf("code = %q (%s), want %q", callErr.Code, callErr.Detail, testCase.code)
			}

			// The host is intact and the next run gets a fresh process: the
			// surviving node of the same package answers on it.
			result, err := pool.Execute(context.Background(), Request{
				Tenant: "hostile", Node: "fixtureNoisy", NodeVersion: 1,
				Items: []Item{{JSON: map[string]any{"after": testCase.name}}},
			})
			if err != nil {
				t.Fatalf("the run after %s failed: %v", testCase.name, err)
			}
			if result.Items[0].JSON["after"] != testCase.name {
				t.Errorf("the run after %s answered %+v", testCase.name, result.Items)
			}
		})
	}
}

// TestOwnersCommunityPackageLoads is the independent proof: the fixtures are
// written by the same hand as the runner, so a package compiled from a real
// TypeScript source is loaded here as well. Point
// KILASFLOW_TEST_COMMUNITY_PACKAGES_DIR at a directory holding the compiled
// package (built outside this repository) and name it in
// KILASFLOW_TEST_COMMUNITY_PACKAGE to run it; without both the test skips and
// says so. The package is named by the environment rather than here, so the
// repository does not depend on any one host product's package.
func TestOwnersCommunityPackageLoads(t *testing.T) {
	packagesDir := os.Getenv("KILASFLOW_TEST_COMMUNITY_PACKAGES_DIR")
	packageName := os.Getenv("KILASFLOW_TEST_COMMUNITY_PACKAGE")
	if packagesDir == "" || packageName == "" {
		t.Skip("set KILASFLOW_TEST_COMMUNITY_PACKAGES_DIR to a directory holding a compiled community package, and KILASFLOW_TEST_COMMUNITY_PACKAGE to its npm name, to run this")
	}
	node := sidecartest.Node(t)
	runnerPath, err := ExtractRunner(t.TempDir())
	if err != nil {
		t.Fatalf("ExtractRunner() error = %v", err)
	}
	spawn := RunnerSpawn(RunnerConfig{
		NodePath:    node,
		RunnerPath:  runnerPath,
		PackagesDir: packagesDir,
		Packages:    []string{packageName},
	})
	raw, err := Discover(context.Background(), spawn, runnerLimits())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	var cat catalogue
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatalf("unmarshalling catalogue: %v", err)
	}
	pkg := packageNamed(t, cat, packageName)

	convertible := 0
	for _, entry := range pkg.Nodes {
		groups, _ := entry.Description["group"].([]any)
		trigger := false
		for _, group := range groups {
			if group == "trigger" {
				trigger = true
			}
		}
		if trigger {
			continue
		}
		if !entry.Execute {
			t.Errorf("%s has no execute() and is not a trigger", entry.File)
			continue
		}
		convertible++
	}
	excluded := 0
	for _, entry := range pkg.Nodes {
		groups, _ := entry.Description["group"].([]any)
		for _, group := range groups {
			if group == "trigger" {
				excluded++
			}
		}
	}
	for _, issue := range pkg.Errors {
		t.Logf("load issue %s %s (%s): %s", issue.Package, issue.File, issue.Code, issue.Message)
		if issue.Severity == "file" {
			excluded++
		}
	}
	if convertible != 8 || excluded != 2 {
		t.Errorf("package yields %d convertible definitions and %d exclusions, want 8 and 2 (nodes: %d, errors: %d)",
			convertible, excluded, len(pkg.Nodes), len(pkg.Errors))
	}
}

// fakeHost is a HostHandler that answers the host calls a package makes, so
// the runner's HTTP helper can be tested before the safehttp proxy exists.
type fakeHost struct {
	serve func(context.Context, HTTPRequest) (HTTPResponse, error)
	count int
}

func (host *fakeHost) HTTP(ctx context.Context, request HTTPRequest) (HTTPResponse, error) {
	host.count++
	return host.serve(ctx, request)
}

func (host *fakeHost) calls() int { return host.count }
