---
id: BUG-3mem9s
title: 'Code node: an HTML-like comment (<!-- or -->) is a syntax error under goja, where V8 reads it as a comment'
status: done
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T14:09:31Z"
updated: "2026-09-24T15:30:55Z"
---

# Description

Found by `make js-diff` over the Code-node compatibility corpus (FEAT-afkx3k): one body among the 500 most-viewed n8n templates has a line starting with `<!--`, and KilasFlow refuses the whole node at import and validation with `SyntaxError: Unexpected token <`. V8 parses it, so n8n runs it.

JavaScript's web-compatibility grammar (ECMAScript Annex B, "HTML-like comments") makes `<!--` start a comment that runs to the end of the line, and makes `-->` at the start of a line do the same, in scripts (not modules). n8n compiles a Code node's body as a script, so an HTML comment left in pasted code is harmless there. goja's parser does not implement Annex B HTML-like comments, so the analyser (which parses with goja) reports a syntax error.

This is a real engine difference, not a shim gap: the same text is a program in V8 and not in goja. It fails loudly (a refusal at import), never silently.

# Minimal reproduction

All-items mode:

```js
<!-- an HTML-style comment left in pasted code
return [{ json: { ok: true } }]
--> and one closing it
```

- Node 24 / n8n: `[{ ok: true }]`
- KilasFlow: `SyntaxError: Unexpected token < [line 1, column 1]` at import, at validate and at run

# Acceptance Criteria

- [x] Decide between (a) teaching the analyser and the compile step to treat `<!--` anywhere, and `-->` at the start of a line (after whitespace or a block comment), as a line comment, by blanking those comments before goja parses, keeping every line and column where it was so error positions do not move; or (b) keeping the refusal but through `jsrun.Refusal` in words that say what to delete.
- [x] A golden recorded from Node 24 pins the parse behaviour of both forms, including `<!--` inside a string or template literal, which is not a comment.
- [x] `make js-diff` shows no "parses only under Node" body.

# Notes

## Plan (2026-09-24, Task 13 of EPIC-tjnr1z)

- Option (a): read the comments as V8 does. A lexical pre-pass over the user's code (only when the text has a `<!--`, or a `-->` that could start a line) finds the HTML-like comments in code position and blanks them to spaces, keeping every byte offset, line and column. The analyser and the compile step share the one parse (`parseWrapped`), so import, validate and run agree by construction.
- The pre-pass is a small JavaScript lexer: strings, templates (with `${…}` nesting), regular expressions, comments. The one thing a lexer alone cannot know is whether a `/` starts a regular expression; so after goja parses the blanked text, the regular expressions goja found must be exactly the ones the lexer found. If they differ, the body is refused through Refusal, never run with a guess.
- Function source (Function.prototype.toString) keeps the original text, comments included.
- A golden recorded from Node 24 (vm.Script over the same wrapper) pins both forms, and `<!--` / `-->` inside strings, templates, regular expressions and comments staying code.

## Progress (2026-09-24)

