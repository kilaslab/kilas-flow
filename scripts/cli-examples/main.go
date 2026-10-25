// Command cli-examples runs the shell examples in docs/.../reference/cli.md
// against a booted server and fails when one does not end the way its page says.
//
// Usage:
//
//	go run ./scripts/cli-examples -binary bin/kilasflow
//
// The page is what an agent copies from, and it drifted from the binary more
// than once: a verb that exited 4 where the page showed it working, a claim about
// `--url ”` the binary contradicted, ids in a shape no server issues. The parse
// test in internal/cli keeps every example's verb, flags and arguments honest
// without a server; this is the half that needs one. It boots the binary it is
// given against a fresh SQLite database, seeds the resources an example names
// through shell variables ($WORKFLOW_ID, $EXECUTION_ID, ...) by driving the same
// binary, and runs each example with `sh -c`. An example ends with exit 0 unless
// its trailing comment says `# exit N`, and the exit code is compared with it.
//
// It never relaxes the server's egress guard. The workflow the examples call
// reads the server's own health endpoint, so the one address it needs is
// admitted through outbound.allowed_private_endpoints, which names a host and a
// port; outbound.allow_private_networks stays off. After the examples it checks
// the catalogue the server really serves against the size node-registry.md
// states, which closes the gap a test in the repository cannot: whether
// internal/shippedcatalogue still registers what run() registers.
//
// This command lives in its own directory for the same reason
// scripts/skills-index does: scripts/config-reference.go already declares
// func main in package main.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kilaslab/kilas-flow/internal/docexamples"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

const (
	// defaultPage is the page whose examples are run.
	defaultPage = "docs/src/content/docs/reference/cli.md"
	// registryPage states the catalogue's size between generated markers.
	registryPage = "docs/src/content/docs/concepts/node-registry.md"

	catalogueBegin = "<!-- generated:catalogue-size -->"
	catalogueEnd   = "<!-- /generated:catalogue-size -->"

	// floor is the fewest examples a run may execute. A page that lost its
	// examples, or an extractor that stopped finding them, would otherwise pass
	// by running nothing.
	floor = 60

	// commandTimeout bounds one example. The CLI bounds itself at thirty seconds,
	// so an example that outlives this is stuck rather than slow.
	commandTimeout = 90 * time.Second

	// killDelay is how long the runner waits, after killing a stuck command, for
	// its output to close before it stops waiting. A child that left the command's
	// process group can hold the pipe open long after the command is gone.
	killDelay = 5 * time.Second

	// serverURLPlaceholder is replaced in the fixtures with the booted server's
	// address, so the workflow the examples run calls the server it runs on.
	serverURLPlaceholder = "{{SERVER_URL}}"
)

//go:embed testdata/*.json
var fixtures embed.FS

type options struct {
	binary string
	page   string
	keep   bool
}

func main() {
	binary := flag.String("binary", "", "the kilasflow binary to boot and drive, e.g. bin/kilasflow")
	page := flag.String("page", defaultPage, "the markdown page whose examples are run, relative to the repository root")
	keep := flag.Bool("keep", false, "keep the scratch directory and the server's log after the run")
	flag.Parse()

	if *binary == "" {
		fmt.Fprintln(os.Stderr, "cli-examples: -binary is required (build it with `make build`)")
		os.Exit(2)
	}
	// An interrupt or a termination ends the run the way a failure does: the
	// commands in flight are killed, and run returns, so the server is stopped and
	// the scratch directory removed. Without this the process dies where it stands
	// and leaves both behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, options{binary: *binary, page: *page, keep: *keep})
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cli-examples: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options) error {
	root, err := splice.RepoRoot()
	if err != nil {
		return err
	}
	binary, err := filepath.Abs(opts.binary)
	if err != nil {
		return err
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		return fmt.Errorf("%s is not a binary; build it with `make build`", opts.binary)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(opts.page)))
	if err != nil {
		return fmt.Errorf("read %s: %w", opts.page, err)
	}
	examples := docexamples.Extract(string(raw))
	runnable := 0
	for _, example := range examples {
		if !example.ServerPath {
			runnable++
		}
	}
	if runnable < floor {
		return fmt.Errorf("%s has %d runnable examples, fewer than the %d this gate insists on", opts.page, runnable, floor)
	}

	work, err := os.MkdirTemp("", "kilasflow-cli-examples-")
	if err != nil {
		return err
	}
	if opts.keep {
		fmt.Printf("cli-examples: keeping %s\n", work)
	} else {
		defer os.RemoveAll(work)
	}

	server, err := startServer(ctx, binary, work)
	if err != nil {
		return err
	}
	defer server.stop()

	r, err := newRunner(ctx, binary, work, server.url, os.Stdout)
	if err != nil {
		return err
	}
	if err := r.catalogue(root); err != nil {
		r.fail("%v", err)
	}
	r.runAll(examples, opts.page)
	if ctx.Err() != nil {
		return fmt.Errorf("interrupted after %d examples", r.executed)
	}

	return r.verdict(opts.page, server.logPath, opts.keep)
}

