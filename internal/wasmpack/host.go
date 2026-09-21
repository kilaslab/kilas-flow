package wasmpack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// The caps on what a guest may hand the host in one call.
//
// They are checked before a byte is read, so a guest cannot make the host
// allocate its way out of memory by claiming a length it does not have, and
// they are deliberately generous enough that no honest pack meets them: a
// request description of 64 KiB, a request body of 8 MiB, a URL of 8 KiB, a
// payload of 16 MiB.
const (
	maxMetadata    = 64 << 10
	maxRequestBody = 8 << 20
	maxURL         = 8 << 10
	maxHeaders     = 64
	maxName        = 256
	maxBinary      = 16 << 20
)

// packSubject is who a failure message is about when the caller did not name
// the node.
const packSubject = "the pack"

// Host runs pack modules under a deployment's egress policy.
//
// It holds no per-run state: everything a run needs — the credentials it may
// name, the payloads it may read, the slots it answers through — is created per
// invocation and reaches the module only through the host functions that
// invocation registered.
type Host struct {
	policy safehttp.Policy
	// following follows redirects up to the policy's ceiling; stopping hands
	// the redirect response back to the pack, which is what the HTTP node does
	// by default and what a pack gets unless it asks otherwise.
	following *http.Client
	stopping  *http.Client
	runner    *runcode.Runner
}

// HostDeps is what a host needs from the deployment it runs in.
type HostDeps struct {
	// Policy bounds every request a pack makes. It is the same policy the HTTP
	// node is built with, so a pack cannot reach anywhere a workflow could not.
	Policy safehttp.Policy
	// Modules is the deployment's translation cache, so a pack's module is
	// translated once per process rather than once per run. Nil means every run
	// translates again, which is correct and slower.
	Modules *runcode.ModuleCache
}

// NewHost builds a host for one deployment's policy.
func NewHost(deps HostDeps) *Host {
	following := safehttp.NewClient(deps.Policy)
	stopping := safehttp.NewClient(deps.Policy)
	stopping.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Host{
		policy:    deps.Policy,
		following: following,
		stopping:  stopping,
		// The artifact cache is unused — a pack arrives compiled — but the
		// runner needs one, and the translation cache is the deployment's.
		runner: runcode.NewRunner(nil, nil, deps.Modules, runcode.DefaultLimits()),
	}
}

// Invocation is one pack run.
type Invocation struct {
	// Module is the pack's compiled artifact.
	Module []byte
	// Caps are what the pack's manifest declares. Nothing outside them is
	// reachable: the host registers only what they grant.
	Caps Capabilities
	// Limits bound the run. A zero Limits is DefaultLimits.
	Limits Limits
	// IR is the node that is running, which is where the credentials it
	// attached are named.
	IR workflow.IRNode
	// Request is the runtime's per-execution input: the credential resolver,
	// the payload store and the ambient context.
	Request engine.Request
	// Stdin is the invocation envelope the host wrote.
	Stdin []byte
	// InputBinary are the payload references the input items carry. They, plus
	// what the run itself writes, are exactly what binary_read can reach.
	InputBinary []workflow.BinaryRef
	// Subject names the node in failure messages. Empty means "the pack".
	Subject string
}

// Effects is what one run changed outside its own linear memory.
//
// It is returned even when the run failed, because a payload the pack stored
// before it was stopped is still stored: the executor decides what to do with
// it, and hiding it would leave a reference nobody can account for.
type Effects struct {
	// Writes are the payloads the pack stored, in the order it stored them.
	Writes []workflow.BinaryRef
}

