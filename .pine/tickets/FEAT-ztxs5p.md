---
id: FEAT-ztxs5p
title: Add the Telegram trigger with self-registering webhooks
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-91as16
    - FEAT-0f87fn
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:03:01Z"
updated: "2026-09-05T11:26:15Z"
---

## Scope

Telegram is the fast proof of the whole phase. A bot token from BotFather costs nothing, needs no WhatsApp infrastructure and no customer instance, and a Telegram Trigger into an AI Agent into a Send Message reply exercises exactly the same path a WAHA template will: a registry-driven webhook binding, a trigger-shaped item, credential-scoped outbound calls, and a running graph. It is the epic's first acceptance scenario for that reason.

Match `n8n-nodes-base.telegramTrigger` parameter for parameter, so an imported workflow lands on an identical form. The **Trigger On** multi-select carries n8n's exact list: `*` (all updates except Chat Member, Message Reaction and Message Reaction Count), Message, Edited Message, Channel Post, Edited Channel Post, Callback Query, Inline Query, Chosen Inline Result, My Chat Member, Chat Member, Chat Join Request, Poll, Poll Answer, Pre-Checkout Query, Shipping Query, Message Reaction, Message Reaction Count, Chat Boost, Removed Chat Boost, Business Connection, Business Message, Edited Business Message, Deleted Business Messages, Purchased Paid Media. The additional fields are Download Images/Files with its Image Size sub-option, Restrict to Chat IDs, and Restrict to User IDs. The `*` exclusion list is not n8n's invention — the Bot API itself documents `chat_member`, `message_reaction` and `message_reaction_count` as the three update types not delivered by default.

Unlike the WAHA trigger, this one registers itself. Activation calls `setWebhook` with the workflow's own URL, the selected `allowed_updates`, and a `secret_token`; deactivation calls `deleteWebhook`. Telegram returns the secret in the `X-Telegram-Bot-Api-Secret-Token` header on every delivery, so verification is a constant-time comparison and there is no excuse for skipping it. This is the first node that needs the activate and deactivate lifecycle hooks, and the first that needs webhook binding extraction to be registry-driven — `internal/webhook/webhook.go`'s `Extract` filters on one node type supplied at `cmd/kilasflow/main.go:138`, so no trigger but `kilasflow.webhook` can bind a path today.

The credential is a bot token. `internal/credentials` has no type that fits and no way to add one from outside its package-level `definitions` map, and `web/src/lib/workflow-editor/credentials.ts`'s `BY_NODE_TYPE` decides whether the editor shows a credential picker at all.

## Acceptance criteria

- [x] The trigger registers with n8n's exact Trigger On list and the three additional fields, and selecting `*` sends no `allowed_updates` narrower than the Bot API's own default set.
- [x] Activation calls `setWebhook` with the workflow's URL, the selected updates and a generated `secret_token`; deactivation calls `deleteWebhook`; both go through `safehttp` and the credential store.
- [x] Every delivery is rejected unless `X-Telegram-Bot-Api-Secret-Token` matches, compared in constant time, before an execution is created.
- [x] A `telegramApi` credential type holds the bot token as a secret field, is never returned after storage, and scopes outbound calls to the Telegram API host.
- [x] Restrict to Chat IDs and Restrict to User IDs drop a non-matching update without starting an execution, and the drop is visible in logs rather than silent.
- [x] Download Images/Files fetches the update's file through `getFile` into the binary store and attaches it to the item as a binary reference, honouring the Image Size sub-option; with the option off, no file is fetched.
- [x] An update type the node does not know is passed through to the item rather than rejected, so a new Bot API update type does not break an active workflow.
- [x] A local development mode exists that receives updates without a public URL, and the README documents both it and the tunnel route.

## Outcome

The trigger is a built-in node rather than a pack, because three of its behaviours are not data: the secret it registers is an HMAC over the bot token and the route, `getFile` is a two-step resolve-then-download, and polling is a goroutine. The pack machinery covers a trigger whose registration is one templated call; this is not one of them, and the ticket's plan said so.

