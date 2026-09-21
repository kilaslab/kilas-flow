package wasmpack_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// TestCapabilityNamesReportWhatAManifestAsksFor is what an operator reads: one
// line per declared thing, with the credential types spelled out rather than
// summarised as "credentials".
func TestCapabilityNamesReportWhatAManifestAsksFor(t *testing.T) {
	caps := wasmpack.Capabilities{
		HTTP:        true,
		Credentials: []string{"wahaApi", "telegramApi"},
		BinaryWrite: true,
	}
	want := []string{"binary.write", "credentials:telegramApi", "credentials:wahaApi", "http"}
	got := caps.Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
	if names := (wasmpack.Capabilities{}).Names(); len(names) != 0 {
		t.Errorf("Names() = %v for a pack that declares nothing, want none", names)
	}
}

// TestTheGrantedSetFollowsTheTable proves the granted functions are the table's
// rows in the table's order, which is what makes the registration order and the
// audit agree.
func TestTheGrantedSetFollowsTheTable(t *testing.T) {
	caps := wasmpack.Capabilities{HTTP: true, Credentials: []string{"wahaApi"}, BinaryRead: true, BinaryWrite: true}
	granted := caps.GrantedFunctions()
	if len(granted) != len(sdk.Functions) {
		t.Fatalf("GrantedFunctions() returned %d rows, want the whole table (%d)", len(granted), len(sdk.Functions))
	}
	for index, function := range granted {
		if function.Name != sdk.Functions[index].Name {
			t.Errorf("row %d is %s, want %s", index, function.Name, sdk.Functions[index].Name)
		}
	}
	// A capability that is not declared grants nothing but the result slots.
	partial := wasmpack.Capabilities{BinaryRead: true}.GrantedFunctions()
	for _, function := range partial {
		if function.Capability == sdk.CapHTTP || function.Capability == sdk.CapBinaryWrite || function.Capability == sdk.CapCredentials {
			t.Errorf("%s was granted by a manifest that does not declare it", function.Name)
		}
	}
}

