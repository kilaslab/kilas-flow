---
id: BUG-3mem9s
title: 'Code node: an HTML-like comment (<!-- or -->) is a syntax error under goja, where V8 reads it as a comment'
status: todo
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

- [ ] Decide between (a) teaching the analyser and the compile step to treat `<!--` anywhere, and `-->` at the start of a line (after whitespace or a block comment), as a line comment, by blanking those comments before goja parses, keeping every line and column where it was so error positions do not move; or (b) keeping the refusal but through `jsrun.Refusal` in words that say what to delete.
- [ ] A golden recorded from Node 24 pins the parse behaviour of both forms, including `<!--` inside a string or template literal, which is not a comment.
- [ ] `make js-diff` shows no "parses only under Node" body.
