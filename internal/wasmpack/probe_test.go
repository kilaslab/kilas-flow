package wasmpack_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// The tests in this file drive the Go probe guest, so they need a toolchain and
// they translate a real wasip1 module: the package's shared cache is what keeps
// that cost to once per test binary.

// probeInvocation is the standard invocation: every capability declared, the
// default limits, and nothing attached.
func probeInvocation(options ...func(*wasmpack.Invocation)) wasmpack.Invocation {
	invocation := wasmpack.Invocation{Caps: fullCaps(), Limits: wasmpack.DefaultLimits()}
	for _, option := range options {
		option(&invocation)
	}
	return invocation
}

func withNode(node workflow.IRNode) func(*wasmpack.Invocation) {
	return func(invocation *wasmpack.Invocation) { invocation.IR = node }
}

func withRequest(request engine.Request) func(*wasmpack.Invocation) {
	return func(invocation *wasmpack.Invocation) { invocation.Request = request }
}

func withLimits(limits wasmpack.Limits) func(*wasmpack.Invocation) {
	return func(invocation *wasmpack.Invocation) { invocation.Limits = limits }
}

func withCaps(caps wasmpack.Capabilities) func(*wasmpack.Invocation) {
	return func(invocation *wasmpack.Invocation) { invocation.Caps = caps }
}

func withInputBinary(refs ...workflow.BinaryRef) func(*wasmpack.Invocation) {
	return func(invocation *wasmpack.Invocation) { invocation.InputBinary = refs }
}

// localHostPort rewrites a 127.0.0.1 URL to the same port on localhost, so a
// redirect crosses a hostname boundary while staying on the test machine.
func localHostPort(t *testing.T, rawURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("parsing %q failed: %v", rawURL, err)
	}
	return "localhost:" + request.URL.Port()
}

// TestOutboundHTTPGoesThroughTheSSRFPolicy is the criterion's core: a pack's
// request passes through the same policy the HTTP node uses, so it reaches an
// endpoint the deployment deliberately named and nothing else on the same host.
// The policy never has AllowPrivateNetworks set.
func TestOutboundHTTPGoesThroughTheSSRFPolicy(t *testing.T) {
	allowed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Server", "allowed")
		fmt.Fprint(writer, "hello from the allowed server")
	}))
	defer allowed.Close()

	refused := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a pack reached a loopback endpoint the policy never named")
	}))
	defer refused.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, allowed), Modules: testModules()})

	report, _, _, err := runProbe(t, host, probeInvocation(),
		probeCommand{Op: "http", Method: "GET", URL: allowed.URL + "/items"})
	if err != nil {
		t.Fatalf("the run failed with %v", err)
	}
	if !report.OK || report.Status != http.StatusOK {
		t.Fatalf("report = %+v, want a 200 from the allowed endpoint", report)
	}
	if report.Body != "hello from the allowed server" {
		t.Errorf("body = %q, want the endpoint's body", report.Body)
	}

	// The other loopback port is refused at dial time, which is the guard this
	// test exists for: CheckURL does not look at addresses at all.
	report, _, _, err = runProbe(t, host, probeInvocation(),
		probeCommand{Op: "http", Method: "GET", URL: refused.URL + "/items"})
	if err != nil {
		t.Fatalf("the run failed with %v, want a reported refusal", err)
	}
	if report.OK {
		t.Fatal("a pack reached a loopback endpoint the policy never named")
	}
	if report.ErrorCode != sdk.CodeBlocked {
		t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeBlocked)
	}
}

// TestAPackCannotReachTheMetadataAddress pins the address that makes the SSRF
// guard worth having: the cloud metadata endpoint a workflow could otherwise
// use to mint credentials for itself.
func TestAPackCannotReachTheMetadataAddress(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy(), Modules: testModules()})
	report, _, _, err := runProbe(t, host, probeInvocation(),
		probeCommand{Op: "http", Method: "GET", URL: "http://169.254.169.254/latest/meta-data/iam/security-credentials/"})
	if err != nil {
		t.Fatalf("the run failed with %v, want a reported refusal", err)
	}
	if report.OK {
		t.Fatal("a pack reached the metadata address")
	}
	if report.ErrorCode != sdk.CodeBlocked {
		t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeBlocked)
	}
	if report.Body != "" {
		t.Errorf("body = %q, want nothing at all", report.Body)
	}
}

