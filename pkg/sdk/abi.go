//go:generate go run ./internal/abigen/cmd -o host_wasip1.go

package sdk

// This file is the one definition of the pack host ABI.
//
// Everything else follows from it: the guest's imports are generated from
// Functions (internal/abigen writes host_wasip1.go), the host registers exactly
// the rows its capabilities grant, and the audit that decides what a pack may
// import reads the same table. A row is added here, the bindings are
// regenerated, and both sides move together.
//
// v1 is deliberately the smallest thing that can carry a JSON document across
// the boundary: i32 parameters and one i32 result. A pointer is an offset into
// the guest's linear memory and a length is a byte count beside it, so the host
// validates both before reading a single byte, and a guest cannot name memory
// it does not have. Anything richer — an i64, a float, a struct — would need
// ABI v2.

// HostModule is the WebAssembly module name the host instantiates for a pack.
//
// The version is in the name so a pack built against a different ABI cannot be
// silently satisfied by today's host functions: it imports a module that is not
// there and fails at instantiation, loudly.
const HostModule = "kilasflow_v1"

// ABIVersion is the value the invocation envelope's `abi` field carries, and
// what a pack manifest declares. It moves when the shape of any wire type or
// function in this file changes.
const ABIVersion = "v1"

// Capability names one thing a pack may be granted.
//
// Capabilities are what an operator reads and approves: a manifest declares
// them, the host registers only the functions they grant, and a function the
// pack's capabilities do not cover is refused at load.
type Capability string

// The capabilities v1 defines.
const (
	// CapHTTP is outbound HTTP through the deployment's SSRF policy.
	CapHTTP Capability = "http"
	// CapCredentials is reading a named credential's non-secret fields and
	// naming a credential for the host to apply to a request.
	CapCredentials Capability = "credentials"
	// CapBinaryRead is reading a payload the input items carry or the pack
	// itself wrote.
	CapBinaryRead Capability = "binary.read"
	// CapBinaryWrite is storing a payload and handing its reference back.
	CapBinaryWrite Capability = "binary.write"
	// CapResult is reading a capability call's result out of the host's slots.
	// It is not declared: it is granted whenever any other capability is,
	// because every other capability answers through the same slots.
	CapResult Capability = "result"
)

// The slots one capability call writes into. Both are cleared before every
// call, so a failure can never be read as the previous call's success.
const (
	// SlotResult holds the call's metadata: the response head, a credential
	// field's value, a payload reference, or an error.
	SlotResult = 0
	// SlotBody holds the bytes: an HTTP response body or a payload read.
	SlotBody = 1
)

// The return codes of a capability call.
//
// A call returns the length of the metadata now in SlotResult when it
// succeeded, and one of these when it did not. They are negative so that a
// length and a failure can never be confused, and they are stable numbers
// rather than strings because the guest switches on them.
const (
	// ErrInvalid is a malformed argument: a bad pointer or length, metadata
	// that is not the JSON it claims to be, a slot that does not exist.
	ErrInvalid int32 = -1
	// ErrDenied is a refusal: a capability the pack does not hold, a
	// credential it did not declare or that is not attached, a payload it may
	// not read, a secret field.
	ErrDenied int32 = -2
	// ErrBlocked is the egress policy refusing the target.
	ErrBlocked int32 = -3
	// ErrFailed is a network, upstream or store failure.
	ErrFailed int32 = -4
	// ErrTooLarge is an argument or a result beyond its cap.
	ErrTooLarge int32 = -5
	// ErrNotFound is a named thing that does not exist.
	ErrNotFound int32 = -6
)

// The HostError.Code strings, one per return code, so the guest's error values
// and the host's refusals are the same vocabulary on both sides.
const (
	CodeInvalid  = "invalid"
	CodeDenied   = "denied"
	CodeBlocked  = "blocked"
	CodeFailed   = "failed"
	CodeTooLarge = "too_large"
	CodeNotFound = "not_found"
)

// ErrorCodeName is the HostError.Code string for a return code.
//
// An unknown code reads as failed: the guest is looking at a host that answered
// something this SDK does not know, and "the host failed" is the only honest
// thing left to say about it.
func ErrorCodeName(code int32) string {
	switch code {
	case ErrInvalid:
		return CodeInvalid
	case ErrDenied:
		return CodeDenied
	case ErrBlocked:
		return CodeBlocked
	case ErrTooLarge:
		return CodeTooLarge
	case ErrNotFound:
		return CodeNotFound
	default:
		return CodeFailed
	}
}

// Function is one host function a pack may import.
type Function struct {
	// Name is the export name inside HostModule, and the name a module's
	// imports are audited against.
	Name string
	// Capability is what grants it.
	Capability Capability
	// Params names the parameters in order. v1 is i32-only, so this is arity
	// and documentation together.
	Params []string
	// Doc is the one-line description the generated bindings and the audit
	// report carry.
	Doc string
}

