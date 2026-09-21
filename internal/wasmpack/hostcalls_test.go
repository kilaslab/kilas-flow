package wasmpack_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// The offsets the hand-built guests lay their arguments at. One page of memory
// is 65536 bytes, so 0x00FFFFFF is well outside it and -16 plus a length of 32
// wraps a uint32.
const (
	typeOffset  = 0
	fieldOffset = 16
	metaOffset  = 32
	dstOffset   = 64
	pageCount   = 1
)

// wahaTypeSegment lays the credential type at typeOffset.
func wahaTypeSegment() wasmtest.DataSegment { return dataAt(typeOffset, "wahaApi") }

// wahaFieldSegment lays the field name at fieldOffset.
func wahaFieldSegment() wasmtest.DataSegment { return dataAt(fieldOffset, "baseUrl") }

// credentialArgs are the four i32 arguments credential_field takes.
func credentialArgs() []int { return []int{typeOffset, len("wahaApi"), fieldOffset, len("baseUrl")} }

// TestGuestPointersAreBoundsCheckedOnTheHost is the memory-safety half of the
// ABI: a pointer is an offset into the guest's own memory, and every way of
// getting it wrong is a refusal rather than a panic or a read of bytes the
// guest does not own. The guest traps unless the host answered exactly the code
// it expected, so a run that finishes is a run that answered correctly.
func TestGuestPointersAreBoundsCheckedOnTheHost(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	metadata, err := json.Marshal(sdk.HTTPRequest{Method: "GET", URL: "https://example.test/items"})
	if err != nil {
		t.Fatalf("encoding the request failed: %v", err)
	}

	cases := []struct {
		name string
		args []int
		want int32
		data []wasmtest.DataSegment
	}{
		{
			name: "the request description points outside memory",
			args: []int{0x00FFFFFF, 16, 0, 0},
			want: sdk.ErrInvalid,
		},
		{
			name: "the request description claims a length of 0xFFFFFFFF",
			args: []int{0, -1, 0, 0},
			want: sdk.ErrTooLarge,
		},
		{
			name: "the pointer plus the length wraps a uint32",
			args: []int{-16, 32, 0, 0},
			want: sdk.ErrInvalid,
		},
		{
			name: "the body points outside memory",
			args: []int{metaOffset, len(metadata), 0x00FFFFFF, 16},
			want: sdk.ErrInvalid,
			data: []wasmtest.DataSegment{dataAt(metaOffset, string(metadata))},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			module := guestCall(t, "http_request", testCase.args, testCase.want, testCase.data, pageCount)
			_, _, err := runGuest(t, host, module, wasmpack.Invocation{
				Caps:   wasmpack.Capabilities{HTTP: true},
				Limits: wasmpack.DefaultLimits(),
			})
			if err != nil {
				t.Fatalf("the run failed with %v, which means the host answered something other than %d", err, testCase.want)
			}
		})
	}
}

// TestOversizedGuestArgumentsAreRefused proves a length is checked against its
// cap before anything is read: the guest cannot make the host allocate a
// megabyte of metadata or a gigabyte of payload by claiming one.
func TestOversizedGuestArgumentsAreRefused(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})

	t.Run("a credential type longer than the name cap", func(t *testing.T) {
		module := guestCall(t, "credential_field", []int{0, 300, 0, 1}, sdk.ErrTooLarge, nil, pageCount)
		_, _, err := runGuest(t, host, module, wasmpack.Invocation{
			Caps:   wasmpack.Capabilities{Credentials: []string{"wahaApi"}},
			Limits: wasmpack.DefaultLimits(),
		})
		if err != nil {
			t.Fatalf("the run failed with %v, want the too-large refusal", err)
		}
	})

	t.Run("a payload larger than the payload cap", func(t *testing.T) {
		description, err := json.Marshal(sdk.BinaryWrite{Name: "report.csv"})
		if err != nil {
			t.Fatalf("encoding the description failed: %v", err)
		}
		module := guestCall(t, "binary_write",
			[]int{metaOffset, len(description), dstOffset, -1},
			sdk.ErrTooLarge,
			[]wasmtest.DataSegment{dataAt(metaOffset, string(description))}, pageCount)
		_, _, err = runGuest(t, host, module, wasmpack.Invocation{
			Caps:    wasmpack.Capabilities{BinaryWrite: true},
			Limits:  wasmpack.DefaultLimits(),
			Request: engineRequest(nil),
		})
		if err != nil {
			t.Fatalf("the run failed with %v, want the too-large refusal", err)
		}
	})
}

