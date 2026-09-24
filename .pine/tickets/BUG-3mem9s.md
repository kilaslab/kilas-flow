---
id: BUG-3mem9s
title: 'Code node: an HTML-like comment (<!-- or -->) is a syntax error under goja, where V8 reads it as a comment'
status: testing
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T14:09:31Z"
updated: "2026-09-24T14:09:31Z"
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
- The regex-or-division question is decided from the token before, as parsers conventionally do; after goja parses the blanked text, `checkHTMLComments` holds the regular expressions goja found to exactly the ones the lexer read. If they differ the lexer read part of the body differently from the parser, and the body is refused through the one refusal sentence ("has an HTML-like comment (<!-- or -->) this server cannot tell apart from the code around it"), never run on a guess. Pinned by TestAnHTMLLikeCommentTheLexerCannotReadIsRefused (a function expression followed by `/ 2 <!--x / 1`).
- `x<!--y` used to parse under goja as `x < !--y` and compute something else silently; it now reads as V8 reads it (golden probe).
- Function.prototype.toString keeps the original text, comments included: the Source goja keeps for each function, arrow, class and static block is swapped back to the original text after the parse.
- Golden: internal/jsrun/testdata/parity/html-comments.json, recorded by scripts/js-parity/record-engine.mjs (dev-only, Node 24, vm.Script over the same wrapper). 27 probes: both forms, first line, after spaces/tabs/one-line and multi-line block comments/\r\n, `i-->0` and `i --> 1` mid-line, `-- >`, `<!--`/`-->` inside strings, templates, `${}`, nested templates, regular expressions (incl. after `if (…)`, after a block, escaped slashes), line and block comments, a line separator ending the comment, a comment eating a closing brace, strict mode, and one that is a syntax error in both.
- Corpus: 3314/18 moves syntax -> ok (baseline regenerated in the same commit). `make js-diff`: "parses only under Node" 1 -> 0, "same" 220 -> 221.
- Not covered: code a script builds at run time and hands to eval() or new Function() is parsed by goja itself, without this pre-pass.
