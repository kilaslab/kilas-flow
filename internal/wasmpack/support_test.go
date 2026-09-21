package wasmpack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// Everything in this package is serial on purpose: wazero v1.9.0's
// internal/version cache is written without synchronization, so two runtimes
// created at the same moment are a data race inside the dependency
// (.pine/memory/code-node.md). No test here calls t.Parallel().

// sharedModules is one translation cache for the whole test binary, closed in
// TestMain. A Go wasip1 guest costs the better part of a second to translate
// warm and about twelve seconds under -race, so a per-test cache would make
// this suite unusable.
var (
	modulesOnce   sync.Once
	sharedModules *runcode.ModuleCache
)

func testModules() *runcode.ModuleCache {
	modulesOnce.Do(func() { sharedModules = runcode.NewModuleCache() })
	return sharedModules
}

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedModules != nil {
		_ = sharedModules.Close(context.Background())
	}
	os.Exit(code)
}

// moduleRoot walks up from this file to the directory holding go.mod, so the
// probe is built independently of where `go test` was invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller refused to say where this test lives")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test file")
		}
		dir = parent
	}
}

// probeGuest builds the Go probe guest once per test binary.
func probeGuest(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no Go toolchain reachable; the probe guest cannot be built here")
	}
	probeOnce.Do(func() {
		directory, err := os.MkdirTemp("", "kf-wasmpack-probe")
		if err != nil {
			probeErr = err
			return
		}
		defer os.RemoveAll(directory)
		wasmPath := filepath.Join(directory, "probe.wasm")
		build := exec.Command("go", "build", "-o", wasmPath, "./internal/wasmpack/testdata/probe")
		build.Dir = moduleRoot(t)
		build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if output, err := build.CombinedOutput(); err != nil {
			probeErr = fmt.Errorf("building the probe guest failed: %v\n%s", err, output)
			return
		}
		probeBytes, err = os.ReadFile(wasmPath)
		if err != nil {
			probeErr = err
		}
	})
	if probeErr != nil {
		t.Fatalf("%v", probeErr)
	}
	return probeBytes
}

var (
	probeOnce  sync.Once
	probeBytes []byte
	probeErr   error
)

// exampleGuest builds one of the SDK's example packs, the way a pack author
// builds it, once per test binary.
func exampleGuest(t *testing.T, name string) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no Go toolchain reachable; an example pack cannot be built here")
	}
	examplesOnce.Do(func() {
		directory, err := os.MkdirTemp("", "kf-wasmpack-examples")
		if err != nil {
			exampleErr = err
			return
		}
		defer os.RemoveAll(directory)
		exampleBytes = map[string][]byte{}
		for _, example := range []string{"echo", "fetch"} {
			wasmPath := filepath.Join(directory, example+".wasm")
			build := exec.Command("go", "build", "-o", wasmPath, "./pkg/sdk/example/"+example)
			build.Dir = moduleRoot(t)
			build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
			if output, err := build.CombinedOutput(); err != nil {
				exampleErr = fmt.Errorf("building example/%s failed: %v\n%s", example, err, output)
				return
			}
			built, err := os.ReadFile(wasmPath)
			if err != nil {
				exampleErr = err
				return
			}
			exampleBytes[example] = built
		}
	})
	if exampleErr != nil {
		t.Fatalf("%v", exampleErr)
	}
	built, found := exampleBytes[name]
	if !found {
		t.Fatalf("no example pack named %q", name)
	}
	return built
}

var (
	examplesOnce sync.Once
	exampleBytes map[string][]byte
	exampleErr   error
)

// probeCommand is the probe's instruction, and probeReport its answer. They are
// the fixture's wire contract, spelled out here rather than shared, because a
// test that agreed with the fixture by construction would not notice the
// fixture changing shape.
type probeCommand struct {
	Op              string            `json:"op"`
	URL             string            `json:"url,omitempty"`
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Credential      string            `json:"credential,omitempty"`
	FollowRedirects bool              `json:"followRedirects,omitempty"`
	TimeoutMS       int               `json:"timeoutMs,omitempty"`
	Body            string            `json:"body,omitempty"`

	CredentialType string `json:"credentialType,omitempty"`
	Field          string `json:"field,omitempty"`

	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      string `json:"data,omitempty"`
}

