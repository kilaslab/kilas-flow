---
id: BUG-ngt25j
title: 'Code node and sticky content use a single-line input: Enter does nothing, pasted multi-line code collapses'
status: done
priority: high
labels:
    - editor
    - code-node
    - ndv
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-25T11:23:00Z"
---

# Description

The Go Code node cannot be written in the editor at all: pasted multi-line code becomes one line that fails to compile. This also blocks the JS runtime epic's usability.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Code node opens a full multi-line code editor with line numbers, syntax highlighting and completions.

# Steps to Reproduce

1. Add a Code node and open it. 2. Paste `out := []Item{}\nfor _, it := range items {\n\tout = append(out, it)\n}\nreturn out, nil` into "Go code", or press Enter to start a new line. 3. Save and Execute.

# Expected

A multi-line code editor (at least a monospace textarea) for code parameters and sticky content.

# Actual

The field is `<input type=text>` (28 px high). The paste becomes `out := []Item{} for _, it := range items { \tout = append(out, it) } return out, nil`, and Enter inserts nothing. The run fails: `node "Code": code did not compile: ... ./main.go:32:17: syntax error: unexpected keyword for at end of statement`. The Sticky Note "Content" field has the same problem, so notes cannot be multi-line markdown from the editor.

# Acceptance Criteria
- [x] Code parameters use a multi-line code editor with monospace font, indentation and line numbers (at minimum a textarea)
- [x] Sticky note content is multi-line
- [x] Pasting multi-line code preserves the newlines, and a pasted Go body compiles

# Implementation Plan

Declare `typeOptions.rows` (or an `editor: code` hint) on nodes/code.go `code` and annotation.go `content`, and render a CodeMirror editor for code kinds. As a stopgap, render any string property whose key is code/content as a textarea.

# Notes

Related (from the audit): none

## Fix (2026-09-25)

