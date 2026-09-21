package runcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
)

// RuntimeVersion changes whenever the compilation or execution contract does.
//
// It is part of the artifact key, so a change here invalidates every cached
// artifact rather than silently running code built against an older contract.
const RuntimeVersion = "wasip1-v1"

// ErrCompilerUnavailable reports that no Go toolchain is reachable.
var ErrCompilerUnavailable = errors.New("go toolchain is not available for compiling Code nodes")

// ErrNotCompiled reports that source has no cached artifact yet.
var ErrNotCompiled = errors.New("code has not been compiled yet")

// Limits bound one execution.
type Limits struct {
	// Timeout bounds wall-clock execution, which is also the CPU bound: a
	// module spinning in a loop is stopped by the same deadline. It covers the
	// user's program alone — building the artifact and translating it to
	// machine code are the host's work and are bounded elsewhere — so the same
	// number means the same thing on a laptop and on a small CI runner.
	Timeout time.Duration
	// MemoryPages bounds linear memory in 64KiB WebAssembly pages.
	MemoryPages uint32
	// MaxOutputBytes bounds what the module may write back.
	MaxOutputBytes int64
	// MaxHostCalls bounds how many times the module may call into the host.
	//
	// It bounds the work the host would do on the module's behalf — an HTTP
	// request, a credential, a read of someone's data — so it is the budget a
	// pack's capabilities are measured in. It has no effect on a module that
	// cannot reach the host at all, which is the Code node.
	MaxHostCalls int
}

// DefaultLimits are conservative because the code is user supplied.
func DefaultLimits() Limits {
	return Limits{
		Timeout:        10 * time.Second,
		MemoryPages:    256, // 16 MiB
		MaxOutputBytes: 4 << 20,
		MaxHostCalls:   100,
	}
}

// orDefault fills what a caller left zero from the product's defaults, so a
// zero field means "the shipped value" rather than "unbounded".
func (limits Limits) orDefault() Limits {
	defaults := DefaultLimits()
	if limits.Timeout <= 0 {
		limits.Timeout = defaults.Timeout
	}
	if limits.MemoryPages == 0 {
		limits.MemoryPages = defaults.MemoryPages
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if limits.MaxHostCalls <= 0 {
		limits.MaxHostCalls = defaults.MaxHostCalls
	}
	return limits
}

// SourceHash is the stable identity of one piece of user code.
//
// It covers the runtime version as well as the source, so the same code
// compiled under a different contract is a different artifact.
func SourceHash(source string) string {
	sum := sha256.Sum256([]byte(RuntimeVersion + "\x00" + source))
	return hex.EncodeToString(sum[:])
}

// Artifact is one compiled module.
type Artifact struct {
	Hash           string
	RuntimeVersion string
	Module         []byte
	CompiledAt     time.Time
}

// Compiler turns source into a module. It is an interface so a deployment can
// use the local toolchain, a compiler sidecar, or none at all.
type Compiler interface {
	Compile(ctx context.Context, source string) ([]byte, error)
	Available() bool
}

// Cache stores compiled artifacts.
type Cache interface {
	Get(hash string) (Artifact, bool)
	Put(artifact Artifact)
}

// MemoryCache is the default in-process artifact cache.
type MemoryCache struct {
	mu        sync.RWMutex
	artifacts map[string]Artifact
	// hits and misses make cache reuse observable rather than assumed.
	hits, misses int
}

// NewMemoryCache creates an empty cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{artifacts: map[string]Artifact{}}
}

// Get returns a cached artifact.
func (cache *MemoryCache) Get(hash string) (Artifact, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	artifact, found := cache.artifacts[hash]
	if found {
		cache.hits++
	} else {
		cache.misses++
	}
	return artifact, found
}

// Put stores an artifact.
func (cache *MemoryCache) Put(artifact Artifact) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.artifacts[artifact.Hash] = artifact
}

// Stats reports cache hits and misses.
func (cache *MemoryCache) Stats() (hits, misses int) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.hits, cache.misses
}

// ModuleCache holds the machine code wazero translates an artifact into.
//
// The artifact Cache above stores the WebAssembly a Go build produced; this
// stores what wazero then turns that WebAssembly into. They are separate costs:
// a wasip1 module carrying the Go runtime is a few megabytes, and translating
// one takes the better part of a second on a warm laptop. Without this cache
// that translation happened on every single sandbox call, so a Code node in
// per-item mode paid for it once per item — contradicting the promise the node
// makes in its own editor, that per-item mode costs one build either way.
//
// wazero shares translated code between runtimes that are given the same cache,
// so each artifact is translated once per process while every execution still
// gets its own runtime, instantiated and closed on its own.
type ModuleCache struct {
	compilation wazero.CompilationCache
}

