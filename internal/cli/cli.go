package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// envURLVar and envTokenVar are the environment variables an agent or a CI
	// job sets instead of passing a flag, so a token never lands in a process
	// listing.
	envURLVar   = "KILASFLOW_URL"
	envTokenVar = "KILASFLOW_TOKEN"

	// defaultBaseURL is where a default installation serves: config.example.yaml
	// binds 0.0.0.0:8080, which on loopback is this.
	defaultBaseURL = "http://127.0.0.1:8080"
)

// Env is everything Run needs from the process, so a test can drive the CLI
// without a subprocess and the composition root owns os.*.
type Env struct {
	Args    []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	TTY     bool
	Version string
}

// Context is one CLI invocation: the environment, the verb being run, the
// flags it was given and the client it talks through.
//
// A verb writes its payload to Data and, when it has one, its identifier to
// Primary; Run turns those into the envelope, the human text, or the --quiet
// line.
type Context struct {
	Env       Env
	Verb      Verb
	Flags     *GlobalFlags
	VerbFlags any
	Client    *Client
	Ctx       context.Context
	Data      any
	Primary   string
	// Operation overrides the operation id the envelope reports. A verb that
	// resolves its operation at run time (the api escape hatch) sets it, so
	// meta.operation names the API call rather than the verb that made it.
	Operation string
	// Streamed marks an invocation whose verb already wrote its payload to
	// stdout. The bytes are the output, so Run writes no envelope over them and
	// reports a failure on stderr instead.
	Streamed bool
	// resolved is the configuration chain for this invocation, read once by
	// settings() so the client and the credential verbs agree on it.
	resolved *Settings
}

// IsTTY reports whether w is a character device, using only the standard
// library: a terminal is the difference between human output and an envelope.
func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// Run executes one CLI invocation against the binary's own verb registry.
//
// handled is false when the process belongs to the server rather than to the
// CLI: no arguments, only flags (-config, -version, -h and the rest are the
// server's), or the explicit `serve` verb. cmd/kilasflow calls Run first and
// carries on into the server when handled is false, which is what keeps every
// Compose file and the container entrypoint working.
func Run(env Env) (code int, handled bool) {
	return run(registry(), env)
}

// run executes one invocation against an explicit verb list.
//
// Production only ever passes the registry; the parameter exists because the
// guard's tests drive the --yes refusal against a fixture registered inside the
// test, which keeps that contract provable without depending on a verb whose own
// operation would have to answer.
func run(verbs []Verb, env Env) (code int, handled bool) {
	if !claimedByCLI(env.Args) {
		return ExitOK, false
	}

	verb, rest, ok := splitVerb(verbs, env.Args)
	if !ok {
		// An unknown word is a usage error, never a server boot: `kflow` in
		// particular is not an alias, it is a stale binary name.
		ctx := &Context{Env: env, Verb: Verb{Path: env.Args[0]}, Flags: &GlobalFlags{}}

		return renderResult(env, ctx, usageError("unknown command %q: run `kilasflow help` for the verb list", env.Args[0])), true
	}

	if verb.Path == ServeVerb {
		// The flags after `serve` are the server's, so they must not be parsed
		// here. verb.Run carries the same signal for any caller that invokes
		// the verb directly.
		return ExitOK, false
	}

	fs := flag.NewFlagSet(verb.Path, flag.ContinueOnError)
	fs.SetOutput(stderr(env))
	// The usage text names the verb's own flags, and PrintDefaults lists them
	// with the sentences that were passed when they were registered. Without
	// it `-h` printed a verb's summary and nothing else, so the flags an agent
	// needs to fill an operation's {placeholder} — --path, --query, --body,
	// --out — were undiscoverable from the CLI itself, which is the one place
	// the reference page tells an agent to look.
	fs.Usage = func() {
		fmt.Fprintf(stderr(env), "usage: kilasflow %s [flags] [args]\n  %s\n", verb.Path, verb.Summary)
		fs.PrintDefaults()
	}

	flags := registerGlobalFlags(fs)
	var verbFlags any
	if verb.Flags != nil {
		verbFlags = verb.Flags(fs)
	}

	positional, err := parseFlags(fs, flags, rest)
	if err != nil {
		// flag has already printed the reason and the usage line.
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK, true
		}

		return ExitUsage, true
	}

	ctx := &Context{
		Env:       env,
		Verb:      verb,
		Flags:     flags,
		VerbFlags: verbFlags,
		Ctx:       context.Background(),
	}

	// The guard runs before the configuration chain and before any request: a
	// refusal is about the intent the verb would carry out, so it must not
	// depend on which server the caller pointed at, and it must not send
	// anything.
	if err := requireConfirmation(verb, flags.Yes, env); err != nil {
		return renderResult(env, ctx, err), true
	}

	client, clientErr := buildClient(ctx)
	ctx.Client = client

	started := client.now()
	runErr := clientErr
	if runErr == nil {
		// The second half of the guard, and it is here rather than beside
		// requireConfirmation because answering it needs a request: consent is
		// checked first, so a missing --yes still sends nothing at all, and
		// only an invocation that is already going to talk to the server pays
		// for the identity read. A scoped token is refused before the verb's
		// own operation, so it never reaches the mutation the server would
		// answer with 403.
		runErr = requireAuthority(ctx, verb)
	}
	if runErr == nil {
		runErr = verb.Run(ctx, positional)
	}
	duration := client.now().Sub(started)

	if errors.Is(runErr, errServeRequested) {
		return ExitOK, false
	}

	return renderResultWithDuration(env, ctx, runErr, duration), true
}

