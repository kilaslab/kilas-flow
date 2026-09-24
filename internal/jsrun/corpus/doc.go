// Package corpus measures the JavaScript runtime against real n8n code: the
// JavaScript Code nodes and Sort code comparators of the most-viewed public
// templates on n8n.io (EPIC-tjnr1z, P7).
//
// It is a measuring instrument and nothing ships from it. Its tests do all
// the work:
//
//   - The scoreboard (TestCodeCorpusScoreboard) says, for every body, whether
//     it parses, whether the analyser accepts it, and whether it runs without
//     an error on synthesised input: the template's own pinned data when it
//     has some, otherwise an item shaped from the fields the body reads. The
//     result is committed as BASELINE.md and baseline.json, and a body that
//     moves without the baseline moving with it fails the test.
//   - The differential run (TestJSDiff, `make js-diff`) runs the same bodies
//     on the same input under Node.js, through a roots harness KilasFlow
//     wrote (scripts/js-diff/harness.mjs), and diffs what the two return. It
//     is how a semantic difference between goja and V8 is found before a
//     user finds it. It needs Node, so it is dev-only and never in CI.
//
// The bodies are other people's work, published as templates, so they are
// fetched by scripts/code-corpus-sync.sh into the gitignored fixtures/
// directory and never committed; MANIFEST.json pins each template by id and
// digest. Nothing committed here carries a template's code, its node names or
// its data: the baseline names a body by template id and node position. The
// one exception is a module name: the analyser's refusal of an unshipped
// module quotes it (`requires the module "youtube-transcript"`), and the
// baseline records refusals in the analyser's words.
//
// Everything that reads a file lives in a _test.go file. The rule that keeps
// the runtime away from the filesystem (internal/guardrails) covers every
// package under internal/jsrun, and this one has no reason to be an exception.
package corpus