// Invoke runs one pack module against one invocation.
func (host *Host) Invoke(ctx context.Context, inv Invocation) (runcode.Outcome, *Effects, error) {
	limits := inv.Limits.withDefaults()
	if err := limits.Validate(); err != nil {
		return runcode.Outcome{}, &Effects{}, fmt.Errorf("pack limits: %w", err)
	}
	subject := inv.Subject
	if strings.TrimSpace(subject) == "" {
		subject = packSubject
	}

	run := newRunState(inv)
	binding := &binding{host: host, caps: inv.Caps, limits: limits, inv: inv, run: run}

	outcome, err := host.runner.Sandbox(ctx, inv.Module, runcode.Call{
		Stdin: inv.Stdin,
		Limits: runcode.Limits{
			Timeout:        limits.Timeout,
			MemoryPages:    limits.MemoryPages,
			MaxOutputBytes: limits.MaxOutputBytes,
			MaxHostCalls:   limits.MaxHostCalls,
		},
		Host:    binding,
		Subject: subject,
	})
	return outcome, run.effects(), err
}

// binding is one run's host module.
//
// It is created per invocation and holds that run's slots, its credential
// cache, the payloads it may read and its host-call accounting, so two runs
// cannot see each other's results even when they run at the same moment.
type binding struct {
	host   *Host
	caps   Capabilities
	limits Limits
	inv    Invocation
	run    *runState
	// calls is the sandbox's host-call budget, filled at install time.
	calls *runcode.CallState
}

// Install registers the host functions this pack's capabilities grant.
//
// It implements runcode.HostBinding, so the sandbox installs it into the one
// runtime that will run this module, after WASI and before the module is
// compiled. A pack that declares nothing gets no host module at all: its
// imports then resolve to nothing, which is the same refusal the audit makes at
// load time, arrived at from the other side.
func (b *binding) Install(ctx context.Context, runtime wazero.Runtime, state *runcode.CallState) error {
	b.calls = state
	granted := b.caps.GrantedFunctions()
	if len(granted) == 0 {
		return nil
	}
	builder := runtime.NewHostModuleBuilder(sdk.HostModule)
	for _, function := range granted {
		impl, found := hostCalls[function.Name]
		if !found {
			return fmt.Errorf("no host implementation for %s, so the ABI and this host have drifted apart", function.Name)
		}
		builder.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, module api.Module, stack []uint64) {
				stack[0] = api.EncodeI32(impl(b, ctx, module, stack))
			}), paramsOf(function), []api.ValueType{api.ValueTypeI32}).
			Export(function.Name)
	}
	if _, err := builder.Instantiate(ctx); err != nil {
		return fmt.Errorf("instantiating %s: %w", sdk.HostModule, err)
	}
	return nil
}

// hostCalls maps an ABI function name to its implementation.
//
// The names here are the ABI's: TestTheHostModuleExportsExactlyTheDeclaredABI
// proves this table and sdk.Functions carry the same names and arities, so a
// row added to the ABI cannot quietly go unimplemented.
var hostCalls = map[string]func(*binding, context.Context, api.Module, []uint64) int32{
	"http_request":     (*binding).httpRequest,
	"credential_field": (*binding).credentialField,
	"binary_read":      (*binding).binaryRead,
	"binary_write":     (*binding).binaryWrite,
	"result_len":       (*binding).resultLen,
	"result_read":      (*binding).resultRead,
}

// paramsOf renders an ABI row's parameters as the i32s v1 is made of.
func paramsOf(function sdk.Function) []api.ValueType {
	params := make([]api.ValueType, len(function.Params))
	for index := range params {
		params[index] = api.ValueTypeI32
	}
	return params
}

// runState is everything one run owns.
type runState struct {
	mu sync.Mutex
	// slots are the two result slots, cleared before every capability call.
	slots [2][]byte
	// writes are the payloads this run stored, in order.
	writes []workflow.BinaryRef
	// readable is the set of payload ids the run may read: the input items'
	// references plus what it wrote itself.
	readable map[string]bool
	// hostDeadline is when the host's share of the wall clock runs out. It is
	// set on the run's first host call rather than at the start, because
	// translating and instantiating the module are the host's work and must not
	// be charged to the pack's budget — the sandbox's own limit starts where
	// the guest does, and this one bounds the host's I/O from the first call.
	hostDeadline time.Time
	// credentials caches what a credential type resolved to, so a pack that
	// reads two fields of one credential resolves it once.
	credentials map[string]engine.Credential
}