// NewModuleCache creates a cache meant to be shared by every execution in a
// process and to live as long as it, exactly like the artifact cache beside it.
//
// It is safe for concurrent use. Two executions translating the same artifact
// at the same moment may both do the work — wazero holds its lock for the
// lookup and the store, not the translation — which costs duplicated effort
// once and never a wrong answer.
func NewModuleCache() *ModuleCache {
	return &ModuleCache{compilation: wazero.NewCompilationCache()}
}

// Close releases every translation the cache is holding.
func (cache *ModuleCache) Close(ctx context.Context) error {
	if cache == nil || cache.compilation == nil {
		return nil
	}
	return cache.compilation.Close(ctx)
}

// ToolchainCompiler builds with the local Go toolchain.
type ToolchainCompiler struct {
	// GoBinary defaults to "go" resolved on PATH.
	GoBinary string
	// Timeout bounds a build, which can hang on a pathological input.
	Timeout time.Duration
	// CacheDir is where the toolchain keeps its build cache (GOCACHE).
	//
	// A deployment that runs with a read-only root filesystem has one writable
	// place: the data volume the code cache sits on. Empty leaves GOCACHE to
	// the Go toolchain's own default, and the process environment wins over
	// this either way, so an operator who set GOCACHE is never overridden.
	CacheDir string
}

var _ Compiler = (*ToolchainCompiler)(nil)

// NewToolchainCompiler builds the local-toolchain compiler.
func NewToolchainCompiler() *ToolchainCompiler {
	return &ToolchainCompiler{GoBinary: "go", Timeout: 90 * time.Second}
}

// Available reports whether the toolchain can be found.
func (compiler *ToolchainCompiler) Available() bool {
	_, err := exec.LookPath(compiler.binaryName())
	return err == nil
}

// buildEnv is the environment one build runs with.
//
// It is the process environment plus the four decisions that make a Code node
// build safe and reproducible, plus the build cache when the deployment gave
// the code cache a directory: a read-only root filesystem leaves the
// toolchain's default cache unwritable, and pointing GOCACHE at a writable
// volume is what lets such a deployment compile at all. A GOCACHE that is
// already in the process environment wins, because an operator who set one has
// a warm cache somewhere deliberate.
//
// The configured directory is resolved to an absolute path here because the go
// command refuses a relative GOCACHE outright ("build cache is required, but
// could not be located: GOCACHE is not an absolute path") — and the default
// code.cache_dir is relative, exactly like the default database path beside it.
func (compiler *ToolchainCompiler) buildEnv() []string {
	env := append(os.Environ(),
		"GOOS=wasip1", "GOARCH=wasm",
		"CGO_ENABLED=0",
		// No module downloads: user code compiles against the standard library
		// only, so a build cannot reach the network either.
		"GOFLAGS=-mod=mod", "GOPROXY=off", "GONOSUMDB=*", "GONOSUMCHECK=1", "GOWORK=off",
	)
	if compiler != nil && compiler.CacheDir != "" && os.Getenv("GOCACHE") == "" {
		if absolute, err := filepath.Abs(compiler.CacheDir); err == nil {
			env = append(env, "GOCACHE="+absolute)
		}
	}
	return env
}

// Compile builds source into a wasip1 module.
//
// The build runs in a temporary module directory with the network disabled and
// no dependencies, so user code cannot pull in a package at build time — the
// compile step is as constrained as the run step.
func (compiler *ToolchainCompiler) Compile(ctx context.Context, source string) ([]byte, error) {
	if !compiler.Available() {
		return nil, ErrCompilerUnavailable
	}
	if err := ValidateSource(source); err != nil {
		return nil, err
	}

	directory, err := os.MkdirTemp("", "kilasflow-code-*")
	if err != nil {
		return nil, fmt.Errorf("create build directory: %w", err)
	}
	defer os.RemoveAll(directory)

	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module kilasflow.usercode\n\ngo 1.24\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write build module: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "main.go"), []byte(Wrap(source)), 0o600); err != nil {
		return nil, fmt.Errorf("write build source: %w", err)
	}

	timeout := compiler.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	buildCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output := filepath.Join(directory, "module.wasm")
	command := exec.CommandContext(buildCtx, compiler.binaryName(), "build", "-trimpath", "-o", output, ".")
	command.Dir = directory
	command.Env = compiler.buildEnv()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, &CompileError{Detail: sanitizeBuildOutput(stderr.String(), directory)}
	}

	module, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("read compiled module: %w", err)
	}
	return module, nil
}