- Decided (a): the comments are read as V8 reads them. internal/jsrun/analyze_html.go lexes a body that could hold one (`mayHoldHTMLComment`: any `<!--`, or a `-->` with only white space or a block comment's end before it on its line) and blanks each HTML-like comment in code to spaces, byte for byte. `parseWrapped`, the one parse the analyser (import, validate) and the compile step (run) share, parses the blanked text, so the three agree by construction and every line and column stays where it was.
- Written in Go rather than reusing goja's scanner: goja's lexer is unexported, and is driven by its parser. The lexer here knows strings, templates with nested `${}`, regular expressions (character classes, escapes, flags), line and block comments, `<<` (so `<<!--` stays a shift, as in V8), U+2028/U+2029 as line ends, and a block comment spanning lines leaving the next token at a line start.
- The regex-or-division question is decided from the token before, as parsers conventionally do; after goja parses the blanked text, `checkHTMLComments` holds the regular expressions goja found to exactly the ones the lexer read. If they differ the lexer read part of the body differently from the parser, and the body is refused through the one refusal sentence ("has code around a <!-- or --> that this server cannot read unambiguously"; reworded in fix round 1), never run on a guess. Pinned by TestAnHTMLLikeCommentTheLexerCannotReadIsRefused (a function expression followed by `/ 2 <!--x / 1`).
- `x<!--y` used to parse under goja as `x < !--y` and compute something else silently; it now reads as V8 reads it (golden probe).
- Function.prototype.toString keeps the original text, comments included: the Source goja keeps for each function, arrow, class and static block is swapped back to the original text after the parse.
- Golden: internal/jsrun/testdata/parity/html-comments.json, recorded by scripts/js-parity/record-engine.mjs (dev-only, Node 24, vm.Script over the same wrapper). 27 probes: both forms, first line, after spaces/tabs/one-line and multi-line block comments/\r\n, `i-->0` and `i --> 1` mid-line, `-- >`, `<!--`/`-->` inside strings, templates, `${}`, nested templates, regular expressions (incl. after `if (…)`, after a block, escaped slashes), line and block comments, a line separator ending the comment, a comment eating a closing brace, strict mode, and one that is a syntax error in both.
- Corpus: 3314/18 moves syntax -> ok (baseline regenerated in the same commit). `make js-diff`: "parses only under Node" 1 -> 0, "same" 220 -> 221.
- Not covered: code a script builds at run time and hands to eval() or new Function() is parsed by goja itself, without this pre-pass.

## Fix round 1 (2026-09-24, review of Task 13)

- **Labels and cases.** The lexer took a `{` after any `:` as an object literal. So a labelled block or a `case x: {}` followed by a regular expression holding `<!--` was refused as a syntax error, though goja and V8 run it. The lexer now counts pending `?` per brace level. A `:` that answers none, where the innermost brace is a block or there is none, ends a label or a case, and the `{` after it opens a block. `??` and `??=` are read as tokens, so they don't count as `?`. Five golden probes cover it: a labelled block and a case block before a regex holding `<!--`, and an object as a property value, in a conditional, and after `??`, each divided and followed by `<!--`.
- **Refusal wording.** When the lexer and the parser disagree, the body may hold no comment at all: `<!--` may sit in a string next to code the lexer misreads. The refusal now says "this node's code has code around a <!-- or --> that this server cannot read unambiguously (line N), which this server does not run. …". Pinned by TestCodeAroundAnHTMLMarkerThatCannotBeReadIsRefusedAsSuch. The JavaScript guide says the same.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `5deaf667` (last commit at or before ticket created 2026-09-24)
- Commits (3):
  - `04182b11` — merge: BUG-jwhj6y and BUG-3mem9s engine errors worded as V8 words them, and HTML-like comments read as V8 reads them
  - `d06d0978` — BUG-3mem9s: a labelled block or a case block before a regular expression with <!-- runs, and a refusal names the code around the marker rather than a comment
  - `8d659960` — BUG-3mem9s: an HTML-like comment in a Code node's JavaScript reads as V8 reads it, at import, validate and run, with every line and column kept
- Files changed (the ticket's own commits, 6fe03df6edb18fe0d9365b6390122c43513cd6d1..04182b11fa57d1c0a3c7dd7e5d98a3629b38339b):

```
 .pine/tickets/BUG-2vcwjf.md                       |  35 +++
 .pine/tickets/BUG-3mem9s.md                       |  33 ++-
 .pine/tickets/BUG-46g75c.md                       |  34 +++
 .pine/tickets/BUG-jwhj6y.md                       |  52 +++-
 docs/src/content/docs/guides/code-javascript.md   |  14 +-
 internal/jsrun/analyze.go                         |  32 ++-
 internal/jsrun/analyze_errors.go                  | 187 ++++++++++++++
 internal/jsrun/analyze_html.go                    | 575 ++++++++++++++++++++++++++++++++++++++++++
 internal/jsrun/analyze_html_test.go               |  63 +++++
 internal/jsrun/console_test.go                    |   2 +-
 internal/jsrun/corpus/BASELINE.md                 |  13 +-
 internal/jsrun/corpus/baseline.json               |  27 +-
 internal/jsrun/corpus/jsdiff_test.go              |  13 +-
 internal/jsrun/engine.go                          |   7 +
 internal/jsrun/htmlcomments_test.go               | 120 +++++++++
 internal/jsrun/js/modules/errors.js               | 279 ++++++++++++++++++++
 internal/jsrun/js/runtime.js                      |  12 +-
 internal/jsrun/modules.go                         |   2 +-
 internal/jsrun/programs.go                        |   4 +
 internal/jsrun/testdata/parity/errors.json        | 293 +++++++++++++++++++++
 internal/jsrun/testdata/parity/html-comments.json |  37 +++
 internal/jsrun/wording.go                         | 149 +++++++++++
 internal/jsrun/wording_internal_test.go           |  48 ++++
 internal/jsrun/wording_test.go                    | 268 ++++++++++++++++++++
 internal/jsrun/wrapper.go                         |  12 +-
 scripts/js-parity/record-engine.mjs               | 322 +++++++++++++++++++++++
 26 files changed, 2595 insertions(+), 38 deletions(-)
```