// TestLimitsAreBoundedByTheCeilings proves a manifest can ask for less than the
// ceiling and never more, in every dimension.
func TestLimitsAreBoundedByTheCeilings(t *testing.T) {
	if err := wasmpack.DefaultLimits().Validate(); err != nil {
		t.Errorf("the default limits are invalid: %v", err)
	}
	if err := wasmpack.Ceilings().Validate(); err != nil {
		t.Errorf("the ceilings are invalid: %v", err)
	}

	cases := []struct {
		name   string
		limits wasmpack.Limits
		want   string
	}{
		{"no time limit", wasmpack.Limits{Timeout: 0, MemoryPages: 1, MaxOutputBytes: 1, MaxHostCalls: 1}, "time limit"},
		{"a time limit above the ceiling", wasmpack.Limits{Timeout: 11 * time.Minute, MemoryPages: 1, MaxOutputBytes: 1, MaxHostCalls: 1}, "ceiling"},
		{"a memory limit above the ceiling", wasmpack.Limits{Timeout: time.Second, MemoryPages: 4097, MaxOutputBytes: 1, MaxHostCalls: 1}, "ceiling"},
		{"an output limit above the ceiling", wasmpack.Limits{Timeout: time.Second, MemoryPages: 1, MaxOutputBytes: 65 << 20, MaxHostCalls: 1}, "ceiling"},
		{"a host-call limit above the ceiling", wasmpack.Limits{Timeout: time.Second, MemoryPages: 1, MaxOutputBytes: 1, MaxHostCalls: 10001}, "ceiling"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.limits.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %+v", testCase.limits)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("Validate() error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// TestTheAuditRefusesAModuleThatReachesPastItsDeclaration is the load-time half
// of the capability boundary: the refusal names the function and the capability
// the manifest is missing, so an operator knows what to ask the author for.
func TestTheAuditRefusesAModuleThatReachesPastItsDeclaration(t *testing.T) {
	module := guestCall(t, "http_request", []int{0, 0, 0, 0}, 0, nil, pageCount)

	report, err := wasmpack.Audit(context.Background(), testModules(), module, wasmpack.Capabilities{}, wasmpack.DefaultLimits())
	if err == nil {
		t.Fatal("Audit() accepted a module importing a capability the pack does not declare")
	}
	if !strings.Contains(err.Error(), "http_request") || !strings.Contains(err.Error(), string(sdk.CapHTTP)) {
		t.Errorf("Audit() error = %v, want it to name the function and the missing capability", err)
	}
	if len(report.Imported) != 0 {
		t.Errorf("report.Imported = %v, want nothing reported as allowed", report.Imported)
	}
}

// TestTheAuditRefusesAnUnknownModuleOrFunction proves a pack cannot import
// anything that is not WASI or this ABI: not another host module, not a
// function that does not exist in this ABI version.
func TestTheAuditRefusesAnUnknownModuleOrFunction(t *testing.T) {
	cases := []struct {
		name   string
		module wasmtest.Import
		want   string
	}{
		{
			name:   "another module",
			module: wasmtest.Import{Module: "env", Name: "abort", Params: nil, Results: nil},
			want:   `"env"`,
		},
		{
			name:   "an older host module",
			module: wasmtest.Import{Module: "kilasflow_v0", Name: "http_request", Params: nil, Results: nil},
			want:   `"kilasflow_v0"`,
		},
		{
			name:   "a function this ABI does not have",
			module: wasmtest.Import{Module: sdk.HostModule, Name: "exec", Params: nil, Results: nil},
			want:   "not part of ABI",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			module := wasmtest.Build([]wasmtest.Import{testCase.module}, []byte{}, pageCount, nil)
			caps := wasmpack.Capabilities{HTTP: true, Credentials: []string{"wahaApi"}, BinaryRead: true, BinaryWrite: true}
			_, err := wasmpack.Audit(context.Background(), testModules(), module, caps, wasmpack.DefaultLimits())
			if err == nil {
				t.Fatalf("Audit() accepted an import from %s", testCase.module.Module)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("Audit() error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// TestTheAuditAcceptsWhatTheManifestDeclaresAndReportsIt proves the audit's
// happy path, and that its report shows declared and imported separately — the
// only way an operator can see a pack asking for more than it uses.
func TestTheAuditAcceptsWhatTheManifestDeclaresAndReportsIt(t *testing.T) {
	// A module that imports only the result slots, while the manifest declares
	// http as well.
	module := guestCall(t, "result_len", []int{sdk.SlotResult}, 0, nil, pageCount)
	caps := wasmpack.Capabilities{HTTP: true}
	report, err := wasmpack.Audit(context.Background(), testModules(), module, caps, wasmpack.DefaultLimits())
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(report.Declared) != 1 || report.Declared[0] != string(sdk.CapHTTP) {
		t.Errorf("report.Declared = %v, want just http", report.Declared)
	}
	if len(report.Imported) != 1 || report.Imported[0] != "result_len" {
		t.Errorf("report.Imported = %v, want just result_len", report.Imported)
	}
	if !contains(report.Exports, "_start") || !contains(report.Exports, "memory") {
		t.Errorf("report.Exports = %v, want a command module's exports", report.Exports)
	}
}

// TestOnlyDeclaredHTTPCapableCredentialTypesCanBeNamed proves the structural
// half of the internal-database guard: a type that signs an HTTP request may be
// declared, and one that does not — a database credential — is refused at
// installation, so no run ever gets the chance to name it.
func TestOnlyDeclaredHTTPCapableCredentialTypesCanBeNamed(t *testing.T) {
	module := wasmtest.MinimalModule("hello")

	if _, err := wasmpack.Audit(context.Background(), testModules(), module,
		wasmpack.Capabilities{Credentials: []string{"wahaApi"}}, wasmpack.DefaultLimits()); err != nil {
		t.Errorf("Audit() refused a credential type that can authenticate a request: %v", err)
	}

	_, err := wasmpack.Audit(context.Background(), testModules(), module,
		wasmpack.Capabilities{Credentials: []string{"mysql"}}, wasmpack.DefaultLimits())
	if err == nil {
		t.Fatal("Audit() accepted a credential type that cannot authenticate an HTTP request")
	}
	if !strings.Contains(err.Error(), "mysql") {
		t.Errorf("Audit() error = %v, want it to name the credential type", err)
	}
}

// TestTheAuditRefusesAModuleThatCannotStartInsideItsMemoryLimit proves the
// audit meets the same refusal a run would, and says which limit is at fault
// rather than quoting a sentence that renders both sizes the same.
func TestTheAuditRefusesAModuleThatCannotStartInsideItsMemoryLimit(t *testing.T) {
	module := wasmtest.Build(nil, []byte{}, 200, nil)
	limits := wasmpack.DefaultLimits()
	limits.MemoryPages = 32
	_, err := wasmpack.Audit(context.Background(), testModules(), module, wasmpack.Capabilities{}, limits)
	if err == nil {
		t.Fatal("Audit() accepted a module that cannot fit its memory limit")
	}
	if !strings.Contains(err.Error(), "32-page") {
		t.Errorf("Audit() error = %v, want it to state the configured limit", err)
	}
}

// TestTheAuditReportsAModuleThatIsNotCompilable proves a corrupt artifact is an
// installation error rather than a run-time trap.
func TestTheAuditReportsAModuleThatIsNotCompilable(t *testing.T) {
	_, err := wasmpack.Audit(context.Background(), testModules(), []byte("not a module"),
		wasmpack.Capabilities{}, wasmpack.DefaultLimits())
	if err == nil {
		t.Fatal("Audit() accepted bytes that are not a module")
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("Audit() error = %v, want it to say the module could not be read", err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, entry := range haystack {
		if entry == needle {
			return true
		}
	}
	return false
}