func newRunState(inv Invocation) *runState {
	readable := make(map[string]bool, len(inv.InputBinary))
	for _, ref := range inv.InputBinary {
		if ref.ID != "" {
			readable[ref.ID] = true
		}
	}
	return &runState{readable: readable, credentials: map[string]engine.Credential{}}
}

// clear empties both slots, which every capability call does before it does
// anything else: a failure must never be read as the previous call's success.
func (state *runState) clear() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.slots[0], state.slots[1] = nil, nil
}

// setSlots fills both slots.
func (state *runState) setSlots(result, body []byte) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.slots[0], state.slots[1] = result, body
}

// slot reads one slot for result_len and result_read.
func (state *runState) slot(index int32) ([]byte, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if index < 0 || int(index) >= len(state.slots) {
		return nil, false
	}
	return state.slots[index], true
}

// recordWrite adds a stored payload to the run's effects and to what it may
// read back.
func (state *runState) recordWrite(ref workflow.BinaryRef) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.writes = append(state.writes, ref)
	if ref.ID != "" {
		state.readable[ref.ID] = true
	}
}

// canRead reports whether the run may read one payload.
func (state *runState) canRead(id string) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.readable[id]
}

// effects renders what the run changed.
func (state *runState) effects() *Effects {
	state.mu.Lock()
	defer state.mu.Unlock()
	return &Effects{Writes: append([]workflow.BinaryRef(nil), state.writes...)}
}

// cachedCredential returns what one credential type resolved to, resolving it
// the first time and reusing that answer for the rest of the run.
func (state *runState) cachedCredential(credentialType string, resolve func() (engine.Credential, bool, error)) (engine.Credential, bool, error) {
	state.mu.Lock()
	cached, found := state.credentials[credentialType]
	state.mu.Unlock()
	if found {
		return cached, true, nil
	}
	credential, attached, err := resolve()
	if err != nil || !attached {
		return engine.Credential{}, attached, err
	}
	state.mu.Lock()
	state.credentials[credentialType] = credential
	state.mu.Unlock()
	return credential, true, nil
}

// budget returns the deadline the host's own work runs under, starting the
// clock on the run's first host call.
func (state *runState) budget(timeout time.Duration) time.Time {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.hostDeadline.IsZero() {
		state.hostDeadline = time.Now().Add(timeout)
	}
	return state.hostDeadline
}

// expired reports whether the host's share of the wall clock is over.
func (state *runState) expired(timeout time.Duration) bool {
	return !state.budget(timeout).After(time.Now())
}

// readGuest copies bytes out of the guest's linear memory.
//
// It returns the bytes and a wire code: 0 when it read them, or the refusal to
// answer with. Nothing here trusts a number the guest supplied — the length is
// checked against the cap before anything is read, and the pointer and length
// are checked against the memory's own size, so an out-of-range pointer, a
// length of 0xFFFFFFFF and a pointer-plus-length that wraps all end as a
// refusal rather than as a panic or a read of somebody else's bytes.
func readGuest(module api.Module, pointer, length, limit uint32) ([]byte, int32) {
	if length == 0 {
		return nil, 0
	}
	if length > limit {
		return nil, sdk.ErrTooLarge
	}
	memory := module.Memory()
	if memory == nil {
		return nil, sdk.ErrInvalid
	}
	if uint64(pointer)+uint64(length) > uint64(memory.Size()) {
		return nil, sdk.ErrInvalid
	}
	contents, ok := memory.Read(pointer, length)
	if !ok {
		return nil, sdk.ErrInvalid
	}
	// Copied, not borrowed: the guest keeps running and may rewrite its memory
	// while the host is still using these bytes.
	return append([]byte(nil), contents...), 0
}

