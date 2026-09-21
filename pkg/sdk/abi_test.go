package sdk_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
	"github.com/kilaslab/kilas-flow/pkg/sdk/internal/abigen"
)

// The tests in this file are deliberately serial. wazero v1.9.0's
// internal/version cache is written without synchronization, so two runtimes
// created at the same moment are a data race inside the dependency
// (.pine/memory/code-node.md); nothing here uses t.Parallel().

// sharedModules is one translation cache for the whole test binary, closed in
// TestMain. A Go wasip1 guest costs seconds to translate under -race, so a
// per-test cache would make this suite unusable.
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

// TestGeneratedGuestBindingsAreCurrent proves the committed bindings are what
// the ABI table renders, so a row added to Functions without regenerating is a
// failure rather than a pack that imports a function the host never registers.
func TestGeneratedGuestBindingsAreCurrent(t *testing.T) {
	committed, err := os.ReadFile("host_wasip1.go")
	if err != nil {
		t.Fatalf("reading the committed bindings failed: %v", err)
	}
	rendered, err := abigen.Render(sdk.Functions)
	if err != nil {
		t.Fatalf("rendering the bindings failed: %v", err)
	}
	if string(committed) != string(rendered) {
		t.Errorf("host_wasip1.go is stale: run `go generate ./pkg/sdk/...`\n--- committed ---\n%s\n--- rendered ---\n%s",
			committed, rendered)
	}
	for _, function := range sdk.Functions {
		if !strings.Contains(string(committed), "//go:wasmimport "+sdk.HostModule+" "+function.Name) {
			t.Errorf("the bindings do not declare %s on %s", function.Name, sdk.HostModule)
		}
	}
}

// TestTheABITableIsWellFormed checks the properties both sides rely on: names
// that can be looked up, parameters that exist (v1 is i32-only, so arity is the
// whole signature), and a capability constant behind every row.
func TestTheABITableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	used := map[sdk.Capability]bool{}
	for _, function := range sdk.Functions {
		if function.Name == "" {
			t.Error("a function has no name")
			continue
		}
		if seen[function.Name] {
			t.Errorf("%s is declared twice", function.Name)
		}
		seen[function.Name] = true
		if len(function.Params) == 0 {
			t.Errorf("%s has no parameters, so it is not an ABI v1 function", function.Name)
		}
		for _, param := range function.Params {
			if param == "" {
				t.Errorf("%s has an unnamed parameter", function.Name)
			}
		}
		if function.Capability == "" {
			t.Errorf("%s has no capability, so nothing can grant it", function.Name)
		}
		used[function.Capability] = true
		if found, ok := sdk.FunctionNamed(function.Name); !ok || found.Name != function.Name {
			t.Errorf("FunctionNamed(%q) did not find the row", function.Name)
		}
	}
	for _, capability := range []sdk.Capability{
		sdk.CapHTTP, sdk.CapCredentials, sdk.CapBinaryRead, sdk.CapBinaryWrite, sdk.CapResult,
	} {
		if !used[capability] {
			t.Errorf("capability %q grants nothing, so declaring it would be a lie", capability)
		}
	}
	if _, ok := sdk.FunctionNamed("not_a_host_function"); ok {
		t.Error("FunctionNamed found a function that is not in the table")
	}
}

// fakeHost is the native stand-in a pack's author tests against.
type fakeHost struct {
	httpCalls       int
	credentialCalls int
	readCalls       int
	writeCalls      int

	lastRequest sdk.HTTPRequest
	lastBody    []byte
	lastField   string
	lastWrite   sdk.BinaryWrite
	lastData    []byte

	response sdk.HTTPResponse
	body     []byte
	field    string
	payload  []byte
	ref      sdk.BinaryRef
	hostErr  *sdk.HostError
}

func (fake *fakeHost) HTTP(request sdk.HTTPRequest, body []byte) (sdk.HTTPResponse, []byte, *sdk.HostError) {
	fake.httpCalls++
	fake.lastRequest, fake.lastBody = request, body
	if fake.hostErr != nil {
		return sdk.HTTPResponse{}, nil, fake.hostErr
	}
	return fake.response, fake.body, nil
}

func (fake *fakeHost) CredentialField(credentialType, field string) (string, *sdk.HostError) {
	fake.credentialCalls++
	fake.lastField = credentialType + "." + field
	if fake.hostErr != nil {
		return "", fake.hostErr
	}
	return fake.field, nil
}