- `property.TypeOptions` gained `editor: "code"` + `editorLanguage` (javaScript | go | python | json), checked by `property.ValidateEditor` at registration (a language without the editor, an unknown editor or language, or a code editor on a non-string field is refused). Packs get it too: their `typeOptions` decode into the same struct; documented in reference/node-packs.md.
- Declared on `kilasflow.code` `code` (go), `kilasflow.jsCode` `jsCode`, `kilasflow.foreignCode` `jsCode`/`pythonCode`, and the Sort `code` comparator. Sticky note `content` declares `rows: 6` (a textarea: it is markdown, not code).
- Web: CodeMirror 6 (`@codemirror/*`, MIT). `web/src/lib/workflow-editor/code-editor.ts` builds the extensions (line numbers, history, Tab/Enter indentation — a tab for Go, two spaces otherwise — bracket matching and closing, highlighting from new `--code-*` tokens in both palettes, completion of the Code node's runtime roots and upstream `$('Name')`, never a root the runtime refuses). `code-editor.svelte` keeps the view and the prop in step without echoing the sync back as an edit. `property-field.svelte` renders it for a code field, with no expression toggle and the label as the surface's aria-label.
- Found on the way: the editor's `onPaste` took any workflow JSON pasted anywhere, including into a field. A paste into a typing target now stays in that field.
- Tests: `internal/property/editor_test.go`, `nodes/code_test.go` TestSourceFieldsAskForACodeEditor, `code-editor.test.ts`, `property-field.test.ts`, e2e `code-editor.spec.ts` (paste a Go body, Enter, save, run, stored source), `js-code.spec.ts` updated for the editor.

# Related Files

SD/27-code-ndv.png, SD/28-after-run.png, SD/codepaste.js output (`INPUT | "out := []Item{} for …"`), SD/42-sticky-click.png. Code: property-field.svelte:1020-1026 picks `<textarea>` only when `needsMultiline(value, typeOptions.rows)`. nodes/code.go:41 (`code`) and nodes/annotation.go:40 (`content`) declare no `rows`/editor typeOption, and their defaults are one line.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (2):
  - `ba254e0e` — BUG-ngt25j: code parameters open in a code editor, and a sticky note's content is multi-line
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .gitignore                                         |    4 +
 .pine/memory/code-node.md                          |   30 +-
 .pine/memory/licensing.md                          |   12 +-
 .pine/memory/n8n-reference.md                      |    4 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0xv7bg.md                        |   54 +
 .pine/tickets/BUG-14gp8r.md                        |  198 +
 .pine/tickets/BUG-15st2k.md                        |  162 +
 .pine/tickets/BUG-2eryxn.md                        |   95 +
 .pine/tickets/BUG-2mes2k.md                        |   54 +
 .pine/tickets/BUG-2n4rfz.md                        |   51 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   41 +
 .pine/tickets/BUG-2z8geh.md                        |  136 +
 .pine/tickets/BUG-3k12ky.md                        |  163 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-3qxx0j.md                        |   59 +
 .pine/tickets/BUG-46g75c.md                        |  219 ++
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  179 +
 .pine/tickets/BUG-56qqgx.md                        |   52 +
 .pine/tickets/BUG-5bgx5c.md                        |   59 +
 .pine/tickets/BUG-5fhcx7.md                        |   26 +
 .pine/tickets/BUG-5xkexq.md                        |   36 +
 .pine/tickets/BUG-605n21.md                        |  131 +
 .pine/tickets/BUG-66fhea.md                        |   97 +
 .pine/tickets/BUG-6d6wbg.md                        |   98 +
 .pine/tickets/BUG-6gkd12.md                        |  107 +
 .pine/tickets/BUG-719gaz.md                        |   88 +
 .pine/tickets/BUG-9296bf.md                        |   53 +
 .pine/tickets/BUG-9dw5me.md                        |   53 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-9pmv8y.md                        |   61 +
 .pine/tickets/BUG-a9d2hb.md                        |  212 ++
 .pine/tickets/BUG-asdh5q.md                        |   52 +
 .pine/tickets/BUG-b3p8va.md                        |  120 +
 .pine/tickets/BUG-b4cb1c.md                        |  119 +
 .pine/tickets/BUG-b8bwhw.md                        |   55 +
 .pine/tickets/BUG-bcahaj.md                        |  100 +
 .pine/tickets/BUG-bw2zc1.md                        |   93 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   47 +
 .pine/tickets/BUG-c73h98.md                        |   42 +
 .pine/tickets/BUG-c7s5ss.md                        |   23 +
 .pine/tickets/BUG-cqbq25.md                        |   23 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-dstsg9.md                        |   54 +
 .pine/tickets/BUG-e7dwpk.md                        |  116 +
 .pine/tickets/BUG-e8ytyq.md                        |   36 +
 .pine/tickets/BUG-ecbq28.md                        |  111 +
 .pine/tickets/BUG-epy2se.md                        |  122 +
 .pine/tickets/BUG-fthahg.md                        |  244 ++
 .pine/tickets/BUG-g7ffj1.md                        |   92 +
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hmp85t.md                        |   99 +
 .pine/tickets/BUG-hnvn3r.md                        |   36 +
 .pine/tickets/BUG-j7qrp2.md                        |   52 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |   45 +
 .pine/tickets/BUG-k99658.md                        |  213 ++
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |   41 +
 .pine/tickets/BUG-n6p7qy.md                        |  101 +
 .pine/tickets/BUG-n9a6bz.md                        |   54 +
 .pine/tickets/BUG-namghh.md                        |   48 +
 .pine/tickets/BUG-nbymq4.md                        |   52 +
 .pine/tickets/BUG-ngt25j.md                        |   60 +
 .pine/tickets/BUG-nn74ph.md                        |   51 +
 .pine/tickets/BUG-nzy3pa.md                        |  186 +
 .pine/tickets/BUG-p334yw.md                        |   53 +
 .pine/tickets/BUG-p3j233.md                        |   53 +
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-phv0r9.md                        |   56 +
 .pine/tickets/BUG-ppvyzr.md                        |   58 +
 .pine/tickets/BUG-pzkpfr.md                        |   53 +
 .pine/tickets/BUG-q6b75c.md                        |   52 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-r1m83f.md                        |   92 +
 .pine/tickets/BUG-rbask0.md                        |  140 +
 .pine/tickets/BUG-rh7mpa.md                        |   52 +
 .pine/tickets/BUG-rs0xq1.md                        |   46 +
 .pine/tickets/BUG-rytwy7.md                        |  118 +
 .pine/tickets/BUG-sgrxhh.md                        |   53 +
 .pine/tickets/BUG-t12ffz.md                        |  196 +
 .pine/tickets/BUG-t3p92b.md                        |   94 +
 .pine/tickets/BUG-t9e3k5.md                        |   24 +
 .pine/tickets/BUG-tpyg0q.md                        |   46 +
 .pine/tickets/BUG-txafja.md                        |   55 +
 .pine/tickets/BUG-v8ksv8.md                        |   49 +
 .pine/tickets/BUG-vsmnby.md                        |  152 +
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-w9k234.md                        |   48 +
 .pine/tickets/BUG-x28fsx.md                        |  142 +
 .pine/tickets/BUG-x2sxzt.md                        |   48 +
 .pine/tickets/BUG-x6gyc1.md                        |   54 +
 .pine/tickets/BUG-xam6t8.md                        |  179 +
 .pine/tickets/BUG-y38bss.md                        |   90 +
 .pine/tickets/BUG-ywbvfa.md                        |   36 +
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-zf4pnj.md                        |  115 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   66 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 +
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 1234 +++++++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 +
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   43 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   38 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |  254 ++
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   40 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   39 +
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |   35 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   38 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   40 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 ++
 .pine/tickets/FEAT-7t0xks.md                       |   39 +
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8752vx.md                       |   54 +
 .pine/tickets/FEAT-8zgwp6.md                       |   37 +
 .pine/tickets/FEAT-9ep5pw.md                       |   38 +
 .pine/tickets/FEAT-9we7kw.md                       | 1003 +++++
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |  118 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   37 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-g6k3y9.md                       |  188 +
 .pine/tickets/FEAT-gzd32h.md                       |   36 +
 .pine/tickets/FEAT-hxztwz.md                       |   45 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   53 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |  107 +
 .pine/tickets/FEAT-mccadj.md                       |   44 +
 .pine/tickets/FEAT-mh4e8g.md                       |   38 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   39 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   40 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       |   38 +
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   44 +
 .pine/tickets/FEAT-pxcbqj.md                       |  479 +++
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qdwm1k.md                       |   50 +
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   41 +
 .pine/tickets/FEAT-r8ph93.md                       |   38 +
 .pine/tickets/FEAT-rdfjh1.md                       |   42 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   36 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   37 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-vjjs8t.md                       |  951 +++++
 .pine/tickets/FEAT-vntngh.md                       |   64 +
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |  320 ++
 .pine/tickets/FEAT-xj5tv6.md                       |   38 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   37 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |  491 +++
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   38 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |  454 +++
 .pine/tickets/FEAT-zm3wh2.md                       |   99 +
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |  200 +-
 CONTRIBUTING.md                                    |   22 +
 Makefile                                           |   35 +
 README.md                                          |  450 +--
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |  115 +-
 config.example.yaml                                |   77 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/execution-model.md  |   25 +-
 docs/src/content/docs/concepts/expressions.md      |   12 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 docs/src/content/docs/concepts/node-registry.md    |    5 +-
 .../src/content/docs/concepts/safety-boundaries.md |  164 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   22 +-
 docs/src/content/docs/concepts/webhooks.md         |   33 +
 docs/src/content/docs/guides/code-javascript.md    |  419 +++
 docs/src/content/docs/guides/community-nodes.md    |    7 +-
 docs/src/content/docs/guides/embedding.md          |   27 +-
 docs/src/content/docs/guides/n8n-migration.md      |  182 +-
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |  147 +-
 docs/src/content/docs/operate/deployment.md        |   27 +
 docs/src/content/docs/operate/tenant-deletion.md   |    3 +-
 docs/src/content/docs/reference/api-contract.md    |   16 +-
 docs/src/content/docs/reference/api.md             |    2 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/reference/api/executions.md  |    2 +-
 docs/src/content/docs/reference/cli.md             |   62 +-
 .../content/docs/reference/expression-grammar.md   |    8 +-
 docs/src/content/docs/reference/node-packs.md      |    8 +
 docs/src/content/docs/start/what-kilasflow-is.md   |   17 +-
 e2e/fixtures/epic-code.ts                          |  271 ++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |    4 +
 e2e/helpers/stub.ts                                |    7 +-
 e2e/tests/code-editor.spec.ts                      |  108 +
 e2e/tests/datastore.spec.ts                        |  103 +
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/js-code.spec.ts                          |  702 ++++
 e2e/tests/n8n-compare.spec.ts                      |   22 +-
 e2e/tests/node-coverage.spec.ts                    |   25 +-
 e2e/tests/smoke.spec.ts                            |   86 +
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   12 +-
 go.sum                                             |   22 +-
 internal/ai/fromai.go                              |   30 +
 internal/ai/fromai_test.go                         |   37 +
 internal/ai/openai.go                              |   77 +-
 internal/ai/openai_test.go                         |   66 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   30 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    4 +-
 internal/api/handlers/credentials.go               |    5 +-
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/execution_retry.go           |    9 +-
 internal/api/handlers/executions.go                |   54 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/oauth.go                     |   17 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/workflows.go                 |   29 +-
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    4 +-
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  132 +-
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |   38 +-
 internal/cli/openapi_contract_test.go              |    8 +-
 internal/cli/verbs_api.go                          |   59 +-
 internal/cli/verbs_api_test.go                     |  256 ++
 internal/cli/verbs_credential_test.go              |   71 +
 internal/config/config.go                          |  172 +-
 internal/config/config_test.go                     |  142 +
 internal/credentials/credentials_test.go           |   26 +-
 .../datastore_unique_names_migration_test.go       |  212 ++
 internal/database/migrate_test.go                  |    1 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |    7 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 ++
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   29 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/always_output_test.go              |  379 ++
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |    8 +-
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/checkpoint.go                      |   27 +-
 internal/engine/console_test.go                    |  397 ++
 internal/engine/eval.go                            |   23 +-
 internal/engine/eval_test.go                       |   40 +
 internal/engine/export_test.go                     |   13 +-
 internal/engine/fanout_lineage_test.go             |  241 ++
 internal/engine/item_outcomes.go                   |  131 +
 internal/engine/item_outcomes_test.go              |  201 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/node_branch_test.go                |   92 +
 internal/engine/node_branches.go                   |   78 +
 internal/engine/runindex_skip_test.go              |  299 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          |  809 +++-
 internal/engine/runner_test.go                     |  968 +++++
 internal/engine/service.go                         |   75 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 ++
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  168 +-
 internal/engine/wait_service.go                    |    7 +-
 internal/engine/wait_service_test.go               |  492 ++-
 internal/execution/records.go                      |    7 +
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |   17 +
 internal/expression/expression_test.go             |   63 +
 internal/expression/globals.go                     |    9 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  178 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/loose.go                      |  252 ++
 internal/interop/n8n/loose_json_test.go            |  239 ++
 internal/interop/n8n/n8n.go                        |  157 +-
 internal/interop/n8n/n8n_test.go                   |  537 ++-
 internal/interop/n8n/parameters.go                 |  246 +-
 internal/interop/n8n/rag_import_test.go            |   40 +-
 internal/jsrun/analyze.go                          |  666 ++++
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |  184 +
 internal/jsrun/bounds_test.go                      |  364 ++
 internal/jsrun/buffer_test.go                      |   78 +
 internal/jsrun/clock.go                            |   68 +
 internal/jsrun/codec.go                            |  330 ++
 internal/jsrun/codec_internal_test.go              |  103 +
 internal/jsrun/comparator_test.go                  |  221 ++
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  109 +
 internal/jsrun/corpus/BASELINE.md                  |  412 +++
 internal/jsrun/corpus/MANIFEST.json                |  769 ++++
 internal/jsrun/corpus/baseline.json                | 3464 ++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 ++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/crypto.go                           |  859 +++++
 internal/jsrun/crypto_test.go                      |  792 ++++
 internal/jsrun/doc.go                              |   99 +
 internal/jsrun/engine.go                           |  739 ++++
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  230 ++
 internal/jsrun/export_test.go                      |   80 +
 internal/jsrun/guards_test.go                      |  172 +
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             | 3853 ++++++++++++++++++++
 internal/jsrun/intl_internal_test.go               |  204 ++
 internal/jsrun/intl_test.go                        |  780 ++++
 internal/jsrun/items.go                            |  248 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  808 ++++
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |  538 +++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  247 ++
 internal/jsrun/js/runtime.js                       | 1726 +++++++++
 internal/jsrun/jsrun.go                            |  247 ++
 internal/jsrun/jsrun_test.go                       |  487 +++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  145 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |  152 +
 internal/jsrun/roots_test.go                       |  584 +++
 internal/jsrun/run.go                              |  484 +++
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 3598 ++++++++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   33 +
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |   68 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/utf8.json           | 2598 +++++++++++++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  832 +++++
 internal/jsrun/testdata/surface.txt                |  512 +++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  252 ++
 internal/jsrun/wire.go                             |  213 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |  111 +
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 ++
 internal/jsworker/confine_linux_test.go            |  501 +++
 internal/jsworker/confine_test.go                  |  250 ++
 internal/jsworker/doc.go                           |   78 +
 internal/jsworker/helpers_test.go                  |  275 ++
 internal/jsworker/jsworker_test.go                 |  595 +++
 internal/jsworker/limits_linux.go                  |   93 +
 internal/jsworker/limits_other.go                  |   45 +
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  952 +++++
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |  158 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  326 ++
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |   39 +-
 internal/nodepack/trigger.go                       |   75 +-
 internal/nodepack/trigger_capture_test.go          |  221 ++
 internal/property/editor_test.go                   |   51 +
 internal/property/property.go                      |   52 +
 internal/repository/executions.go                  |   43 +-
 internal/repository/models.go                      |   11 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   11 +
 internal/repository/tenant_rows.go                 |    5 +-
 internal/repository/webhook_state.go               |  108 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflows.go                   |   12 +-
 internal/routing/executor.go                       |    2 +-
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/tenantpurge/harness_test.go               |    5 +
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  563 ++-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 ++
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/webhook.go                        |   29 +-
 internal/workflow/compiler.go                      |  164 +-
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |    9 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 nodes/ai.go                                        |   93 +-
 nodes/ai_test.go                                   |   59 +
 nodes/annotation.go                                |    1 +
 nodes/code.go                                      |    3 +-
 nodes/code_test.go                                 |   48 +
 nodes/core.go                                      |    1 +
 nodes/datastore.go                                 |  718 +++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_tool_test.go                       | 1299 ++++++-
 nodes/embedscope.go                                |   36 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/executors.go                                 |   36 +-
 nodes/jscode.go                                    |  317 +-
 nodes/jscode_helpers.go                            |  272 ++
 nodes/jscode_helpers_test.go                       |  270 ++
 nodes/jscode_lineage_test.go                       |  207 ++
 nodes/jscode_roots.go                              |   49 +
 nodes/jscode_roots_test.go                         |  177 +
 nodes/jscode_run_test.go                           |  361 ++
 nodes/jscode_test.go                               |   41 +-
 nodes/sort_code_test.go                            |  206 ++
 nodes/transform.go                                 |  110 +-
 packs/waha/waha_test.go                            |   55 +-
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  756 ++++
 sdk/src/generated/models.ts                        |  298 +-
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |    2 +
 skills/kilasflow-datastore/SKILL.md                |    3 +-
 skills/kilasflow-datastore/references/FILTERS.md   |    2 +-
 skills/kilasflow-expressions/SKILL.md              |    5 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 skills/kilasflow-import-export/SKILL.md            |   15 +-
 .../references/MAPPING_LIMITS.md                   |   57 +-
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/editor.json                        |   20 +-
 web/messages/en/executions.json                    |    3 +
 web/messages/id/editor.json                        |   20 +-
 web/messages/id/executions.json                    |    3 +
 web/package.json                                   |   10 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |   16 +
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   13 +
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 web/src/lib/api/http.ts                            |   32 +-
 .../workflow-editor/canvas-chat-panel.svelte       |  381 +-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../components/workflow-editor/code-editor.svelte  |   73 +
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../workflow-editor/property-field.svelte          |   13 +-
 .../workflow-editor/property-field.test.ts         |   50 +
 .../workflow-editor/workflow-editor.svelte         |   59 +-
 web/src/lib/embed/session.svelte.ts                |    6 +-
 web/src/lib/embed/session.test.ts                  |   47 +-
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   54 +-
 web/src/lib/workflow-editor/chat.ts                |   78 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  262 ++
 web/src/lib/workflow-editor/event-stream.svelte.ts |   71 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   27 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/execution.test.ts      |   70 +
 web/src/lib/workflow-editor/execution.ts           |   62 +-
 web/src/lib/workflow-editor/validation.ts          |    5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   36 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   61 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  181 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 658 files changed, 90756 insertions(+), 1564 deletions(-)
```
