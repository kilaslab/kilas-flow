# Contributing to KilasFlow

KilasFlow is an embeddable workflow engine: a Go binary that executes workflows,
a SvelteKit editor it serves, a TypeScript host SDK, and a documentation site.
Bug reports, node packs, documentation corrections and code are all welcome.

Three things are worth knowing before you spend an afternoon on something.

**The documentation describes the system that exists.** A page that oversells
sends its reader into a debugging session; a missing page sends them to the
source. If a feature is planned rather than built, say so or leave it out. The
same rule governs comments: the doc comments in this repository are load-bearing
and are read as statements about today's behaviour, so a stale one is a bug.

**Work is tracked in tickets, and the tickets are in the repository.** KilasFlow
uses [Pine](https://github.com/underworld14/pine), which keeps tickets in
`.pine/tickets/` as Markdown committed with the code. The reasoning, the rejected
alternatives and the file-change evidence behind any change in the tree can be
read from a clone, without an account. A maintainer opens the ticket; you do not
need one to open an issue or a pull request.

**Security problems never go in a public issue.** See [SECURITY.md](SECURITY.md).

## Setting up

Toolchain: Go 1.27 (from `go.mod`), Node and pnpm (pinned in `devbox.json` and by
the `packageManager` field in `web/package.json`), and Docker if you want the
image. [devbox](https://www.jetify.com/devbox) is supported but not required —
the Makefile works with a local Go, Node and pnpm otherwise.

```bash
git clone https://github.com/kilaslab/kilas-flow.git
cd kilas-flow
make setup   # go mod download, Air, and the web/ dependencies
make dev     # Go on :8080, Vite on :5173
```

Open **<http://localhost:5173>** — the Vite port, not the Go one.

`make setup` deliberately installs only `web/`, because that is what running the
product needs. The SDK and the documentation site are separate pnpm projects and
only matter if you touch them:

```bash
cd sdk  && pnpm install   # @kilasflow/sdk
cd docs && pnpm install   # the documentation site
```

For a single binary with the editor embedded, `make build-all` then
`./bin/kilasflow` on <http://localhost:8080>. `make help` lists every target.

## The checks your change has to pass

CI runs all of these. Run the first three before you push; the generated-file
checks only matter if your change reaches the API surface or the configuration.

| Command | What it proves |
| --- | --- |
| `make lint` | `go vet`, `gofmt`, `svelte-check`, and the web test suite |
| `make test` | `go test ./... -race`; the PostgreSQL and MySQL packages skip without a DSN |
| `make docs-build` | the documentation site builds and every internal link resolves |
| `make generate-api-check` | the committed web API client matches the served OpenAPI document |
| `make generate-types-check` | the committed SDK types match the served OpenAPI document |
| `make generate-api-reference-check` | the committed API reference pages match the served document |
| `make generate-config-reference-check` | the configuration reference and `config.example.yaml` match the `Config` structs |
| `make sdk-package-check` | the packed SDK installs into a scratch project and typechecks under `bundler`, `node16` and `nodenext` |
| `make sdk-example-check` | `examples/host-page` runs against the packed SDK: installed into a scratch copy outside the repository, booted against a stub KilasFlow, with raw `..%2f` probes at its static route |
| `make sdk-release-check` | every gate a release has to pass, in one command (`sdk-check`, `sdk-test`, `sdk-build`, `sdk-version-check`, `generate-types-check`, `sdk-package-check`, `sdk-example-check`) |
| `make test-e2e` | the Playwright suite, against a real binary and SPA |
| `make test-e2e-capstone` | the epic acceptance capstone, against a docker image (on demand) |
| `make e2e-capstone-report` | the capstone's verdict: exit 0 passed, 1 failed, 2 unavailable |
| `make e2e-capstone-scheduled` | the same verdict as `.github/workflows/capstone.yml` reads it: an unavailable third party does not fail the run |

The `generate-*-check` targets exist because a hand-maintained copy drifts, and
the failure is silent: a client that compiles and calls an endpoint that no
longer exists. If you change a request or response type, run the matching
`make generate-*` and commit the result in the same change.

`make test` runs the database-gated packages against the DSN in
`KILASFLOW_TEST_POSTGRES_DSN` when it is set. Those tests drop every KilasFlow
table on entry, so point it at a scratch database and never at anything you care
about.

`make test-e2e` needs `pnpm install` in `e2e/` and a built binary. It is slow and
CI runs it on your pull request, so it is not part of the local loop.

## Tests

Test behaviour, boundaries and error paths. A test that restates the
implementation — a log line, a copied field, a mock echoing its own argument —
passes for the wrong reason and breaks on the first refactor, which makes it
worse than no test at all. Where an existing test pins implementation detail,
rewriting or deleting it belongs to the change that broke it, not to a later
cleanup.

## Commits

- Subject in the imperative: "add the datastore node", not "added" or "adds".
- Maintainers prefix the ticket id — `BUG-341sxn: …`, `FEAT-a5fhjw: …` — and use
  `chore(pine): …` for ticket bookkeeping. An external contribution needs no id.
- The body carries the *why*; the diff already says what moved.
- Branch off `main` and open the pull request against `main`.

## Code

- **Go**: stdlib conventions, `gofmt`, and a written reason for every new
  dependency. Keep the direction of the imports: the engine does not import
  `internal/api` or `internal/ai` — it reaches persistence through the
  `internal/repository` interfaces and the agent runtime through an injected
  `ai.AgentRuntime`.
- **Nothing vendored or copied before reading `.pine/memory/licensing.md`.** That
  file is the boundary: n8n's fixtures are LicenseRef-n8n-sustainable-use and the
  WAHA templates carry no licence at all, which is why neither is committed.
- **Frontend**: relative URLs only — `fetch('/api/v1/workflows')`, never an
  absolute origin. Production serves the API and the SPA from one origin, and an
  embedded editor is mounted on whichever origin the host chooses.
- **Replace, don't shim.** When a path changes, every caller moves in the same
  change: no aliases, no re-exports, no deprecated wrappers.
- Comments explain why something is the way it is, above all where the obvious
  implementation would be wrong. The code around it is the style guide.

## Documentation

`docs/` is an Astro Starlight site, a sibling of `web/` and never part of the
binary. Dropping a page into a directory puts it in the sidebar; only the Start
section's order is written by hand in `docs/astro.config.mjs`.

A change a reader would notice needs its page updated in the same pull request.
[Contributing to these docs](docs/src/content/docs/contributing.md) covers the
site itself.

## Pull requests

One change per pull request, with a description that says what you verified and
how you verified it. CI has to be green. Review is by the maintainer, and expect
questions about the reasoning rather than the formatting.

No CLA and no DCO sign-off are required. By opening a pull request you agree your
contribution is licensed under Apache-2.0, like the rest of the repository.

## Where to ask

- Questions and bug reports:
  [issues](https://github.com/kilaslab/kilas-flow/issues) — there are forms for
  bugs and features, and a `question` label.
- Anything security-related: [SECURITY.md](SECURITY.md), privately.
- Behaviour in issues, pull requests or any other project space:
  [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