func (fake *fakeHost) ReadBinary(string) ([]byte, *sdk.HostError) {
	fake.readCalls++
	if fake.hostErr != nil {
		return nil, fake.hostErr
	}
	return fake.payload, nil
}

func (fake *fakeHost) WriteBinary(name, mediaType string, data []byte) (sdk.BinaryRef, *sdk.HostError) {
	fake.writeCalls++
	fake.lastWrite = sdk.BinaryWrite{Name: name, MediaType: mediaType}
	fake.lastData = data
	if fake.hostErr != nil {
		return sdk.BinaryRef{}, fake.hostErr
	}
	return fake.ref, nil
}

// TestSDKFunctionsRoundTripAgainstAFakeHost proves the four capability
// functions carry their arguments and their answers across the slot protocol,
// which is the half a pack author can exercise without a sandbox.
func TestSDKFunctionsRoundTripAgainstAFakeHost(t *testing.T) {
	fake := &fakeHost{
		response: sdk.HTTPResponse{Status: 201, Headers: map[string][]string{"X-Trace": {"abc"}}, BodyLength: 5},
		body:     []byte("hello"),
		field:    "https://api.example.test",
		payload:  []byte("payload bytes"),
		ref:      sdk.BinaryRef{ID: "bin_1", FileName: "report.csv", MediaType: "text/csv", Size: 13},
	}
	sdk.SetHost(fake)
	defer sdk.SetHost(nil)

	request := sdk.HTTPRequest{Method: "POST", URL: "https://api.example.test/items", Credential: "httpHeaderAuth"}
	response, body, err := sdk.HTTP(request, []byte(`{"name":"ada"}`))
	if err != nil {
		t.Fatalf("HTTP() error = %v", err)
	}
	if response.Status != 201 || response.Headers["X-Trace"][0] != "abc" || response.BodyLength != 5 {
		t.Errorf("response = %+v, want the fake's head", response)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want the fake's body", body)
	}
	if fake.lastRequest.Method != request.Method || fake.lastRequest.URL != request.URL ||
		fake.lastRequest.Credential != request.Credential || string(fake.lastBody) != `{"name":"ada"}` {
		t.Errorf("the host received %+v / %q, want what was passed", fake.lastRequest, fake.lastBody)
	}

	value, err := sdk.CredentialField("httpHeaderAuth", "baseUrl")
	if err != nil {
		t.Fatalf("CredentialField() error = %v", err)
	}
	if value != "https://api.example.test" || fake.lastField != "httpHeaderAuth.baseUrl" {
		t.Errorf("CredentialField() = %q (%q), want the fake's field", value, fake.lastField)
	}

	payload, err := sdk.ReadBinary(sdk.BinaryRef{ID: "bin_1"})
	if err != nil {
		t.Fatalf("ReadBinary() error = %v", err)
	}
	if string(payload) != "payload bytes" {
		t.Errorf("ReadBinary() = %q, want the fake's payload", payload)
	}

	ref, err := sdk.WriteBinary("report.csv", "text/csv", []byte("a,b\n1,2\n"))
	if err != nil {
		t.Fatalf("WriteBinary() error = %v", err)
	}
	if ref.ID != "bin_1" || ref.Size != 13 {
		t.Errorf("WriteBinary() = %+v, want the fake's reference", ref)
	}
	if fake.lastWrite.Name != "report.csv" || fake.lastWrite.MediaType != "text/csv" || string(fake.lastData) != "a,b\n1,2\n" {
		t.Errorf("the host received %+v / %q, want what was passed", fake.lastWrite, fake.lastData)
	}
}

// TestAHostRefusalKeepsItsCodeAndMessage proves a refusal arrives as a
// *HostError that answers errors.Is, so a pack can branch on the kind of
// refusal rather than on a message.
func TestAHostRefusalKeepsItsCodeAndMessage(t *testing.T) {
	fake := &fakeHost{hostErr: &sdk.HostError{Code: sdk.CodeBlocked, Message: "request target is not allowed: 169.254.169.254"}}
	sdk.SetHost(fake)
	defer sdk.SetHost(nil)

	_, _, err := sdk.HTTP(sdk.HTTPRequest{Method: "GET", URL: "http://169.254.169.254/latest/meta-data"}, nil)
	if err == nil {
		t.Fatal("HTTP() succeeded against a blocked target")
	}
	if !errors.Is(err, sdk.ErrBlockedError) {
		t.Errorf("errors.Is(err, ErrBlockedError) = false for %v", err)
	}
	if errors.Is(err, sdk.ErrDeniedError) {
		t.Error("a blocked call reported itself as denied")
	}
	var hostErr *sdk.HostError
	if !errors.As(err, &hostErr) || hostErr.Message != "request target is not allowed: 169.254.169.254" {
		t.Errorf("err = %v, want the host's own message", err)
	}
}