// TestForbiddenRequestHeadersAreRefused proves the pack cannot take over the
// transport: a header that decides framing or the destination is refused before
// the request is built, and the endpoint sees nothing.
func TestForbiddenRequestHeadersAreRefused(t *testing.T) {
	var reached int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached++
	}))
	defer server.Close()
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, server), Modules: testModules()})

	for _, header := range []string{"Host", "Content-Length", "Transfer-Encoding", "Connection", "Proxy-Authorization"} {
		t.Run(header, func(t *testing.T) {
			report, _, _, err := runProbe(t, host, probeInvocation(), probeCommand{
				Op: "http", Method: "GET", URL: server.URL + "/items",
				Headers: map[string]string{header: "x"},
			})
			if err != nil {
				t.Fatalf("the run failed with %v, want a reported refusal", err)
			}
			if report.OK {
				t.Fatalf("the %s header was accepted", header)
			}
			if report.ErrorCode != sdk.CodeDenied {
				t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeDenied)
			}
		})
	}
	if reached != 0 {
		t.Errorf("the endpoint was reached %d times, want none", reached)
	}
}

// TestRedirectsAreNotFollowedUnlessAskedAndAScopedCredentialStopsTheChain
// covers both halves of the redirect rule: a pack gets the 302 unless it asks
// to follow, and a credential's domain scope survives the hop rather than
// following a 30x to a host AllowedDomains never named.
func TestRedirectsAreNotFollowedUnlessAskedAndAScopedCredentialStopsTheChain(t *testing.T) {
	var destinationHits int
	var leaked string
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		destinationHits++
		leaked = request.Header.Get("X-Api-Key")
		writer.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "http://"+localHostPort(t, destination.URL)+"/steal", http.StatusFound)
	}))
	defer redirector.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{
		Policy:  loopbackPolicy(t, redirector, destination),
		Modules: testModules(),
	})

	t.Run("the redirect is handed back", func(t *testing.T) {
		report, _, _, err := runProbe(t, host, probeInvocation(),
			probeCommand{Op: "http", Method: "GET", URL: redirector.URL + "/start"})
		if err != nil {
			t.Fatalf("the run failed with %v", err)
		}
		if !report.OK || report.Status != http.StatusFound {
			t.Fatalf("report = %+v, want the 302 itself", report)
		}
	})

	t.Run("a scoped credential stops the chain", func(t *testing.T) {
		// The credential is scoped to the host the request starts on, so the
		// hop to the same port under another name is outside its scope.
		resolver := &countingResolver{credential: wahaCredential("127.0.0.1")}
		report, _, _, err := runProbe(t, host,
			probeInvocation(
				withNode(workflow.IRNode{Name: "Fetch", Credentials: map[string]string{"wahaApi": "cred_waha"}}),
				withRequest(engineRequest(resolver)),
			),
			probeCommand{Op: "http", Method: "GET", URL: redirector.URL + "/start", Credential: "wahaApi", FollowRedirects: true})
		if err != nil {
			t.Fatalf("the run failed with %v", err)
		}
		if !report.OK || report.Status != http.StatusFound {
			t.Fatalf("report = %+v, want the last in-scope response", report)
		}
		if resolver.calls != 1 {
			t.Errorf("the credential was resolved %d times, want once", resolver.calls)
		}
	})

	if destinationHits != 0 {
		t.Errorf("the redirect destination was reached %d times, want none", destinationHits)
	}
	if leaked != "" {
		t.Errorf("the destination received the credential header %q", leaked)
	}
}

// TestTheResponseIsBoundedByThePolicy proves a pack cannot pull an unbounded
// response into memory, and that it is told the body was cut rather than
// handed a partial body as if it were the whole one.
func TestTheResponseIsBoundedByThePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer server.Close()

	policy := loopbackPolicy(t, server)
	policy.MaxResponseBytes = 64
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: policy, Modules: testModules()})

	report, _, _, err := runProbe(t, host, probeInvocation(),
		probeCommand{Op: "http", Method: "GET", URL: server.URL + "/big"})
	if err != nil {
		t.Fatalf("the run failed with %v", err)
	}
	if !report.OK {
		t.Fatalf("report = %+v, want the truncated response", report)
	}
	if !report.Truncated {
		t.Error("the response was not reported as truncated")
	}
	if report.BodySize != 64 || len(report.Body) != 64 {
		t.Errorf("body = %d bytes (reported %d), want the policy's 64", len(report.Body), report.BodySize)
	}
}

// TestASlowUpstreamIsCutOffByTheWallClock proves the run's clock bounds the
// host's I/O too: a pack blocked in a request is released when the run's wall
// clock runs out, and the failure is the run's time limit rather than a failed
// call the pack might retry.
func TestASlowUpstreamIsCutOffByTheWallClock(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-request.Context().Done():
		}
	}))
	defer slow.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, slow), Modules: testModules()})
	limits := wasmpack.DefaultLimits()
	limits.Timeout = 300 * time.Millisecond

	started := time.Now()
	_, _, _, err := runProbe(t, host, probeInvocation(withLimits(limits)),
		probeCommand{Op: "http", Method: "GET", URL: slow.URL + "/slow"})
	elapsed := time.Since(started)

	if !errors.Is(err, runcode.ErrTimeLimit) {
		t.Fatalf("Invoke() error = %v, want the named time limit", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the run took %s, want it cut off by the wall clock", elapsed)
	}
}