// CompileError is a user-facing compilation failure.
type CompileError struct {
	Detail string
}

func (err *CompileError) Error() string {
	if err.Detail == "" {
		return "code did not compile"
	}
	return "code did not compile: " + err.Detail
}

// sanitizeBuildOutput removes the build directory from compiler output, so an
// error shown to a user carries no server path.
func sanitizeBuildOutput(output, directory string) string {
	output = strings.ReplaceAll(output, directory+string(os.PathSeparator), "")
	output = strings.ReplaceAll(output, directory, "")
	output = strings.TrimSpace(output)
	if len(output) > 4000 {
		output = output[:4000] + "\n…"
	}
	return output
}

// Wrap turns a user's function body into a complete program.
//
// The data contract is stdin/stdout JSON rather than a host-function ABI: it
// needs nothing beyond WASI's standard streams, which keeps the capability
// surface as small as it can be.
//
// A useful slice of the standard library is imported so a Code node can
// actually do work — format strings, parse numbers, sort, return errors. The
// dangerous packages are imported too, deliberately: `os` and `net` compile
// fine and then fail at run time inside the sandbox, which is a far better
// guarantee than "it would not have compiled". The blank assignments keep Go
// from rejecting the ones a given body does not use.
func Wrap(source string) string {
	return `package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	_ = errors.New
	_ = fmt.Sprintf
	_ = math.Abs
	_ = net.Dial
	_ = sort.Strings
	_ = strconv.Itoa
	_ = strings.TrimSpace
	_ = time.Now
)

type Item struct {
	JSON map[string]any ` + "`json:\"json\"`" + `
}

func run(items []Item) ([]Item, error) {
` + source + `
}

func main() {
	var items []Item
	if err := json.NewDecoder(os.Stdin).Decode(&items); err != nil {
		writeError("input could not be decoded: " + err.Error())
		return
	}
	out, err := run(items)
	if err != nil {
		writeError(err.Error())
		return
	}
	if out == nil {
		out = []Item{}
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"items": out})
}

func writeError(message string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"error": message})
	os.Exit(1)
}
`
}

// ValidateSource rejects source that cannot be a function body.
//
// This is a usability check, not the security boundary: the sandbox is what
// makes untrusted code safe. Catching an obvious mistake here gives a better
// message than a compiler error would.
func ValidateSource(source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("code is required")
	}
	if strings.Contains(source, "package ") {
		return fmt.Errorf("write only the function body; the package clause is added for you")
	}
	if !strings.Contains(source, "return") {
		return fmt.Errorf("code must return ([]Item, error)")
	}
	return nil
}

// Item is one workflow item as user code sees it.
type Item struct {
	JSON map[string]any `json:"json"`
}

// Result is one execution's outcome.
type Result struct {
	Items []Item
	// Stderr is whatever the module wrote to standard error, kept for
	// diagnostics and truncated.
	Stderr string
}

// Runner compiles on demand and executes cached artifacts.
type Runner struct {
	compiler Compiler
	cache    Cache
	modules  *ModuleCache
	limits   Limits
}

// NewRunner builds the Code node's execution path.
//
// A nil modules cache is allowed and means every execution translates the
// artifact again into a runtime that is closed after it: correct, and what a
// caller that runs an artifact once wants, since a shared cache has to outlive
// the runner to be worth anything.
func NewRunner(compiler Compiler, cache Cache, modules *ModuleCache, limits Limits) *Runner {
	if cache == nil {
		cache = NewMemoryCache()
	}
	return &Runner{compiler: compiler, cache: cache, modules: modules, limits: limits.orDefault()}
}