**The secret is derived, not generated.** There is nowhere to store a generated one — a binding's parameters are the workflow document's, and activation cannot write to them — so it is `HMAC-SHA256(botToken, "kilasflow-telegram-webhook:" + route)`, which gives registration and verification the same value with no new storage and rotates it whenever the token or the route changes. It is rendered in the Bot API's own alphabet (`A-Z a-z 0-9 _ -`, 1–256 characters), because a raw base64 secret is refused at registration and the failure looks like a bad token.

**Filtering is not verification, and the answers differ.** A failed signature is 401 and means "you should not be sending this". A restricted chat is 200 and means "I received it and chose not to act", which is what stops Telegram retrying — and what avoids telling an attacker which chats a workflow watches. That distinction needed a new seam: `webhook.TriggerKind.Accept`, separate from `Verify`, and the drop is logged with the route, the workflow, the node and the reason.

**The filter fails closed.** An update type this build has never seen carries its chat id somewhere unknown, so a restriction that cannot find one drops the update — a filter that fails open is not a filter. With no restriction configured the same unknown update passes straight through, which is what keeps a new Bot API update type from breaking an active workflow. Both halves are tested.

**Large ids are compared as integers.** A Telegram chat id rendered through the default float formatting becomes `1.234567891e+09` and matches nothing, so a user would have configured a restriction that silently dropped everything.

**Two delivery modes, one downstream path.** Polling calls `getUpdates` in a long poll owned by the activation lifecycle, one goroutine per active trigger, cancelled on deactivation, with a backoff that stops a revoked token from hammering the API. It applies the same restriction filters the HTTP boundary applies, so the two modes cannot diverge. Telegram refuses `getUpdates` while a webhook is set and its error names neither cause nor cure, so polling deletes the webhook first — every time, not only when we think one exists. The supervisor's context is the server's, not the activation request's: a poller cancelled when its HTTP request finished would stop the moment it started.

**`setWebhook` requires HTTPS**, and its own error for a non-HTTPS URL is opaque, so activation refuses with a message naming the requirement and pointing at polling.

Two smaller decisions. The credential carries an optional Base URL, because Telegram publishes a local Bot API server and a deployment running one has no route to the public host; it is a credential field rather than server configuration because two bots on one server can legitimately live behind different addresses. And `nodes.RegisterLifecycles` and `nodes.RegisterTriggerKinds` exist so composition and the binding test call the same function — a test that built its own registry would assert that composition agrees with the test.

The Trigger On list is pinned to n8n's, in n8n's order, so an imported node finds the strings it carries. Selecting `*` sends **no** `allowed_updates` at all, which is what asks for the Bot API's default set — building an explicit list from every option would have subscribed the workflow to the three types the API deliberately withholds, which a user who picked `*` did not ask for.

## Implementation Plan

Order the work so each step is testable: credential type, then the node definition and its webhook binding, then the lifecycle hooks and `setWebhook`, then secret verification, then the restrict filters, then downloads. The lifecycle hooks are the piece with the most reach — they are the same hooks WAHA's optional auto-registration uses — so implement them as the trigger-lifecycle work defines them and resist adding a Telegram-shaped variant.

Take the local development mode, and implement it as an explicit Delivery parameter with `webhook` as the default and `polling` as the alternative. It is the difference between the owner being able to test on a laptop and not, `getUpdates` is a documented Bot API method with the same `allowed_updates` argument, and the two modes share everything downstream of the update. Implement polling as a goroutine owned by the activation lifecycle, one per active trigger, cancelled on deactivation. Be honest in the parameter description that it is single-process: it is correct for the single binary shipped today and will need revisiting when work moves across processes. Telegram refuses `getUpdates` while a webhook is set, so switching modes must delete the webhook first — that failure is confusing enough to deserve its own error message.