type probeReport struct {
	OK        bool                `json:"ok"`
	Error     string              `json:"error,omitempty"`
	ErrorCode string              `json:"errorCode,omitempty"`
	Status    int                 `json:"status,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	Body      string              `json:"body,omitempty"`
	BodySize  int                 `json:"bodySize,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
	Value     string              `json:"value,omitempty"`
	Ref       *sdk.BinaryRef      `json:"ref,omitempty"`
	Data      string              `json:"data,omitempty"`
}

// runProbe drives the probe guest with one command.
func runProbe(t *testing.T, host *wasmpack.Host, inv wasmpack.Invocation, instruction probeCommand) (probeReport, runcode.Outcome, *wasmpack.Effects, error) {
	t.Helper()
	encoded, err := json.Marshal(instruction)
	if err != nil {
		t.Fatalf("encoding the probe command failed: %v", err)
	}
	inv.Module = probeGuest(t)
	inv.Stdin = encoded
	outcome, effects, err := host.Invoke(context.Background(), inv)
	report := probeReport{}
	if len(outcome.Stdout) > 0 {
		if decodeErr := json.Unmarshal(outcome.Stdout, &report); decodeErr != nil {
			t.Fatalf("the probe's report is not JSON: %v\n%s", decodeErr, outcome.Stdout)
		}
	}
	return report, outcome, effects, err
}

// fullCaps declares every capability. The probe guest reaches all six host
// functions, so a run of it needs them all registered; the capability-specific
// refusals are tested against hand-built modules instead, where the module only
// imports what the case is about.
func fullCaps() wasmpack.Capabilities {
	return wasmpack.Capabilities{
		HTTP:        true,
		Credentials: []string{"wahaApi", "telegramApi"},
		BinaryRead:  true,
		BinaryWrite: true,
	}
}

// wahaCredential is the credential the tests attach to a node: WAHA has a
// non-secret baseUrl and a secret apiKey, and it authenticates with a header,
// which is everything the credential half of the ABI needs to exercise.
func wahaCredential(allowedDomains ...string) engine.Credential {
	return engine.Credential{
		ID: "cred_waha", Name: "WAHA", Type: "wahaApi",
		Fields:         map[string]string{"baseUrl": "https://waha.example.test", "apiKey": "waha-secret"},
		AllowedDomains: allowedDomains,
	}
}

// wahaNode attaches the credential the way a node does: by type.
func wahaNode() workflow.IRNode {
	return workflow.IRNode{
		Name:        "Fetch",
		Credentials: map[string]string{"wahaApi": "cred_waha"},
	}
}

// countingResolver counts what it was asked for, so a test can prove a refusal
// happened before the secret was ever looked up.
type countingResolver struct {
	credential engine.Credential
	err        error
	calls      int
}

func (resolver *countingResolver) ResolveCredential(context.Context, string) (engine.Credential, error) {
	resolver.calls++
	if resolver.err != nil {
		return engine.Credential{}, resolver.err
	}
	return resolver.credential, nil
}

// fakeBinaries is the payload store the runtime would supply.
type fakeBinaries struct {
	payloads map[string][]byte
	puts     int
	gets     int
	err      error
}

func newFakeBinaries(payloads map[string][]byte) *fakeBinaries {
	if payloads == nil {
		payloads = map[string][]byte{}
	}
	return &fakeBinaries{payloads: payloads}
}