// TestResultReadHonoursOffsetsAndBounds exercises the whole return path — a
// successful capability call, the length it reports, the copy out of the slot —
// and then the ways a guest can ask for the wrong thing.
func TestResultReadHonoursOffsetsAndBounds(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	encoded, err := json.Marshal("https://waha.example.test")
	if err != nil {
		t.Fatalf("encoding the field failed: %v", err)
	}
	length := int32(len(encoded))

	body := callWith(0, credentialArgs(), length)                                        // credential_field
	body = append(body, callWith(1, []int{sdk.SlotResult}, length)...)                   // result_len(0)
	body = append(body, callWith(2, []int{sdk.SlotResult, 0, dstOffset, 64}, length)...) // result_read(0, 0, dst, 64)
	body = append(body, callWith(2, []int{sdk.SlotResult, int(length) + 1, dstOffset, 64}, sdk.ErrInvalid)...)
	body = append(body, callWith(2, []int{sdk.SlotResult, 0, 0x00FFFFFF, 64}, sdk.ErrInvalid)...)
	body = append(body, callWith(2, []int{7, 0, dstOffset, 64}, sdk.ErrInvalid)...)

	module := guest(t, []string{"credential_field", "result_len", "result_read"}, body,
		[]wasmtest.DataSegment{wahaTypeSegment(), wahaFieldSegment()}, pageCount)

	resolver := &countingResolver{credential: wahaCredential()}
	_, _, err = runGuest(t, host, module, wasmpack.Invocation{
		Caps:    wasmpack.Capabilities{Credentials: []string{"wahaApi"}},
		Limits:  wasmpack.DefaultLimits(),
		IR:      wahaNode(),
		Request: engineRequest(resolver),
	})
	if err != nil {
		t.Fatalf("the run failed with %v, want the slot protocol to answer every case", err)
	}
	if resolver.calls != 1 {
		t.Errorf("the credential was resolved %d times, want once for the whole run", resolver.calls)
	}
}

// TestTheHostCallLimitFailsTheRunWithANamedError proves a pack cannot use the
// host as free computation: the budget is counted before the work, the module
// is stopped, the failure is named, and the process and the host both survive
// it.
func TestTheHostCallLimitFailsTheRunWithANamedError(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	encoded, err := json.Marshal("https://waha.example.test")
	if err != nil {
		t.Fatalf("encoding the field failed: %v", err)
	}

	// The guest calls the host and drops the answer: a guest that checked the
	// refused call's return would trap before the module close takes effect,
	// which is the guest's business and not what this test is about.
	body := []byte{}
	for _, arg := range credentialArgs() {
		body = append(body, wasmtest.I32Const(arg)...)
	}
	body = append(body, wasmtest.Call(0)...)
	body = append(body, wasmtest.Drop()...)
	body = append(body, wasmtest.Br(0)...)
	module := guest(t, []string{"credential_field"}, wasmtest.Loop(body),
		[]wasmtest.DataSegment{wahaTypeSegment(), wahaFieldSegment()}, pageCount)

	limits := wasmpack.DefaultLimits()
	limits.MaxHostCalls = 5
	resolver := &countingResolver{credential: wahaCredential()}
	_, _, err = runGuest(t, host, module, wasmpack.Invocation{
		Caps:    wasmpack.Capabilities{Credentials: []string{"wahaApi"}},
		Limits:  limits,
		IR:      wahaNode(),
		Request: engineRequest(resolver),
	})
	if !errors.Is(err, runcode.ErrHostCallLimit) {
		t.Fatalf("Invoke() error = %v, want the named host-call limit", err)
	}
	if resolver.calls != 1 {
		t.Errorf("the credential was resolved %d times, want once: the cache outlives the refused call", resolver.calls)
	}

	// The host and the process are still usable, which is the half of the
	// promise that matters: one pack's runaway loop fails one node run.
	following := guestCall(t, "credential_field", credentialArgs(), int32(len(encoded)),
		[]wasmtest.DataSegment{wahaTypeSegment(), wahaFieldSegment()}, pageCount)
	_, _, err = runGuest(t, host, following, wasmpack.Invocation{
		Caps:    wasmpack.Capabilities{Credentials: []string{"wahaApi"}},
		Limits:  wasmpack.DefaultLimits(),
		IR:      wahaNode(),
		Request: engineRequest(&countingResolver{credential: wahaCredential()}),
	})
	if err != nil {
		t.Fatalf("the run after the limit failure failed with %v, want the host to keep working", err)
	}
}

// TestAPackThatDeclaresNoCapabilityIsGrantedNoHostFunction is the structural
// half of the capability boundary, seen from the run side: with nothing
// declared there is no host module, so a module that imports one cannot even be
// instantiated.
func TestAPackThatDeclaresNoCapabilityIsGrantedNoHostFunction(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	module := guestCall(t, "http_request", []int{0, 0, 0, 0}, 0, nil, pageCount)
	_, _, err := runGuest(t, host, module, wasmpack.Invocation{Limits: wasmpack.DefaultLimits()})
	if err == nil {
		t.Fatal("a module importing a host function ran with no capability declared")
	}
	if !strings.Contains(err.Error(), sdk.HostModule) {
		t.Errorf("error = %v, want it to name the host module the module tried to import", err)
	}
}

