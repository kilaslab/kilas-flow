package wasmpack

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// install registers a binding's host functions into a fresh runtime, the way
// the sandbox does, and returns the module it created (nil when the binding
// installed nothing).
//
// It is an internal test because what it inspects — the host module wazero was
// given — is not reachable through Invoke, which closes the runtime when the
// run ends.
func install(t *testing.T, caps Capabilities) (wazero.Runtime, api.Module) {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })

	binding := &binding{
		host:   NewHost(HostDeps{}),
		caps:   caps,
		limits: DefaultLimits(),
		inv:    Invocation{},
		run:    newRunState(Invocation{}),
	}
	// The budget is not exercised here; a zero CallState never refuses a call
	// it is asked about because nothing calls through it.
	if err := binding.Install(ctx, runtime, &runcode.CallState{}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	return runtime, runtime.Module(sdk.HostModule)
}

// TestTheHostModuleExportsExactlyTheDeclaredABI proves the registered functions
// are the ABI table and nothing else: same names, same arity, i32 in and one
// i32 out. A row added to the table without an implementation fails here rather
// than at a pack's first run.
func TestTheHostModuleExportsExactlyTheDeclaredABI(t *testing.T) {
	_, host := install(t, Capabilities{
		HTTP: true, Credentials: []string{"httpHeaderAuth"}, BinaryRead: true, BinaryWrite: true,
	})
	if host == nil {
		t.Fatal("a pack that declares capabilities got no host module")
	}
	definitions := host.ExportedFunctionDefinitions()
	if len(definitions) != len(sdk.Functions) {
		t.Fatalf("the host module exports %d functions, want the %d in the ABI", len(definitions), len(sdk.Functions))
	}
	for _, function := range sdk.Functions {
		definition, found := definitions[function.Name]
		if !found {
			t.Errorf("%s is in the ABI but not in the host module", function.Name)
			continue
		}
		params := definition.ParamTypes()
		if len(params) != len(function.Params) {
			t.Errorf("%s takes %d parameters, want %d", function.Name, len(params), len(function.Params))
		}
		for index, param := range params {
			if param != api.ValueTypeI32 {
				t.Errorf("%s parameter %d is %v, want i32", function.Name, index, param)
			}
		}
		results := definition.ResultTypes()
		if len(results) != 1 || results[0] != api.ValueTypeI32 {
			t.Errorf("%s returns %v, want one i32", function.Name, results)
		}
	}
	// The table and the implementation table are the same set of names.
	for name := range hostCalls {
		if _, found := sdk.FunctionNamed(name); !found {
			t.Errorf("%s has an implementation but is not in the ABI table", name)
		}
	}
	for _, function := range sdk.Functions {
		if _, found := hostCalls[function.Name]; !found {
			t.Errorf("%s is in the ABI table but has no implementation", function.Name)
		}
	}
}

// TestEachCapabilityGrantsOnlyItsOwnFunctions walks the capability set one
// declaration at a time, so a capability that quietly grants more than it says
// is a failure rather than a surprise.
func TestEachCapabilityGrantsOnlyItsOwnFunctions(t *testing.T) {
	cases := []struct {
		name string
		caps Capabilities
		want []string
	}{
		{
			name: "nothing declared",
			caps: Capabilities{},
			want: nil,
		},
		{
			name: "http",
			caps: Capabilities{HTTP: true},
			want: []string{"http_request", "result_len", "result_read"},
		},
		{
			name: "credentials",
			caps: Capabilities{Credentials: []string{"httpHeaderAuth"}},
			want: []string{"credential_field", "result_len", "result_read"},
		},
		{
			name: "binary.read",
			caps: Capabilities{BinaryRead: true},
			want: []string{"binary_read", "result_len", "result_read"},
		},
		{
			name: "binary.write",
			caps: Capabilities{BinaryWrite: true},
			want: []string{"binary_write", "result_len", "result_read"},
		},
		{
			name: "everything",
			caps: Capabilities{HTTP: true, Credentials: []string{"httpHeaderAuth"}, BinaryRead: true, BinaryWrite: true},
			want: []string{"http_request", "credential_field", "binary_read", "binary_write", "result_len", "result_read"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, host := install(t, testCase.caps)
			if len(testCase.want) == 0 {
				if host != nil {
					t.Fatalf("a pack that declares nothing got a host module exporting %v", host.ExportedFunctionDefinitions())
				}
				return
			}
			if host == nil {
				t.Fatal("the declared capabilities produced no host module")
			}
			definitions := host.ExportedFunctionDefinitions()
			if len(definitions) != len(testCase.want) {
				t.Fatalf("the host module exports %v, want %v", namesOf(definitions), testCase.want)
			}
			for _, name := range testCase.want {
				if _, found := definitions[name]; !found {
					t.Errorf("the host module does not export %s", name)
				}
			}
		})
	}
}

func namesOf(definitions map[string]api.FunctionDefinition) []string {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	return names
}