func (store *fakeBinaries) Put(name, mediaType string, body io.Reader) (workflow.BinaryRef, error) {
	store.puts++
	if store.err != nil {
		return workflow.BinaryRef{}, store.err
	}
	contents, err := io.ReadAll(body)
	if err != nil {
		return workflow.BinaryRef{}, err
	}
	ref := workflow.BinaryRef{
		ID:        fmt.Sprintf("bin_%d", len(store.payloads)+1),
		FileName:  name,
		MediaType: mediaType,
		Size:      int64(len(contents)),
	}
	store.payloads[ref.ID] = contents
	return ref, nil
}

func (store *fakeBinaries) Get(id string) (io.ReadCloser, workflow.BinaryRef, error) {
	store.gets++
	if store.err != nil {
		return nil, workflow.BinaryRef{}, store.err
	}
	contents, found := store.payloads[id]
	if !found {
		return nil, workflow.BinaryRef{}, errors.New("no such payload")
	}
	return io.NopCloser(bytes.NewReader(contents)), workflow.BinaryRef{
		ID: id, Size: int64(len(contents)),
	}, nil
}

// loopbackPolicy allows exactly the servers a test names, and never
// AllowPrivateNetworks: the point of these tests is that the guard is on.
func loopbackPolicy(t *testing.T, servers ...*httptest.Server) safehttp.Policy {
	t.Helper()
	policy := safehttp.DefaultPolicy()
	for _, server := range servers {
		policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, strings.TrimPrefix(server.URL, "http://"))
	}
	return policy
}

// hostImport is the wasmtest import for one ABI function, with the signature
// the ABI declares.
func hostImport(t *testing.T, name string) wasmtest.Import {
	t.Helper()
	function, found := sdk.FunctionNamed(name)
	if !found {
		t.Fatalf("no ABI function named %q", name)
	}
	params := make([]wasmtest.ValueType, len(function.Params))
	for index := range params {
		params[index] = wasmtest.I32
	}
	return wasmtest.Import{
		Module: sdk.HostModule, Name: name,
		Params: params, Results: []wasmtest.ValueType{wasmtest.I32},
	}
}

// guestCall assembles a module whose _start calls the import at index 0 with
// args and traps unless the host answered want.
func guestCall(t *testing.T, name string, args []int, want int32, data []wasmtest.DataSegment, memPages uint32) []byte {
	t.Helper()
	return guest(t, []string{name}, callWith(0, args, want), data, memPages)
}

// guest assembles a module importing imports, running body as its _start.
func guest(t *testing.T, imports []string, body []byte, data []wasmtest.DataSegment, memPages uint32) []byte {
	t.Helper()
	imported := make([]wasmtest.Import, len(imports))
	for index, name := range imports {
		imported[index] = hostImport(t, name)
	}
	return wasmtest.Build(imported, body, memPages, data)
}

// callWith pushes args and asserts the function at index answers want.
func callWith(index uint32, args []int, want int32) []byte {
	body := []byte{}
	for _, arg := range args {
		body = append(body, wasmtest.I32Const(arg)...)
	}
	return append(body, wasmtest.ExpectResult(index, int(want))...)
}

// dataAt lays a string into linear memory at an offset and returns the offset
// and length to pass as (pointer, length).
func dataAt(offset int, text string) wasmtest.DataSegment {
	return wasmtest.DataSegment{Offset: uint32(offset), Bytes: []byte(text)}
}

// runGuest executes a hand-built module and returns its outcome.
func runGuest(t *testing.T, host *wasmpack.Host, module []byte, inv wasmpack.Invocation) (runcode.Outcome, *wasmpack.Effects, error) {
	t.Helper()
	inv.Module = module
	return host.Invoke(context.Background(), inv)
}

// engineRequest builds the runtime input the host reads from: a credential
// resolver, and whatever else a case needs.
func engineRequest(resolver engine.CredentialResolver, options ...func(*engine.Request)) engine.Request {
	request := engine.Request{Credentials: resolver}
	for _, option := range options {
		option(&request)
	}
	return request
}

// withBinaries supplies the payload store a runtime would thread through.
func withBinaries(store engine.BinaryStore) func(*engine.Request) {
	return func(request *engine.Request) { request.Binaries = store }
}