// runner executes examples against one booted server and keeps the verdict.
type runner struct {
	// ctx ends when the run is interrupted; every command the runner starts is
	// killed with it.
	ctx     context.Context
	binary  string
	scratch string
	binDir  string
	url     string
	out     io.Writer

	// timeout bounds one command, and waitDelay is how long the runner waits for a
	// killed command's output to close before it stops waiting.
	timeout   time.Duration
	waitDelay time.Duration

	counter  int
	executed int
	skipped  []string
	failures []string
}

func newRunner(ctx context.Context, binary, work, url string, out io.Writer) (*runner, error) {
	scratch := filepath.Join(work, "scratch")
	binDir := filepath.Join(work, "bin")
	for _, dir := range []string{scratch, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// `skills install` writes into the nearest checkout above the working
	// directory, so a scratch directory inside one would write into somebody's
	// repository.
	for dir := scratch; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return nil, fmt.Errorf("the scratch directory %s sits inside the git checkout %s; set TMPDIR elsewhere", scratch, dir)
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	// The examples say `kilasflow`, whatever the binary under test is called.
	if err := os.Symlink(binary, filepath.Join(binDir, "kilasflow")); err != nil {
		return nil, err
	}
	// `pack validate ./packs/telegram` runs as written, from the scratch directory.
	root, err := splice.RepoRoot()
	if err != nil {
		return nil, err
	}
	if err := os.Symlink(filepath.Join(root, "packs"), filepath.Join(scratch, "packs")); err != nil {
		return nil, err
	}
	if err := writeDirectoryPack(filepath.Join(root, "packs", "telegram", "pack.json"), filepath.Join(scratch, "my-pack")); err != nil {
		return nil, err
	}
	if err := writeExportDirectory(filepath.Join(scratch, "exports")); err != nil {
		return nil, err
	}
	entries, err := fixtures.ReadDir("testdata")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		content, err := fixtures.ReadFile("testdata/" + entry.Name())
		if err != nil {
			return nil, err
		}
		content = bytes.ReplaceAll(content, []byte(serverURLPlaceholder), []byte(url))
		if err := os.WriteFile(filepath.Join(scratch, entry.Name()), content, 0o644); err != nil {
			return nil, err
		}
	}

	// The reference page's directory example imports every .json under
	// exports/, so the scratch tree carries one directory with a known-good
	// n8n document in it rather than only the flat fixtures above.
	exportsDir := filepath.Join(scratch, "exports")
	if err := os.MkdirAll(exportsDir, 0o755); err != nil {
		return nil, err
	}
	exported, err := os.ReadFile(filepath.Join(scratch, "n8n.json"))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(exportsDir, "n8n.json"), exported, 0o644); err != nil {
		return nil, err
	}

	return &runner{
		ctx: ctx, binary: binary, scratch: scratch, binDir: binDir, url: url, out: out,
		timeout: commandTimeout, waitDelay: killDelay,
	}, nil
}

// writeDirectoryPack lays a pack out the way an operator installs one: a
// directory holding pack.json and pack.sha256, the hex SHA-256 of the manifest.
// The repository's embedded packs ship without the sidecar, so the page's
// directory example needs one made from a manifest that is known to be valid.
func writeDirectoryPack(manifestPath, dir string) error {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), manifest, 0o644); err != nil {
		return err
	}
	digest := sha256.Sum256(manifest)

	return os.WriteFile(filepath.Join(dir, "pack.sha256"), []byte(hex.EncodeToString(digest[:])+"\n"), 0o644)
}

// writeExportDirectory lays the n8n backup form out: a directory of bare n8n
// documents, the shape `workflow import --dir` reads and the one
// `workflow export --all --out-dir` writes. Both files come from the page's
// own n8n fixture, so what the example imports is what the page shows.
func writeExportDirectory(dir string) error {
	document, err := fixtures.ReadFile("testdata/n8n.json")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"orders.json", "refunds.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), document, 0o644); err != nil {
			return err
		}
	}

	return nil
}