// Functions is the ABI, in the order the guest's imports are declared.
var Functions = []Function{
	{
		Name:       "http_request",
		Capability: CapHTTP,
		Params:     []string{"meta_ptr", "meta_len", "body_ptr", "body_len"},
		Doc:        "sends one HTTP request and writes the response head and body into the result slots",
	},
	{
		Name:       "credential_field",
		Capability: CapCredentials,
		Params:     []string{"type_ptr", "type_len", "field_ptr", "field_len"},
		Doc:        "reads one non-secret field of a credential the node attached",
	},
	{
		Name:       "binary_read",
		Capability: CapBinaryRead,
		Params:     []string{"id_ptr", "id_len"},
		Doc:        "reads a payload the input items carry or this run wrote",
	},
	{
		Name:       "binary_write",
		Capability: CapBinaryWrite,
		Params:     []string{"meta_ptr", "meta_len", "data_ptr", "data_len"},
		Doc:        "stores a payload and writes its reference into the result slot",
	},
	{
		Name:       "result_len",
		Capability: CapResult,
		Params:     []string{"slot"},
		Doc:        "reports how many bytes a result slot holds",
	},
	{
		Name:       "result_read",
		Capability: CapResult,
		Params:     []string{"slot", "offset", "dst_ptr", "dst_cap"},
		Doc:        "copies bytes out of a result slot into the guest's memory",
	},
}

// FunctionNamed returns the ABI row for a function name.
func FunctionNamed(name string) (Function, bool) {
	for _, function := range Functions {
		if function.Name == name {
			return function, true
		}
	}
	return Function{}, false
}

// HTTPRequest is what a pack asks the host to send.
//
// It is a request description rather than an *http.Request because the guest
// has no HTTP client and must not build one: the host constructs the request,
// applies the policy and the credential, and never lets the guest name a header
// that would let it address the host's own transport.
type HTTPRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// Headers are the request's own headers. The host refuses hop-by-hop and
	// identity headers, which belong to the transport rather than the pack.
	Headers map[string]string `json:"headers,omitempty"`
	// Credential names a credential type the node attached and the pack
	// declared. The host resolves it and applies it; the pack never sees the
	// secret.
	Credential string `json:"credential,omitempty"`
	// FollowRedirects asks for the following client. Default is not to follow,
	// matching the HTTP node.
	FollowRedirects bool `json:"followRedirects,omitempty"`
	// TimeoutMS tightens the request's wall clock. It can only tighten: the
	// host takes the smallest of this, the deployment's policy timeout and the
	// run's remaining budget.
	TimeoutMS int `json:"timeoutMs,omitempty"`
}

// HTTPResponse is the response head. The body arrives in SlotBody, because it
// can be megabytes and has no business being JSON-escaped.
type HTTPResponse struct {
	Status     int                 `json:"status"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyLength int                 `json:"bodyLength"`
	// Truncated reports that the body was cut off at the deployment's response
	// cap, so a pack cannot mistake a partial body for the whole one.
	Truncated bool `json:"truncated,omitempty"`
}

// BinaryRef is what an item carries in place of a payload, and what
// binary_write answers with. The shape matches internal/workflow's so the host
// can hand one straight to an item.
type BinaryRef struct {
	ID        string `json:"id"`
	FileName  string `json:"fileName,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// BinaryWrite describes a payload a pack is storing.
type BinaryWrite struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType,omitempty"`
}

// HostError is a refusal or a failure the host reported, carrying the code the
// guest can match on and the message a person reads.
type HostError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error renders the failure the way a pack author reads it in a log.
func (e *HostError) Error() string {
	if e == nil {
		return ""
	}
	return "host refused the call (" + e.Code + "): " + e.Message
}

// Is matches the error sentinels, so a pack writes
// `errors.Is(err, sdk.ErrDeniedError)` rather than switching on strings.
func (e *HostError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrInvalidError:
		return e.Code == CodeInvalid
	case ErrDeniedError:
		return e.Code == CodeDenied
	case ErrBlockedError:
		return e.Code == CodeBlocked
	case ErrFailedError:
		return e.Code == CodeFailed
	case ErrTooLargeError:
		return e.Code == CodeTooLarge
	case ErrNotFoundError:
		return e.Code == CodeNotFound
	}
	return false
}

// NodeInfo identifies the node definition a run is executing, so a pack can
// branch on its own type and version.
type NodeInfo struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	Name    string `json:"name"`
}

// Envelope is what the host writes to a pack's standard input.
//
// Parameters are resolved by the host, per item or per batch according to the
// node's mode, and are the same map the editor validated — a pack does not
// resolve expressions itself. Items carry their JSON and their payload
// references.
type Envelope struct {
	ABI        string         `json:"abi"`
	Node       NodeInfo       `json:"node"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Items      []Item         `json:"items"`
}