// TestCapabilityCallsWithoutAHostAreRefused proves a native build with no fake
// host answers rather than panicking or calling a nil function.
func TestCapabilityCallsWithoutAHostAreRefused(t *testing.T) {
	sdk.SetHost(nil)
	if _, _, err := sdk.HTTP(sdk.HTTPRequest{Method: "GET", URL: "https://example.test"}, nil); !errors.Is(err, sdk.ErrFailedError) {
		t.Errorf("HTTP() error = %v, want a failed host call", err)
	}
	if _, err := sdk.WriteBinary("x", "", nil); !errors.Is(err, sdk.ErrFailedError) {
		t.Errorf("WriteBinary() error = %v, want a failed host call", err)
	}
}

// builtExamples builds both example packs once per test binary, under the same
// GOOS/GOARCH a pack author uses.
var (
	examplesOnce sync.Once
	exampleBytes map[string][]byte
	exampleErr   error
)

func buildExamples(t *testing.T) map[string][]byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no Go toolchain reachable; compiling an example pack is impossible here")
	}
	examplesOnce.Do(func() {
		directory, err := os.MkdirTemp("", "kf-sdk-examples")
		if err != nil {
			exampleErr = err
			return
		}
		defer os.RemoveAll(directory)
		exampleBytes = map[string][]byte{}
		for _, name := range []string{"echo", "fetch"} {
			wasmPath := filepath.Join(directory, name+".wasm")
			build := exec.Command("go", "build", "-o", wasmPath, "./pkg/sdk/example/"+name)
			build.Dir = moduleRoot(t)
			build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
			if output, err := build.CombinedOutput(); err != nil {
				exampleErr = errors.New("building example/" + name + " failed: " + err.Error() + "\n" + string(output))
				return
			}
			module, err := os.ReadFile(wasmPath)
			if err != nil {
				exampleErr = err
				return
			}
			exampleBytes[name] = module
		}
	})
	if exampleErr != nil {
		t.Fatalf("%v", exampleErr)
	}
	return exampleBytes
}

// TestAPackThatUsesNoCapabilityImportsNoHostFunction is criterion 4's guest
// half: example/echo declares nothing, so the linker drops every host import
// and the module asks the host for nothing but WASI.
func TestAPackThatUsesNoCapabilityImportsNoHostFunction(t *testing.T) {
	module := buildExamples(t)["echo"]
	inspection, err := testModules().Inspect(context.Background(), module, 0)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	for _, imported := range inspection.Imports {
		t.Logf("echo imports %s.%s", imported.Module, imported.Name)
		if imported.Module == sdk.HostModule {
			t.Errorf("a pack that declares no capability imports %s.%s", imported.Module, imported.Name)
		}
	}
	if len(inspection.Imports) == 0 {
		t.Error("echo imports nothing at all, so this test proves nothing about pruning")
	}
}

// TestAPackImportsOnlyWhatItReaches proves the other half: example/fetch
// reaches http and credentials, so it imports those functions and the result
// slots it reads them through — and nothing else, in particular no binary
// function.
func TestAPackImportsOnlyWhatItReaches(t *testing.T) {
	module := buildExamples(t)["fetch"]
	inspection, err := testModules().Inspect(context.Background(), module, 0)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	imported := map[string]bool{}
	for _, entry := range inspection.Imports {
		t.Logf("fetch imports %s.%s", entry.Module, entry.Name)
		if entry.Module == sdk.HostModule {
			imported[entry.Name] = true
		}
	}
	want := map[string]bool{"http_request": true, "credential_field": true, "result_len": true, "result_read": true}
	for name := range want {
		if !imported[name] {
			t.Errorf("fetch does not import %s, which it reaches", name)
		}
	}
	for name := range imported {
		if !want[name] {
			t.Errorf("fetch imports %s, which it never reaches", name)
		}
	}
	// The WASI surface is the guest's own business; what matters here is that
	// no other host module appeared.
	for _, entry := range inspection.Imports {
		if entry.Module != "wasi_snapshot_preview1" && entry.Module != sdk.HostModule {
			t.Errorf("fetch imports %s.%s, which is not a module the host allows", entry.Module, entry.Name)
		}
	}
}