// writeGuest copies bytes into the guest's linear memory.
func writeGuest(module api.Module, pointer uint32, contents []byte) int32 {
	if len(contents) == 0 {
		return 0
	}
	memory := module.Memory()
	if memory == nil {
		return sdk.ErrInvalid
	}
	if uint64(pointer)+uint64(len(contents)) > uint64(memory.Size()) {
		return sdk.ErrInvalid
	}
	if !memory.Write(pointer, contents) {
		return sdk.ErrInvalid
	}
	return 0
}

// readGuestText copies a string argument out of the guest.
func readGuestText(module api.Module, pointer, length, limit uint32) (string, int32) {
	contents, code := readGuest(module, pointer, length, limit)
	if code != 0 {
		return "", code
	}
	return string(contents), 0
}

// succeed fills the slots and answers with the length of the metadata, which is
// what a successful capability call returns.
func (b *binding) succeed(result, body []byte) int32 {
	b.run.setSlots(result, body)
	return int32(len(result))
}

// fail writes a refusal into the result slot and returns its code, so the guest
// reads the host's own words rather than a generic failure.
func (b *binding) fail(code int32, message string) int32 {
	encoded, err := json.Marshal(sdk.HostError{Code: sdk.ErrorCodeName(code), Message: message})
	if err != nil {
		encoded = nil
	}
	b.run.setSlots(encoded, nil)
	return code
}

// deadlineEnded ends the run when the pack's wall clock ran out while the host
// was working.
//
// Closing the module with wazero's deadline exit code is what makes the
// sandbox report the run as having hit its time limit, rather than reporting
// whatever the guest did next after a failed call. The run is over either way —
// the sandbox's own deadline has already passed — and this only makes the
// reason the honest one.
func (b *binding) deadlineEnded(ctx context.Context, module api.Module) int32 {
	// The close must not be cancelled by the context that just expired, or it
	// would not happen at all.
	_ = module.CloseWithExitCode(context.WithoutCancel(ctx), sys.ExitCodeDeadlineExceeded)
	return 0
}

// resultLen is the host side of sdk's result_len: how many bytes a slot holds.
//
// The slots are the whole return path for a body too large to be JSON-escaped
// into a return value, so the guest sizes its buffer from here and copies with
// result_read.
func (b *binding) resultLen(ctx context.Context, module api.Module, stack []uint64) int32 {
	if !b.calls.Enter(ctx, module) {
		return 0
	}
	contents, ok := b.run.slot(api.DecodeI32(stack[0]))
	if !ok {
		return b.fail(sdk.ErrInvalid, "there is no such result slot")
	}
	return int32(len(contents))
}

// resultRead is the host side of sdk's result_read: copy bytes out of a slot
// into the guest's memory.
//
// Every number is the guest's, so every number is checked: the slot must exist,
// the offset must be inside it, and the destination is bounds-checked against
// the guest's own memory before anything is written.
func (b *binding) resultRead(ctx context.Context, module api.Module, stack []uint64) int32 {
	if !b.calls.Enter(ctx, module) {
		return 0
	}
	contents, ok := b.run.slot(api.DecodeI32(stack[0]))
	if !ok {
		return b.fail(sdk.ErrInvalid, "there is no such result slot")
	}
	offset, capacity := api.DecodeI32(stack[1]), api.DecodeU32(stack[3])
	if offset < 0 || int64(offset) > int64(len(contents)) {
		return b.fail(sdk.ErrInvalid, "the offset is outside the result slot")
	}
	remaining := contents[offset:]
	if uint32(len(remaining)) < capacity {
		capacity = uint32(len(remaining))
	}
	if code := writeGuest(module, api.DecodeU32(stack[2]), remaining[:capacity]); code != 0 {
		return b.fail(code, "the destination is outside the module's memory")
	}
	return int32(capacity)
}

// decodeStrict decodes a guest's metadata, refusing a field the ABI does not
// define.
//
// Strictness is the point: a field the host silently ignored would be a pack
// author's mistake that only shows up as a request that did not do what they
// wrote, and it would let a pack believe it had set something it had not.
func decodeStrict(contents []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