// TestBinaryCapabilitiesRefuseWhenTheRuntimeHasNoBinaryStore covers the case
// that made this a defect worth pinning: engine.Request.Binaries is an
// interface and is nil in a runtime that has no store, so the host call has to
// refuse rather than panic on a nil interface.
func TestBinaryCapabilitiesRefuseWhenTheRuntimeHasNoBinaryStore(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	invocation := wasmpack.Invocation{
		Caps:        wasmpack.Capabilities{BinaryRead: true, BinaryWrite: true},
		Limits:      wasmpack.DefaultLimits(),
		InputBinary: []workflow.BinaryRef{{ID: "bin_1", Size: 5}},
		Request:     engineRequest(nil),
	}

	t.Run("reading a payload the items carry", func(t *testing.T) {
		module := guestCall(t, "binary_read",
			[]int{metaOffset, len("bin_1")}, sdk.ErrDenied,
			[]wasmtest.DataSegment{dataAt(metaOffset, "bin_1")}, pageCount)
		if _, _, err := runGuest(t, host, module, invocation); err != nil {
			t.Fatalf("the run failed with %v, want the named refusal", err)
		}
	})

	t.Run("writing a payload", func(t *testing.T) {
		description, err := json.Marshal(sdk.BinaryWrite{Name: "report.csv"})
		if err != nil {
			t.Fatalf("encoding the description failed: %v", err)
		}
		module := guestCall(t, "binary_write",
			[]int{metaOffset, len(description), dstOffset, len("a,b\n")}, sdk.ErrDenied,
			[]wasmtest.DataSegment{dataAt(metaOffset, string(description)), dataAt(dstOffset, "a,b\n")}, pageCount)
		if _, _, err := runGuest(t, host, module, invocation); err != nil {
			t.Fatalf("the run failed with %v, want the named refusal", err)
		}
	})
}

// TestBinaryNeedsItsCapability proves the payload functions are granted by
// their own capability and not by having any capability: a pack that declares
// http and nothing else cannot read a payload, and one that declares
// binary.read can.
func TestBinaryNeedsItsCapability(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	store := newFakeBinaries(map[string][]byte{"bin_1": []byte("mine")})
	module := guestCall(t, "binary_read", []int{metaOffset, len("bin_1")}, 0,
		[]wasmtest.DataSegment{dataAt(metaOffset, "bin_1")}, pageCount)

	_, _, err := runGuest(t, host, module, wasmpack.Invocation{
		Caps:        wasmpack.Capabilities{HTTP: true},
		Limits:      wasmpack.DefaultLimits(),
		InputBinary: []workflow.BinaryRef{{ID: "bin_1"}},
		Request:     engineRequest(nil, withBinaries(store)),
	})
	if err == nil {
		t.Fatal("a pack that does not declare binary.read read a payload")
	}
	if !strings.Contains(err.Error(), sdk.HostModule) {
		t.Errorf("error = %v, want the missing host module named", err)
	}
	if store.gets != 0 {
		t.Errorf("the store was read %d times, want none", store.gets)
	}
}

// TestBinaryReadIsLimitedToPayloadsTheItemsCarryOrThePackWrote proves a payload
// id is a capability: the store holds two payloads and the run may reach one.
func TestBinaryReadIsLimitedToPayloadsTheItemsCarryOrThePackWrote(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy()})
	store := newFakeBinaries(map[string][]byte{"bin_1": []byte("mine"), "bin_2": []byte("somebody else's")})

	t.Run("a payload the items carry", func(t *testing.T) {
		// binary_read answers with the length of the reference it wrote into
		// slot 0, so the guest asserts against that reference's encoding.
		head, err := json.Marshal(sdk.BinaryRef{ID: "bin_1", Size: 4})
		if err != nil {
			t.Fatalf("encoding the reference failed: %v", err)
		}
		module := guestCall(t, "binary_read", []int{metaOffset, len("bin_1")}, int32(len(head)),
			[]wasmtest.DataSegment{dataAt(metaOffset, "bin_1")}, pageCount)
		if _, _, err := runGuest(t, host, module, wasmpack.Invocation{
			Caps:        wasmpack.Capabilities{BinaryRead: true},
			Limits:      wasmpack.DefaultLimits(),
			InputBinary: []workflow.BinaryRef{{ID: "bin_1"}},
			Request:     engineRequest(nil, withBinaries(store)),
		}); err != nil {
			t.Fatalf("reading a payload the item carried failed with %v", err)
		}
		if store.gets != 1 {
			t.Errorf("the store was read %d times, want once", store.gets)
		}
	})

	t.Run("a payload the run never saw", func(t *testing.T) {
		before := store.gets
		module := guestCall(t, "binary_read", []int{metaOffset, len("bin_2")}, sdk.ErrDenied,
			[]wasmtest.DataSegment{dataAt(metaOffset, "bin_2")}, pageCount)
		_, _, err := runGuest(t, host, module, wasmpack.Invocation{
			Caps:        wasmpack.Capabilities{BinaryRead: true},
			Limits:      wasmpack.DefaultLimits(),
			InputBinary: []workflow.BinaryRef{{ID: "bin_1"}},
			Request:     engineRequest(nil, withBinaries(store)),
		})
		if err != nil {
			t.Fatalf("the run failed with %v, want the named refusal", err)
		}
		if store.gets != before {
			t.Error("the store was read for a payload the run may not reach")
		}
	})
}