// Artifact returns the compiled module for source, compiling it only when the
// cache has no entry for its hash.
//
// This is what keeps compilation an artifact lifecycle rather than part of
// every workflow run: unchanged source is a cache hit, and changed source is a
// different hash and therefore a different artifact.
func (runner *Runner) Artifact(ctx context.Context, source string) (Artifact, error) {
	hash := SourceHash(source)
	if artifact, found := runner.cache.Get(hash); found {
		return artifact, nil
	}
	if runner.compiler == nil || !runner.compiler.Available() {
		return Artifact{}, ErrCompilerUnavailable
	}
	module, err := runner.compiler.Compile(ctx, source)
	if err != nil {
		return Artifact{}, err
	}
	artifact := Artifact{
		Hash: hash, RuntimeVersion: RuntimeVersion,
		Module: module, CompiledAt: time.Now().UTC(),
	}
	runner.cache.Put(artifact)
	return artifact, nil
}

// Run executes source against items.
func (runner *Runner) Run(ctx context.Context, source string, items []Item) (Result, error) {
	artifact, err := runner.Artifact(ctx, source)
	if err != nil {
		return Result{}, err
	}
	return runner.Execute(ctx, artifact, items)
}

// Execute runs one compiled artifact against items.
//
// The module is instantiated with WASI's standard streams and nothing else: no
// preopened directory, no environment, no clock beyond WASI's, and no host
// functions. There is therefore no capability through which user code could
// reach the filesystem, the network, another process, or KilasFlow's own
// database — the Code node is the sandbox with no host binding at all, which
// is why Sandbox is the seam and this is the node's contract on top of it.
//
// Everything between the module and the host lives in Sandbox; what is left
// here is the item batch: one JSON document in, one out.
func (runner *Runner) Execute(ctx context.Context, artifact Artifact, items []Item) (Result, error) {
	if len(artifact.Module) == 0 {
		return Result{}, ErrNotCompiled
	}
	if artifact.RuntimeVersion != RuntimeVersion {
		// A cached artifact from an older contract must be rebuilt rather than
		// run against today's wrapper.
		return Result{}, ErrNotCompiled
	}

	input, err := json.Marshal(items)
	if err != nil {
		return Result{}, fmt.Errorf("encode items: %w", err)
	}

	outcome, err := runner.Sandbox(ctx, artifact.Module, Call{Stdin: input, Limits: runner.limits})
	result := Result{Stderr: outcome.Stderr}
	if err != nil {
		return result, err
	}

	var payload struct {
		Items []Item `json:"items"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(outcome.Stdout), &payload); err != nil {
		return result, &ExecutionError{Detail: "code did not produce a decodable result"}
	}
	if payload.Error != "" {
		return result, &ExecutionError{Detail: payload.Error}
	}
	result.Items = payload.Items
	if result.Items == nil {
		result.Items = []Item{}
	}
	return result, nil
}

// ExecutionError is a user-facing failure from inside the sandbox.
//
// Detail is the sentence a user reads; Cause is the named failure behind it —
// one of ErrTimeLimit, ErrMemoryLimit, ErrOutputLimit or ErrHostCallLimit —
// when the failure was a limit rather than the program itself. Keeping both is
// what lets a caller report the same message as before while answering
// errors.Is for the limit that caused it.
type ExecutionError struct {
	Detail string
	Cause  error
}

func (err *ExecutionError) Unwrap() error { return err.Cause }

func (err *ExecutionError) Error() string { return err.Detail }

func decodeFailure(output []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &payload); err != nil {
		return ""
	}
	return payload.Error
}

// sanitizeRuntimeError keeps host paths and module internals out of a message
// a user will read.
func sanitizeRuntimeError(err error) string {
	message := err.Error()
	if index := strings.Index(message, "\n"); index > 0 {
		message = message[:index]
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

// limitedWriter stops accepting bytes past its limit and remembers that it
// did, so a module cannot fill memory by writing endlessly.
type limitedWriter struct {
	buffer    bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (writer *limitedWriter) Write(payload []byte) (int, error) {
	remaining := writer.limit - writer.written
	if remaining <= 0 {
		writer.truncated = true
		// Reporting success keeps the module from failing on a write error
		// when the real outcome is "produced too much output", which the
		// caller reports with a clearer message.
		return len(payload), nil
	}
	if int64(len(payload)) > remaining {
		writer.buffer.Write(payload[:remaining])
		writer.written = writer.limit
		writer.truncated = true
		return len(payload), nil
	}
	writer.buffer.Write(payload)
	writer.written += int64(len(payload))
	return len(payload), nil
}

func (writer *limitedWriter) Bytes() []byte   { return writer.buffer.Bytes() }
func (writer *limitedWriter) String() string  { return writer.buffer.String() }
func (writer *limitedWriter) Truncated() bool { return writer.truncated }