// claimedByCLI reports whether the invocation is a CLI verb rather than the
// server's own command line.
func claimedByCLI(args []string) bool {
	if len(args) == 0 {
		return false
	}

	// A leading flag belongs to the server: -config, -version, -role, -h.
	return !strings.HasPrefix(args[0], "-")
}

// buildClient resolves the configuration chain and builds the client.
//
// The chain itself lives in config.go; this only turns its answer into a
// client. A configuration failure still returns a client, because Run measures
// the invocation's duration through it.
func buildClient(ctx *Context) (*Client, error) {
	settings, err := ctx.settings()
	if err != nil {
		return newClient(ctx.Flags, "", ""), err
	}

	client := newClient(ctx.Flags, "", settings.Token)
	if ctx.Flags.Verbose {
		client.Verbose = stderr(ctx.Env)
	}

	if settings.URL == "" {
		return client, nil
	}

	root, err := normalizeBaseURL(settings.URL)
	if err != nil {
		return client, err
	}
	client.BaseURL = root

	return client, nil
}

// requireAuthority refuses a guarded verb whose credential is an agent token.
//
// Design §4.7 gives a guarded verb two gates, and they are different
// questions. --yes is the caller's *consent*: a run without it is refused
// before anything is sent, because consent is never inferred. This is the
// caller's *authority*: a guarded operation reaches beyond the tenant's own
// data — it publishes an endpoint, destroys one, or stores a secret every
// workflow can use — so only the tenant-wide key that owns the tenant may do
// it, and a scoped agent token is refused here whatever the flags say. It is
// the same refusal the server would send as a 403, moved in front of the
// operation so the caller learns it before a mutation is attempted, and it
// carries the same error code, `scope_denied`, because that is what an agent
// reads as "drop this step".
//
// The question is answered by the identity resource the CLI already has a verb
// for: `auth whoami` reads /auth/me, whose `scopes` list is the key's scope
// binding. A tenant-wide key carries none — NULL scopes is the legacy key the
// design leaves untouched — so a non-empty list is exactly "this is an agent
// token". The read goes through whoamiWith, on the invocation's own credential.
//
// An invocation with no server URL is left alone: the verb refuses that one
// itself with a usage error naming every way to configure a URL, and a probe
// against an empty base URL would report a network failure for what is the
// caller's missing configuration.
func requireAuthority(ctx *Context, verb Verb) error {
	if !verb.Guarded || ctx.Client == nil || ctx.Client.BaseURL == "" {
		return nil
	}

	who, err := whoamiWith(ctx, ctx.Client.Token)
	if err != nil {
		return err
	}
	if !who.Scoped() {
		return nil
	}

	return &ExitError{
		Code:    ExitRefused,
		ErrCode: scopeDeniedCode,
		Message: verb.Path + " " + verb.Refusal + "; this needs a tenant-wide key: " +
			"a scoped agent token may not do it, and --yes is consent, not authority",
	}
}

// newClient builds a client whose requests are bounded by --timeout.
func newClient(flags *GlobalFlags, base, token string) *Client {
	timeout := defaultTimeout
	skills := []string(nil)
	if flags != nil && flags.Timeout > 0 {
		timeout = flags.Timeout
	}
	if flags != nil {
		for _, name := range strings.Split(flags.SkillsUsed, ",") {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				skills = append(skills, trimmed)
			}
		}
	}

	return &Client{
		BaseURL:    strings.TrimRight(base, "/"),
		Token:      token,
		HTTP:       &http.Client{Timeout: timeout},
		Now:        time.Now,
		SkillsUsed: skills,
	}
}