The traps are all in the details. `secret_token` accepts only `A-Z`, `a-z`, `0-9`, `_` and `-`, 1 to 256 characters, so generate it from an alphabet that respects that rather than from arbitrary base64. `setWebhook` requires a public HTTPS URL, so activation must fail with a message naming that requirement rather than a bare API error when the configured public URL is not one. And the Bot API has grown update types beyond n8n's list — the current documentation includes several that n8n's selector does not offer — which is exactly why the list is pinned to n8n's for import parity while unknown incoming updates still pass through.

One thing cannot be confirmed from the reference checkout as it currently stands: the sparse checkout contains only `HttpRequest`, `If`, `Schedule` and `Set` under `packages/nodes-base/nodes`, so the exact credential `name` and the exact option values must be read off the Telegram node once the checkout is widened. Treat `telegramApi` as the expected name, not a verified one, until then.

## References

- Roadmap plan, p3 section, entry V2-p3-6: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- Telegram Bot API, https://core.telegram.org/bots/api — `setWebhook` (`url`, `allowed_updates`, `drop_pending_updates`, `secret_token` of 1–256 characters from `A-Z a-z 0-9 _ -`), the `X-Telegram-Bot-Api-Secret-Token` header, `deleteWebhook`, `getUpdates`, and the note that `chat_member`, `message_reaction` and `message_reaction_count` are not delivered by default.
- `internal/webhook/webhook.go` — `Extract` and its single hardcoded node type, wired at `cmd/kilasflow/main.go:138`; `requestPayload`'s fixed item envelope.
- `nodes/webhook.go` — `webhookTrigger`, the authentication modes, and `WebhookPath`.
- `internal/credentials/credentials.go` — the closed `definitions` map and `Apply`'s switch.
- `web/src/lib/workflow-editor/credentials.ts` — `BY_NODE_TYPE`, which gates the credential picker.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 11 — the Telegram Trigger NDV to match: Webhook URLs, credential, two notice blocks, Trigger On as multi-select chips, Additional Fields, Test this trigger. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .gitignore                                         |    10 +
 .pine/MEMORY.md                                    |     2 +
 .pine/memory/licensing.md                          |    12 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/roadmap.md                                   |   909 ++
 .pine/tickets/EPIC-m42s3g.md                       |    73 +
 .pine/tickets/FEAT-096vs9.md                       |    53 +
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |    65 +
 .pine/tickets/FEAT-1500sp.md                       |    58 +
 .pine/tickets/FEAT-1axhdn.md                       |    65 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |    68 +
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |    68 +
 .pine/tickets/FEAT-347egc.md                       |    54 +
 .pine/tickets/FEAT-3xqky1.md                       |    70 +
 .pine/tickets/FEAT-45tfmh.md                       |    68 +
 .pine/tickets/FEAT-48hreg.md                       |    61 +
 .pine/tickets/FEAT-4d0bje.md                       |    62 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fv8gf.md                       |    59 +
 .pine/tickets/FEAT-5kfctc.md                       |    66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   118 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-68zzqs.md                       |    65 +
 .pine/tickets/FEAT-6vfn3s.md                       |    61 +
 .pine/tickets/FEAT-7cg0cd.md                       |    60 +
 .pine/tickets/FEAT-8qyfh1.md                       |    53 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   141 +
 .pine/tickets/FEAT-9555xz.md                       |    58 +
 .pine/tickets/FEAT-96p7m3.md                       |    52 +
 .pine/tickets/FEAT-9dqn7d.md                       |    66 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a94c8y.md                       |    60 +
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   113 +
 .pine/tickets/FEAT-agj52c.md                       |    64 +
 .pine/tickets/FEAT-ajw7wt.md                       |    61 +
 .pine/tickets/FEAT-az620p.md                       |    54 +
 .pine/tickets/FEAT-bp0ytb.md                       |   376 +
 .pine/tickets/FEAT-c2a081.md                       |    55 +
 .pine/tickets/FEAT-cgm1y3.md                       |    50 +
 .pine/tickets/FEAT-cjpbe6.md                       |    70 +
 .pine/tickets/FEAT-csqgg5.md                       |   145 +
 .pine/tickets/FEAT-ddzk2k.md                       |    59 +
 .pine/tickets/FEAT-ej0468.md                       |    54 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |    64 +
 .pine/tickets/FEAT-gjzgkd.md                       |    59 +
 .pine/tickets/FEAT-gvn62x.md                       |    57 +
 .pine/tickets/FEAT-gxppx1.md                       |    71 +
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |    56 +
 .pine/tickets/FEAT-jwhdsy.md                       |    51 +
 .pine/tickets/FEAT-k3fmj1.md                       |   141 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |    60 +
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    56 +
 .pine/tickets/FEAT-mvegj5.md                       |    56 +
 .pine/tickets/FEAT-n19dch.md                       |    66 +
 .pine/tickets/FEAT-n5fdz3.md                       |    69 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nch9dg.md                       |    67 +
 .pine/tickets/FEAT-nrfg6e.md                       |    69 +
 .pine/tickets/FEAT-nrfz6m.md                       |    64 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-ptyh9w.md                       |    65 +
 .pine/tickets/FEAT-q81bq4.md                       |    56 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |    39 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-sdjdh2.md                       |    33 +
 .pine/tickets/FEAT-snxxny.md                       |    68 +
 .pine/tickets/FEAT-sp8cfm.md                       |   396 +
 .pine/tickets/FEAT-ss44d9.md                       |    67 +
 .pine/tickets/FEAT-t26rt7.md                       |    65 +
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |    57 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |    67 +
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |    66 +
 .pine/tickets/FEAT-xx6p22.md                       |    62 +
 .pine/tickets/FEAT-ybm2pd.md                       |    55 +
 .pine/tickets/FEAT-yyjfjq.md                       |   123 +
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |    79 +
 Makefile                                           |    19 +
 README.md                                          |    33 +
 cmd/kilasflow/main.go                              |   113 +-
 cmd/nodepackgen/generate.go                        |   576 +
 cmd/nodepackgen/generate_test.go                   |   374 +
 cmd/nodepackgen/main.go                            |   165 +
 cmd/nodepackgen/openapi.go                         |   168 +
 cmd/nodepackgen/testdata/manifest.json             |    12 +
 cmd/nodepackgen/testdata/pack.golden.json          |   238 +
 cmd/nodepackgen/testdata/report.golden.md          |    20 +
 cmd/nodepackgen/testdata/spec.json                 |    98 +
 config.example.yaml                                |    17 +
 internal/ai/ai_test.go                             |    47 +
 internal/api/handlers/credentials.go               |    73 +
 internal/api/handlers/executions.go                |     1 +
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |   119 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/routes.go                             |    12 +-
 internal/api/server.go                             |    15 +-
 internal/api/workflows_test.go                     |    73 +-
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/config/config.go                          |    33 +
 internal/credentials/builtin.go                    |   150 +
 internal/credentials/credentials.go                |    91 +-
 internal/credentials/credentials_test.go           |   242 +
 internal/credentials/registry.go                   |   312 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   830 +-
 internal/engine/runner_test.go                     |  1142 +-
 internal/engine/service.go                         |    85 +-
 internal/engine/service_test.go                    |    47 +-
 internal/engine/worker_test.go                     |     4 +-
 internal/execution/records.go                      |    55 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    82 +-
 internal/expression/expression.go                  |   328 +-
 internal/expression/expression_test.go             |   309 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/interop/n8n/corpus/BASELINE.md            |   104 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   436 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   570 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |   907 +-
 internal/interop/n8n/n8n_test.go                   |  1263 ++-
 internal/interop/n8n/parameters.go                 |   101 +-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   634 +-
 internal/node/registry_test.go                     |   610 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   180 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |    48 +-
 internal/repository/models.go                      |    97 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   371 +
 internal/routing/request.go                        |   388 +
 internal/routing/response.go                       |   172 +
 internal/routing/routing.go                        |   354 +
 internal/routing/routing_test.go                   |   764 ++
 internal/scheduler/scheduler.go                    |     8 +-
 internal/scheduler/scheduler_test.go               |    41 +-
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   322 +-
 internal/webhook/webhook_test.go                   |   473 +-
 internal/workflow/compiler.go                      |   347 +-
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 nodes/ai.go                                        |    64 +-
 nodes/ai_test.go                                   |    28 +-
 nodes/annotation.go                                |    62 +
 nodes/bindings_test.go                             |   129 +
 nodes/code.go                                      |    28 +-
 nodes/code_test.go                                 |    68 +-
 nodes/core.go                                      |    50 +-
 nodes/database.go                                  |    14 +-
 nodes/database_test.go                             |     8 +-
 nodes/executors.go                                 |    64 +-
 nodes/executors_test.go                            |   113 +
 nodes/http.go                                      |   193 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/unsupported.go                               |   116 +-
 nodes/webhook.go                                   |    47 +-
 packs/waha/README.md                               |    32 +
 packs/waha/REPORT-202409.md                        |   100 +
 packs/waha/REPORT-202502.md                        |   128 +
 packs/waha/manifest-202409.json                    |   124 +
 packs/waha/manifest-202502.json                    |   124 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/pack-trigger-202409.json                |   124 +
 packs/waha/pack-trigger-202502.json                |   130 +
 packs/waha/waha.go                                 |   107 +
 packs/waha/waha_test.go                            |  1193 ++
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/corpus-sync.sh                             |   225 +
 sdk/src/generated/models.ts                        |   457 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |    96 +-
 .../models/{unsupported.ts => activationNotice.ts} |    10 +-
 .../lib/api/generated/models/activationResource.ts |    23 +
 web/src/lib/api/generated/models/condition.ts      |    14 +
 .../api/generated/models/credentialRequirement.ts  |    15 +
 web/src/lib/api/generated/models/definition.ts     |    17 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     2 +
 .../lib/api/generated/models/executionSummary.ts   |     2 +
 web/src/lib/api/generated/models/exportIssue.ts    |    16 +
 .../api/generated/models/exportIssueSeverity.ts    |    19 +
 .../generated/models/exportedWorkflowResource.ts   |     4 +-
 .../lib/api/generated/models/expressionGrammar.ts  |    16 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    27 +-
 .../api/generated/models/loadOptionsInputBody.ts   |    20 +
 .../models/loadOptionsInputBodyParameters.ts       |     9 +
 .../api/generated/models/loadOptionsResource.ts    |    17 +
 web/src/lib/api/generated/models/node.ts           |     1 +
 web/src/lib/api/generated/models/nodeCodex.ts      |    16 +
 .../api/generated/models/nodeCodexSubcategories.ts |     9 +
 .../api/generated/models/{lossy.ts => nodeIcon.ts} |     7 +-
 web/src/lib/api/generated/models/option.ts         |    12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |    21 +
 web/src/lib/api/generated/models/port.ts           |     9 +-
 .../lib/api/generated/models/propertyDefinition.ts |    11 +
 web/src/lib/api/generated/models/propertyGroup.ts  |    15 +
 .../api/generated/models/testCredentialResource.ts |    14 +
 web/src/lib/api/generated/models/typeOptions.ts    |    17 +
 web/src/lib/api/generated/models/visibility.ts     |    15 +
 .../lib/api/generated/models/webhookDeclaration.ts |    15 +
 .../api/generated/models/webhookRouteResource.ts   |    14 +
 web/src/lib/api/generated/nodes/nodes.ts           |   342 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |     3 +-
 .../components/workflow-editor/canvas-node.svelte  |    33 +-
 .../workflow-editor/execution-canvas-node.svelte   |    20 +-
 .../components/workflow-editor/node-icon.svelte    |    10 +-
 .../components/workflow-editor/node-picker.svelte  |     8 +-
 .../workflow-editor/properties-panel.svelte        |    46 +-
 .../workflow-editor/property-field.svelte          |   138 +-
 .../workflow-editor/workflow-editor.svelte         |     4 +-
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |     4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    86 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   208 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 292 files changed, 63123 insertions(+), 1173 deletions(-)
```