func (r *runner) fail(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// environ is the environment every example runs in: the binary first on PATH, a
// home directory of its own, and the server and the credential the page's
// examples take from the environment. Nothing is inherited, so a developer's own
// KILASFLOW_* settings cannot change what the page is held to.
func (r *runner) environ(extra map[string]string) []string {
	env := []string{
		"PATH=" + r.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + r.scratch,
		"XDG_CONFIG_HOME=" + filepath.Join(r.scratch, ".config"),
		"KILASFLOW_URL=" + r.url,
		"KILASFLOW_TOKEN=" + operatorKey,
		"TOKEN=" + operatorKey,
	}
	for name, value := range extra {
		env = append(env, name+"="+value)
	}

	return env
}

// shell runs one command line with sh and returns its exit code and output.
func (r *runner) shell(text string, extra map[string]string) (code int, output string, err error) {
	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()

	command := exec.CommandContext(ctx, "sh", "-c", text)
	r.bound(command, extra)
	var combined limitedBuffer
	command.Stdout, command.Stderr = &combined, &combined

	runErr := command.Run()
	switch {
	case runErr == nil:
		return 0, combined.String(), nil
	case r.ctx.Err() != nil:
		return -1, combined.String(), fmt.Errorf("interrupted")
	case ctx.Err() != nil:
		return -1, combined.String(), fmt.Errorf("did not finish in %s", r.timeout)
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return exit.ExitCode(), combined.String(), nil
	}

	return -1, combined.String(), runErr
}

// cli runs the binary under test directly, for seeding, and returns its trimmed
// stdout. Seeding is part of the proof as well: a verb that cannot create what
// the examples act on fails the run here.
func (r *runner) cli(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()

	command := exec.CommandContext(ctx, r.binary, args...)
	r.bound(command, nil)
	// stdout is read whole, because a caller parses it: `node list` is the whole
	// catalogue, far past what an example's output is kept to.
	var stdout bytes.Buffer
	var stderr limitedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if r.ctx.Err() != nil {
			return "", fmt.Errorf("interrupted while seeding with `kilasflow %s`", strings.Join(args, " "))
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("did not finish in %s", r.timeout)
		}

		return "", fmt.Errorf("seeding with `kilasflow %s` failed: %w\n%s%s",
			strings.Join(args, " "), err, limit(stdout.String()), stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// bound sets what every command the runner starts shares: where it runs, what
// it sees, and how it is stopped (see stopWithGroup).
func (r *runner) bound(command *exec.Cmd, extra map[string]string) {
	command.Dir = r.scratch
	command.Env = r.environ(extra)
	stopWithGroup(command, r.waitDelay)
}

// runAll runs the page's examples block by block, seeding each block afresh.
func (r *runner) runAll(examples []docexamples.Example, page string) {
	var blocks [][]docexamples.Example
	for _, example := range examples {
		if len(blocks) == 0 || blocks[len(blocks)-1][0].Block != example.Block {
			blocks = append(blocks, nil)
		}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], example)
	}

	for _, block := range blocks {
		if r.ctx.Err() != nil {
			return
		}
		texts := make([]string, 0, len(block))
		for _, example := range block {
			if !example.ServerPath {
				texts = append(texts, example.Text)
			}
		}
		needed, unknown := needsOf(texts)
		if len(unknown) > 0 {
			r.fail("%s:%d the block uses %s, which the runner cannot seed", page, block[0].Line, strings.Join(unknown, ", "))

			continue
		}
		vars, err := r.seed(needed)
		if err != nil {
			r.fail("%s:%d %v", page, block[0].Line, err)

			continue
		}

		for _, example := range block {
			if r.ctx.Err() != nil {
				return
			}
			if example.ServerPath {
				// Running it would start a second server, which does not return.
				r.skipped = append(r.skipped, fmt.Sprintf("%s:%d %s", page, example.Line, example.Text))

				continue
			}
			r.execute(page, example, vars)
		}
	}
}

// execute runs one example and compares its exit code with the page's claim.
func (r *runner) execute(page string, example docexamples.Example, seeded map[string]string) {
	vars := make(map[string]string, len(seeded)+1)
	for name, value := range seeded {
		vars[name] = value
	}
	if usesVariable(example.Text, "CURSOR") {
		cursor, err := r.cursor(example, vars)
		if err != nil {
			r.fail("%s:%d %s: %v", page, example.Line, example.Text, err)

			return
		}
		vars["CURSOR"] = cursor
	}

	code, output, err := r.shell(example.Text, vars)
	if r.ctx.Err() != nil {
		// The run was interrupted: this example did not fail, it was stopped.
		return
	}
	r.executed++
	where := fmt.Sprintf("%s:%d", page, example.Line)
	switch {
	case err != nil:
		fmt.Fprintf(r.out, "FAIL %s  %s\n", where, example.Text)
		r.fail("%s %s: %v\n%s", where, example.Text, err, indent(output))
	case code != example.ExpectedExit:
		fmt.Fprintf(r.out, "FAIL %s  exit %d, the page says %d  %s\n", where, code, example.ExpectedExit, example.Text)
		r.fail("%s %s\n  exited %d; the page says %d\n%s", where, example.Text, code, example.ExpectedExit, indent(output))
	default:
		fmt.Fprintf(r.out, "ok   %s  exit %d  %s\n", where, code, firstLine(example.Text))
	}
}

// verdict prints the summary and returns an error when anything failed.
func (r *runner) verdict(page, logPath string, keep bool) error {
	for _, entry := range r.skipped {
		fmt.Fprintf(r.out, "skip %s (starts a server)\n", entry)
	}
	fmt.Fprintf(r.out, "cli-examples: %d executed, %d skipped, %d failed\n", r.executed, len(r.skipped), len(r.failures))

	if r.executed < floor {
		r.fail("only %d examples ran, fewer than the %d this gate insists on", r.executed, floor)
	}
	if len(r.failures) == 0 {
		return nil
	}
	hint := ""
	if keep {
		hint = "\nthe server's log is " + logPath
	}

	return fmt.Errorf("%d problem(s) with %s:\n\n%s%s", len(r.failures), page, strings.Join(r.failures, "\n\n"), hint)
}

// catalogue compares the catalogue the booted server serves with the size
// node-registry.md states. The page's sentence is generated from
// internal/shippedcatalogue and held to it by `make generate-docs-facts-check`,
// so this closes the loop: the registrations that package makes are the ones
// run() makes.
func (r *runner) catalogue(root string) error {
	out, err := r.cli("node", "list", "--json")
	if err != nil {
		return err
	}
	var envelope struct {
		Data struct {
			Items []servedNode `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		return fmt.Errorf("decode `node list --json`: %w", err)
	}
	served := shippedcatalogue.RenderSentence(countServed(envelope.Data.Items))

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(registryPage)))
	if err != nil {
		return err
	}
	stated, ok := generatedBlock(string(raw), catalogueBegin, catalogueEnd)
	if !ok {
		return fmt.Errorf("%s carries no %s block", registryPage, catalogueBegin)
	}
	if stated != served {
		return fmt.Errorf("the booted server serves a different catalogue from the one %s states.\n"+
			"  served: %s\n  page:   %s\n"+
			"run `make generate-docs-facts`; if the page was already regenerated, internal/shippedcatalogue.Register "+
			"has fallen behind the registrations in cmd/kilasflow run()", registryPage, served, stated)
	}
	fmt.Fprintf(r.out, "ok   the served catalogue matches %s\n", registryPage)

	return nil
}

// servedNode is the part of a node-types entry the catalogue count reads.
type servedNode struct {
	Type   string `json:"type"`
	Source string `json:"source"`
}

// countServed measures a served catalogue the way shippedcatalogue.Count
// measures a registry: one pair per entry, one type however many versions it is
// served at, split by where the node was registered from. A sidecar node is an
// operator's own installation and belongs to neither side.
func countServed(nodes []servedNode) shippedcatalogue.Counts {
	types := map[string]map[string]struct{}{"builtin": {}, "pack": {}}
	pairs := map[string]int{}
	for _, entry := range nodes {
		seen, counted := types[entry.Source]
		if !counted {
			continue
		}
		seen[entry.Type] = struct{}{}
		pairs[entry.Source]++
	}

	return shippedcatalogue.Counts{
		Compiled: shippedcatalogue.Size{Types: len(types["builtin"]), Pairs: pairs["builtin"]},
		Packs:    shippedcatalogue.Size{Types: len(types["pack"]), Pairs: pairs["pack"]},
	}
}

// generatedBlock is the trimmed text between a page's two markers.
func generatedBlock(page, begin, end string) (string, bool) {
	if strings.Count(page, begin) != 1 || strings.Count(page, end) != 1 {
		return "", false
	}
	from := strings.Index(page, begin) + len(begin)
	to := strings.Index(page, end)
	if to < from {
		return "", false
	}

	return strings.TrimSpace(page[from:to]), true
}

// limitedBuffer keeps the first few kilobytes of an example's output: enough to
// explain a failure, bounded so a runaway example cannot fill memory.
type limitedBuffer struct {
	buf bytes.Buffer
}

const outputLimit = 4096

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := outputLimit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

// limit cuts text to what a failure message should carry.
func limit(text string) string {
	if len(text) > outputLimit {
		return text[:outputLimit] + "…"
	}

	return text
}

func indent(text string) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return "    (no output)"
	}

	return "    " + strings.ReplaceAll(text, "\n", "\n    ")
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")

	return line
}