// renderResult renders a failure that happened before a verb ran.
func renderResult(env Env, ctx *Context, err error) int {
	return renderResultWithDuration(env, ctx, err, 0)
}

// renderResultWithDuration prints the envelope, the human text or the quiet
// identifier, and returns the exit code.
func renderResultWithDuration(env Env, ctx *Context, runErr error, duration time.Duration) int {
	meta := Meta{Operation: operationOf(ctx), DurationMs: duration.Milliseconds()}
	mode := resolveMode(ctx.Flags, env.TTY)

	if runErr == nil {
		if ctx.Streamed {
			// The verb already wrote the payload: raw bytes are the output, and
			// an envelope after them would corrupt the stream.
			return ExitOK
		}

		switch mode {
		case modeQuiet:
			if ctx.Primary != "" {
				fmt.Fprintln(stdout(env), ctx.Primary)
			}
		case modeJSON:
			writeEnvelope(stdout(env), Envelope{OK: true, Data: ctx.Data, Meta: meta})
		default:
			if ctx.Verb.Human != nil {
				ctx.Verb.Human(stdout(env), ctx.Data)
			} else {
				printJSONValue(stdout(env), ctx.Data)
			}
		}

		return ExitOK
	}

	failure := asExitError(runErr)
	if mode == modeJSON && !ctx.Streamed {
		writeEnvelope(stdout(env), Envelope{
			OK:    false,
			Error: &ErrorEnvelope{Code: failure.ErrCode, Message: failure.Message, Status: failure.Status, Detail: detailOf(failure)},
			Meta:  meta,
		})
	} else {
		// A streamed invocation's stdout is its payload, so a failure is
		// reported on stderr: an envelope appended to a byte stream would
		// corrupt the thing the caller is reading.
		fmt.Fprintf(stderr(env), "kilasflow: %s\n", failure.Message)
	}

	return failure.Code
}

// asExitError classifies a verb's error: anything that is not an ExitError is
// an unexpected failure, which is exit 1.
func asExitError(err error) *ExitError {
	var failure *ExitError
	if errors.As(err, &failure) {
		return failure
	}

	return &ExitError{Code: ExitFailure, ErrCode: "error", Message: err.Error()}
}

// detailOf keeps the server's own explanation, when there is one.
func detailOf(failure *ExitError) *ErrorDetail {
	if len(failure.Problem) == 0 && failure.Body == "" && len(failure.Execution) == 0 && len(failure.Issues) == 0 {
		return nil
	}

	return &ErrorDetail{
		Problem:   failure.Problem,
		Body:      failure.Body,
		Execution: failure.Execution,
		Issues:    failure.Issues,
	}
}

// operationOf is the operation id the envelope reports: the one the verb
// resolved at run time, else the one its definition names, else its path for
// the verbs that are local.
func operationOf(ctx *Context) string {
	if ctx.Operation != "" {
		return ctx.Operation
	}

	if ctx.Verb.Operation != "" {
		return ctx.Verb.Operation
	}

	return ctx.Verb.Path
}

// getenv reads an environment variable, tolerating an Env without a getter.
func getenv(env Env, name string) string {
	if env.Getenv == nil {
		return ""
	}

	return env.Getenv(name)
}

// bound sets the invocation's deadline rather than one request's.
//
// A verb that waits or streams outlives a per-request timeout, so it takes the
// clock into its own hands: the context carries the deadline and the HTTP
// client's own timer is switched off, because a stream cut short by a timer
// meant for a single request is indistinguishable from a run that ended. A
// duration of zero or less leaves the context alone and means "no deadline".
func (ctx *Context) bound(after time.Duration) context.CancelFunc {
	if ctx.Client != nil && ctx.Client.HTTP != nil {
		ctx.Client.HTTP.Timeout = 0
	}
	if after <= 0 {
		return func() {}
	}

	bounded, cancel := context.WithTimeout(ctx.Ctx, after)
	ctx.Ctx = bounded

	return cancel
}

// deadlineExceeded reports whether the invocation ran out of time rather than
// failing, which is the difference between "keep watching" and "report this".
func (ctx *Context) deadlineExceeded() bool {
	return errors.Is(ctx.Ctx.Err(), context.DeadlineExceeded)
}

// stdout and stderr fall back to discarding, so a caller that only wants the
// exit code does not have to supply writers.
func stdout(env Env) io.Writer {
	if env.Stdout == nil {
		return io.Discard
	}

	return env.Stdout
}

func stderr(env Env) io.Writer {
	if env.Stderr == nil {
		return io.Discard
	}

	return env.Stderr
}