// TestACredentialThePackDidNotDeclareIsNotResolvable proves the declaration is
// checked before anything is looked up: the credential is attached to the node,
// and the resolver is still never called.
func TestACredentialThePackDidNotDeclareIsNotResolvable(t *testing.T) {
	var reached int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached++
	}))
	defer server.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, server), Modules: testModules()})
	resolver := &countingResolver{credential: wahaCredential()}
	// Every capability is declared except the credential type the request
	// names: the probe guest reaches all six host functions, so narrowing the
	// declaration to make a point would fail at instantiation instead.
	caps := fullCaps()
	caps.Credentials = []string{"wahaApi"}
	report, _, _, err := runProbe(t, host,
		probeInvocation(
			withCaps(caps),
			// telegramApi is attached to the node, and not declared by the pack.
			withNode(workflow.IRNode{Name: "Fetch", Credentials: map[string]string{
				"wahaApi": "cred_waha", "telegramApi": "cred_telegram",
			}}),
			withRequest(engineRequest(resolver)),
		),
		probeCommand{Op: "http", Method: "GET", URL: server.URL + "/items", Credential: "telegramApi"})
	if err != nil {
		t.Fatalf("the run failed with %v, want a reported refusal", err)
	}
	if report.OK {
		t.Fatal("a pack used a credential type it did not declare")
	}
	if report.ErrorCode != sdk.CodeDenied {
		t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeDenied)
	}
	if resolver.calls != 0 {
		t.Errorf("the resolver was called %d times, want none", resolver.calls)
	}
	if reached != 0 {
		t.Errorf("the endpoint was reached %d times, want none", reached)
	}
}

// TestADeclaredCredentialMustBeAttachedToTheNode proves declaring a credential
// is not enough: the node has to carry it, and the request is refused rather
// than sent unauthenticated.
func TestADeclaredCredentialMustBeAttachedToTheNode(t *testing.T) {
	var reached int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached++
	}))
	defer server.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, server), Modules: testModules()})
	resolver := &countingResolver{credential: wahaCredential()}
	report, _, _, err := runProbe(t, host,
		probeInvocation(
			withNode(workflow.IRNode{Name: "Fetch", Credentials: map[string]string{"telegramApi": "cred_telegram"}}),
			withRequest(engineRequest(resolver)),
		),
		probeCommand{Op: "http", Method: "GET", URL: server.URL + "/items", Credential: "wahaApi"})
	if err != nil {
		t.Fatalf("the run failed with %v, want a reported refusal", err)
	}
	if report.OK {
		t.Fatal("a pack used a credential the node does not carry")
	}
	if report.ErrorCode != sdk.CodeDenied {
		t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeDenied)
	}
	if resolver.calls != 0 {
		t.Errorf("the resolver was called %d times, want none", resolver.calls)
	}
	if reached != 0 {
		t.Errorf("the endpoint was reached %d times, want none", reached)
	}
}

// TestCredentialDomainScopeAppliesToPackRequests proves a credential's
// AllowedDomains bound a pack exactly as they bound the HTTP node: the request
// is refused before a byte is sent.
func TestCredentialDomainScopeAppliesToPackRequests(t *testing.T) {
	var reached int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached++
	}))
	defer server.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, server), Modules: testModules()})
	resolver := &countingResolver{credential: wahaCredential("other.example.test")}
	report, _, _, err := runProbe(t, host,
		probeInvocation(
			withNode(wahaNode()),
			withRequest(engineRequest(resolver)),
		),
		probeCommand{Op: "http", Method: "GET", URL: server.URL + "/items", Credential: "wahaApi"})
	if err != nil {
		t.Fatalf("the run failed with %v, want a reported refusal", err)
	}
	if report.OK {
		t.Fatal("a credential was sent to a host outside its AllowedDomains")
	}
	if report.ErrorCode != sdk.CodeDenied {
		t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeDenied)
	}
	if reached != 0 {
		t.Errorf("the endpoint was reached %d times, want none", reached)
	}
}

// TestAuthenticationIsAppliedByTheHostNotTheGuest is the secret-handling
// promise, end to end: the endpoint receives the credential's header, and the
// pack's own output never contains the secret.
func TestAuthenticationIsAppliedByTheHostNotTheGuest(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = request.Header.Get("X-Api-Key")
		fmt.Fprint(writer, "ok")
	}))
	defer server.Close()

	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: loopbackPolicy(t, server), Modules: testModules()})
	resolver := &countingResolver{credential: wahaCredential()}
	report, outcome, _, err := runProbe(t, host,
		probeInvocation(
			withNode(wahaNode()),
			withRequest(engineRequest(resolver)),
		),
		probeCommand{Op: "http", Method: "GET", URL: server.URL + "/items", Credential: "wahaApi"})
	if err != nil {
		t.Fatalf("the run failed with %v", err)
	}
	if !report.OK || report.Status != http.StatusOK {
		t.Fatalf("report = %+v, want a 200", report)
	}
	if seen != "waha-secret" {
		t.Errorf("the endpoint received %q, want the credential's header", seen)
	}
	if strings.Contains(string(outcome.Stdout), "waha-secret") {
		t.Error("the credential's secret appeared in the pack's output")
	}
}

// TestASecretCredentialFieldNeverCrossesTheBoundary proves the pack may read a
// non-secret field and is refused a secret one — refused, not redacted, so it
// cannot mistake a placeholder for the value.
func TestASecretCredentialFieldNeverCrossesTheBoundary(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy(), Modules: testModules()})
	resolver := &countingResolver{credential: wahaCredential()}
	invocation := probeInvocation(withNode(wahaNode()), withRequest(engineRequest(resolver)))

	report, _, _, err := runProbe(t, host, invocation,
		probeCommand{Op: "credential", CredentialType: "wahaApi", Field: "baseUrl"})
	if err != nil {
		t.Fatalf("the run failed with %v", err)
	}
	if !report.OK || report.Value != "https://waha.example.test" {
		t.Fatalf("report = %+v, want the credential's non-secret base URL", report)
	}

	report, outcome, _, err := runProbe(t, host, invocation,
		probeCommand{Op: "credential", CredentialType: "wahaApi", Field: "apiKey"})
	if err != nil {
		t.Fatalf("the run failed with %v, want a reported refusal", err)
	}
	if report.OK {
		t.Fatal("a pack read a secret credential field")
	}
	if report.ErrorCode != sdk.CodeDenied {
		t.Errorf("error code = %q, want %q", report.ErrorCode, sdk.CodeDenied)
	}
	if strings.Contains(string(outcome.Stdout), "waha-secret") {
		t.Error("the credential's secret appeared in the pack's output")
	}
}

// TestBinaryWriteReturnsARefTheOutputMayCarry proves the write half of the
// payload capability, and that what a run wrote is readable when a later item
// carries its reference.
func TestBinaryWriteReturnsARefTheOutputMayCarry(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy(), Modules: testModules()})
	store := newFakeBinaries(nil)

	report, _, effects, err := runProbe(t, host,
		probeInvocation(withRequest(engineRequest(nil, withBinaries(store)))),
		probeCommand{Op: "write", Name: "report.csv", MediaType: "text/csv", Data: "a,b\n1,2\n"})
	if err != nil {
		t.Fatalf("the run failed with %v", err)
	}
	if !report.OK || report.Ref == nil {
		t.Fatalf("report = %+v, want a payload reference", report)
	}
	if report.Ref.FileName != "report.csv" || report.Ref.MediaType != "text/csv" || report.Ref.Size != int64(len("a,b\n1,2\n")) {
		t.Errorf("ref = %+v, want the payload's description", report.Ref)
	}
	if len(effects.Writes) != 1 || effects.Writes[0].ID != report.Ref.ID {
		t.Errorf("effects = %+v, want the one payload this run wrote", effects)
	}
	if string(store.payloads[report.Ref.ID]) != "a,b\n1,2\n" {
		t.Errorf("the store holds %q, want the payload", store.payloads[report.Ref.ID])
	}

	// A later run whose item carries that reference may read it back.
	report, _, _, err = runProbe(t, host,
		probeInvocation(
			withRequest(engineRequest(nil, withBinaries(store))),
			withInputBinary(workflow.BinaryRef{ID: report.Ref.ID, FileName: "report.csv"}),
		),
		probeCommand{Op: "read", ID: report.Ref.ID})
	if err != nil {
		t.Fatalf("the run failed with %v", err)
	}
	if !report.OK || report.Data != "a,b\n1,2\n" {
		t.Fatalf("report = %+v, want the payload back", report)
	}
}

// TestTheMemoryLimitFailsThePackRunWithANamedError proves a real guest meets the
// run's memory limit as a named failure with the configured limit stated, not
// as a trap or the Go runtime's own message.
func TestTheMemoryLimitFailsThePackRunWithANamedError(t *testing.T) {
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy(), Modules: testModules()})
	limits := wasmpack.DefaultLimits()
	limits.MemoryPages = 32

	_, _, _, err := runProbe(t, host, probeInvocation(withLimits(limits)),
		probeCommand{Op: "http", Method: "GET", URL: "https://example.test/items"})
	if !errors.Is(err, runcode.ErrMemoryLimit) {
		t.Fatalf("Invoke() error = %v, want the named memory limit", err)
	}
	if !strings.Contains(err.Error(), "32-page") {
		t.Errorf("error = %v, want it to state the configured limit", err)
	}
}
