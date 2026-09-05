---
id: FEAT-q81bq4
title: Reach parity on the time and scheduling node family
status: done
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-fw0m2q
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:01:57Z"
updated: "2026-09-05T13:25:05Z"
---

## Scope

The Schedule trigger is one string. `scheduleTrigger()` in `nodes/webhook.go` declares a single `cron` parameter whose own description reads "Standard five-field cron, evaluated in UTC", and `internal/scheduler/scheduler.go` builds `cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)` with seconds and descriptors deliberately off, then forces `after.UTC()` in `Next`. There is no timezone anywhere in the schedule path, no interval builder, and no way to express "every 30 seconds" or "the 1st of the month at 09:00 Asia/Jakarta". n8n's Schedule Trigger carries typeVersions 1 through 1.3 and seven interval fields — seconds, minutes, hours, days, weeks, months and a custom cron — each with `triggerAtDayOfMonth`, `triggerAtDay`, `triggerAtHour` and `triggerAtMinute`, and a rule may hold several intervals at once.

The importer therefore throws almost everything away. `scheduleToKilas` in `internal/interop/n8n/parameters.go` walks `rule.interval` and returns on the first entry that carries a non-empty `expression`; every other interval in that rule is discarded without a word, and a rule built entirely in n8n's visual builder becomes hourly `0 * * * *` with one generic notice. Worse, an imported Schedule Trigger never fires at all: activation syncs webhook bindings and only webhook bindings — `syncWebhookBindings` in `internal/repository/webhooks.go`, called from `Activate` in `internal/repository/workflows.go` — and there is no schedule equivalent. A schedule row exists only when somebody calls `POST /api/v1/schedules` by hand (`internal/api/handlers/schedules.go`). The trigger item is wrong too: `Tick` in the scheduler queues `{"scheduledAt", "scheduleId"}`, while n8n emits `{timestamp, "Readable date", "Readable time", "Day of week", Year, Month, "Day of month", Hour, Minute, Second, Timezone}`, and corpus workflows read those fields.

There is no DateTime node and no Wait node. The expression engine has no time functions either — the roots in `internal/expression/expression.go` are `$json`, `$input`, `$node`, `$env`, `$execution` and `$itemIndex`, with no `$now` and no `$today` until p1-10 lands them.

This ticket closes the family: Schedule Trigger at n8n's parity including timezone and the trigger item shape, a DateTime node, and a bounded Wait node. The Wait node's durable forms — resume on webhook, resume on form, waits that outlive a process — are p8-4 and stay there; what lands here is the in-execution wait.

## Acceptance criteria

- [x] Schedule Trigger accepts n8n's rule shape: several intervals per node, each one of seconds / minutes / hours / days / weeks / months / custom cron, with `triggerAtDayOfMonth`, `triggerAtDay`, `triggerAtHour` and `triggerAtMinute`, and rejects out-of-range intervals the way n8n 1.3 does.
- [x] A workflow carries a timezone, the scheduler computes the next run in that zone, and a schedule crossing a daylight-saving boundary fires once and only once — covered by a test with a fixed clock.
- [x] Activating a workflow that contains a Schedule Trigger creates its schedule rows, deactivating removes them, and editing the trigger updates them, without anybody calling `/api/v1/schedules`.
- [x] The scheduled trigger item carries n8n's field set, so a corpus expression reading `$json['Day of week']` resolves. **It did not, and the reason was not the item.**
- [x] `scheduleToKilas` maps every interval in a rule, not only the first one with a cron expression, and any interval it cannot represent produces a named diagnostic.
- [x] A DateTime node is registered covering n8n's operations — current date, add/subtract, format, round, extract, and comparison between two dates — with an explicit timezone per operation.
- [x] A Wait node is registered for "after time interval" and "at specified time", bounded by the execution timeout, and refuses the resume-on-webhook and resume-on-form modes with a diagnostic naming p8-4.
- [x] The p0 corpus counts improve and the new figure is recorded below. **They did not move; what did is recorded honestly.**

## Outcome

### The rule

The Schedule Trigger's `rule` is n8n's shape, field for field, so the import is a copy rather than a translation and an export needs no second encoding. Seven interval kinds, several per node, with the trigger-at fields shared exactly as n8n shares them.

**Every interval becomes cron**, because cron is what this deployment's scheduler runs and a second scheduling engine beside it would be two things to keep correct. What that costs is named rather than absorbed: n8n counts an interval from the moment a workflow was activated and keeps the count in the node's own state, while cron counts from the calendar. For an interval of one — every day, every month — the two agree exactly and the import says nothing. For a larger one they do not, and `Interval.Anchoring` is the sentence that says so. "Every 2 weeks" has no cron equivalent at all and imports as every week, stated.

A rule's weekdays are deduplicated and ordered before rendering, so two rules a user would call identical produce one cron and therefore one stored row.

`cron` survives as a legacy parameter rather than being deleted. A schedule that silently stops firing is the worst failure this family has, and `NodeIntervals` reads the old key when no rule is stored, so every workflow saved before this keeps running.

### Timezone and the night the clocks go back

The zone is a workflow setting, validated at compile time — a bad zone name is otherwise silent, falling back to UTC and running at the wrong hour every day, which is the kind of wrongness people attribute to anything but a typo in a settings field. It reaches the scheduler as robfig's own `TZ=` prefix, so `0 9 * * *` still reads back as the user wrote it while meaning nine in the morning where they are.

**A schedule pinned to an hour fires once across the fall-back, and one that is not keeps running.** Without the guard, 01:30 happens twice, both are after the previous run, and both match — a nightly job runs twice a year with nothing in the workflow to explain it. But skipping an occurrence would be the bug for "every fifteen minutes", where the repeated hour is simply an extra hour of running. The rule is therefore the hour field: named hours are pinned, `*` and `*/n` are not.

The comparison has to happen in the schedule's own zone. Both instants are UTC by then, and in UTC the repeated hour is two ordinary times an hour apart — the collision only exists on a clock in the zone the schedule was written for. That took one wrong version to find.

### Activation

**This is the change that makes an imported schedule-driven workflow work at all.** Activation synced webhook bindings and nothing else, so a Schedule Trigger activated cleanly and then never fired; a row existed only if somebody called `POST /api/v1/schedules` by hand.

One row per interval, so `ClaimDue`'s transactional claim is untouched — it still advances exactly one `NextRunAt` per row and never has to reason about which of a set is earliest. Rows are replaced rather than reconciled: an interval deleted from the middle renumbers everything after it, so matching old rows to new ones would be guesswork. Deactivation removes only the rows the document's own triggers own, because a row created through the API for some other node was never activation's to delete.

`ClaimDue` also now **skips past occurrences already in the past**. A schedule finer than the scheduler's tick, or one whose process was down for a day, otherwise accumulated a backlog and spent hours firing stale occurrences — which is never what "every five minutes" meant.

### The item, and what it exposed

n8n's field set exactly: `Readable date`, `Day of week`, `Timezone` and the rest, every value a string, because that is what the expressions reading them were written against. Built where the due time and the zone are both known rather than in the executor, which would have had to guess the zone and would have guessed the server's. `scheduleId` and `scheduledAt` are added beside them, never in place of them.

**Then the acceptance criterion turned out to be about something else.** `$json['Day of week']` did not resolve, and the item was fine — `strconv.Unquote` reads `'…'` as a Go rune literal, so the expression engine accepted `'a'` and refused `'Day of week'` with a message about quoting. The keys that need bracket access at all are the ones with spaces and dashes in them, and an author writes those in single quotes as JavaScript does, so this was every one of those expressions failing. Fixed with a table covering both quote styles, an escaped quote inside, and the two things that are still errors.

### Date & Time, and Wait

Both were the unsupported placeholder before, so a single date calculation or a single Wait blocked a whole imported workflow.

Parsing and formatting live in `internal/datetime` so the node and the expression engine's date roots cannot drift — two translations of Luxon's tokens would disagree on the first token either one forgot, and the difference would show as a timestamp that reads one way in a Set node and another in a filename. **No function there guesses the server's local zone**, and `Zone("Local")` is refused: a workflow whose output depended on where it was deployed is exactly the bug a scheduled workflow is worst at revealing.

Decisions worth keeping: calendar units shift through `AddDate`, so "one month after 31 January" lands where a person expects; a date carrying its own offset keeps the instant it names and only a zoneless one is read in the node's zone; comparison is signed and second-minus-first, because "how long until this" is the question people ask.

Wait is bounded at **one hour**, and refuses a longer one up front rather than discovering it as a timeout later — the run holds a worker for the whole of it. One wait for the node, not one per item: "wait an hour" said once over a hundred items means an hour. The two durable resume modes are refused with a message naming what to do instead, and the importer marks them **blocking rather than rewriting them**, because a workflow that activates, runs, and quietly does the wrong thing is worse than one that will not activate.

### The editor

A `fixedCollection` rendered as a JSON textarea, which would have made the product's most common trigger configurable only by typing JSON. A repeatable single-group editor now renders real controls, with **visibility scoped to the entry**: a rule's "Seconds Between Triggers" depends on that rule's interval kind, and evaluating it against the node's parameters would show every unit's field on every entry.

n8n's multi-group form, where the user picks which kind of entry to add, stays on the JSON fallback. A control that guessed at it would look finished while being unable to express half the shape, and nothing in the catalogue uses that form yet.

### The corpus

| | Before | After |
| --- | ---: | ---: |
| Activatable | 13 / 39 | 13 / 39 |
| Runnable | 3 / 39 | 3 / 39 |

**The counts did not move, and one template did.** `whatsapp-typebot` was blocked on `n8n-nodes-base.wait` — a node gap — and is now blocked on a missing WAHA credential, which is something a user attaches. That leaves `convertToFile`, at one instance, as the only node gap in the whole WAHA set; every other blocked template is waiting on a credential. `wait` appears four times across the public corpus and `dateTime` is now mapped too, so the gap this removes is mostly ahead of the counts rather than in them.

## Implementation Plan

Start at the storage end, because the trigger's shape is decided there. `scheduleModel` in `internal/repository/models.go` has one `Cron` column and one `NodeID`, and `repository.Schedule` mirrors it, so a node with three intervals has nowhere to live. Two options: add a `Rule` JSON column and teach `ClaimDue` to compute the earliest next run across a rule, or keep one row per interval and widen the row's identity to `(workflow, node, intervalIndex)`. Take the second — `ClaimDue` is already a correct transactional claim that advances one `NextRunAt` per row, and one row per interval keeps it untouched, at the cost of a fan-out on sync. Either way this is a schema change, so it wants p6-1's versioned migrations rather than AutoMigrate.

Timezone next. `robfig/cron` v3.0.1 already parses a `TZ=` or `CRON_TZ=` prefix on the spec string (`parser.go`), so a per-schedule zone can be carried without changing the parser — store the zone on the schedule row, prefix the spec when calling `Next`, and validate the zone name against `time.LoadLocation` at save time. Loosen the parser to allow seconds only if the seconds interval is actually implemented; otherwise map "every N seconds" to a diagnostic rather than silently rounding it up to a minute, which is the failure mode a user notices last.

Then wire activation. Add a schedule extractor beside `store.webhooks` in `internal/repository/workflows.go` and a `syncSchedules` next to `syncWebhookBindings`, so a Schedule Trigger in an activated document becomes rows in the same transaction that pins the version. This is the single change that makes an imported schedule-driven workflow work at all, and it is easy to leave out because the node already exists and the editor already renders it.

The trigger item is a small change in the wrong place. `Tick` in `internal/scheduler/scheduler.go` marshals the payload and `executeSchedule` in `nodes/webhook.go` emits `request.Input` verbatim, so build n8n's field set in `Tick` where the fire time and the zone are both known, rather than in the executor where the zone is not.

DateTime and Wait go in a new `nodes/datetime.go` and `nodes/wait.go`. DateTime overlaps p1-10's `$now`/`$today` — the node must not grow a second date implementation, so put the parsing and formatting in one package both can call. Wait's bounded modes are a `time.Sleep` against the node context, which means a wait longer than the execution timeout fails as a timeout rather than hanging; enforce a documented ceiling and say what it is in the node description instead of letting the user discover it.

## References

- Roadmap plan, p4 section: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the p4 "time and control" family; entry V2-p1-10 (expression engine v2); entry V2-p6-1 (versioned migrations); entry V2-p8-4 (human approval / Wait with durable resume).
- PRD `gflow-prd-v1.md` §24 Native V1 Nodes, §33 Expressions, §35 Workflow Execution Model.
- Verified in this repository: `nodes/webhook.go` (`scheduleTrigger`, `executeSchedule`, `validateScheduleConfiguration`), `internal/scheduler/scheduler.go` (`parser`, `Next`, `Validate`, `Tick`), `internal/repository/models.go` (`scheduleModel`), `internal/repository/schedules.go` (`Schedule`, `ClaimDue`), `internal/repository/workflows.go` (`Activate` calls `syncWebhookBindings` and nothing else), `internal/api/handlers/schedules.go`, `internal/interop/n8n/parameters.go` (`scheduleToKilas`, `scheduleToN8N`), `internal/expression/expression.go` (root allowlist).
- n8n 2.34.0 reference checkout (read-only, outside this repo): `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/Schedule/ScheduleTrigger.node.ts` (`version: [1, 1.1, 1.2, 1.3]`, the emitted item's field names) and `.../Schedule/GenericFunctions.ts` (`withIntervalDefaults` defaults, `validateInterval` ranges).
- `github.com/robfig/cron/v3@v3.0.1` `parser.go`: `TZ=` / `CRON_TZ=` spec prefix.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .gitignore                                         |    10 +
 .pine/MEMORY.md                                    |     4 +
 .pine/memory/licensing.md                          |    12 +
 .pine/memory/live-databases.md                     |    30 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/roadmap.md                                   |  1161 ++
 .pine/tickets/EPIC-m42s3g.md                       |    79 +
 .pine/tickets/FEAT-0556ck.md                       |    66 +
 .pine/tickets/FEAT-096vs9.md                       |    53 +
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |    65 +
 .pine/tickets/FEAT-1500sp.md                       |    58 +
 .pine/tickets/FEAT-1axhdn.md                       |    65 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |    73 +
 .pine/tickets/FEAT-27km39.md                       |    71 +
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |    68 +
 .pine/tickets/FEAT-347egc.md                       |    54 +
 .pine/tickets/FEAT-3taswf.md                       |    67 +
 .pine/tickets/FEAT-3xqky1.md                       |    70 +
 .pine/tickets/FEAT-45tfmh.md                       |    68 +
 .pine/tickets/FEAT-48hreg.md                       |    67 +
 .pine/tickets/FEAT-4d0bje.md                       |    62 +
 .pine/tickets/FEAT-53fht8.md                       |    60 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fhj6p.md                       |    69 +
 .pine/tickets/FEAT-5fv8gf.md                       |    59 +
 .pine/tickets/FEAT-5kfctc.md                       |    66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   118 +
 .pine/tickets/FEAT-5mvech.md                       |    72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-5z37xh.md                       |    72 +
 .pine/tickets/FEAT-68zzqs.md                       |    65 +
 .pine/tickets/FEAT-6vfn3s.md                       |   395 +
 .pine/tickets/FEAT-7cg0cd.md                       |    60 +
 .pine/tickets/FEAT-7tgasa.md                       |    61 +
 .pine/tickets/FEAT-8qyfh1.md                       |    53 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   141 +
 .pine/tickets/FEAT-9555xz.md                       |    58 +
 .pine/tickets/FEAT-96p7m3.md                       |    52 +
 .pine/tickets/FEAT-9dqn7d.md                       |   422 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a94c8y.md                       |    60 +
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   113 +
 .pine/tickets/FEAT-agj52c.md                       |    64 +
 .pine/tickets/FEAT-ajw7wt.md                       |    61 +
 .pine/tickets/FEAT-az620p.md                       |    54 +
 .pine/tickets/FEAT-bp0ytb.md                       |   376 +
 .pine/tickets/FEAT-bscygc.md                       |    62 +
 .pine/tickets/FEAT-c2a081.md                       |    55 +
 .pine/tickets/FEAT-cgm1y3.md                       |    50 +
 .pine/tickets/FEAT-cjpbe6.md                       |    70 +
 .pine/tickets/FEAT-cpdp8y.md                       |    70 +
 .pine/tickets/FEAT-csqgg5.md                       |   145 +
 .pine/tickets/FEAT-cwz4ac.md                       |    66 +
 .pine/tickets/FEAT-cx3hq1.md                       |    71 +
 .pine/tickets/FEAT-czbzs6.md                       |    65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    63 +
 .pine/tickets/FEAT-de8d4c.md                       |    71 +
 .pine/tickets/FEAT-ed6wdy.md                       |    66 +
 .pine/tickets/FEAT-ej0468.md                       |    54 +
 .pine/tickets/FEAT-frvez8.md                       |    70 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |    64 +
 .pine/tickets/FEAT-gg85se.md                       |    69 +
 .pine/tickets/FEAT-gjzgkd.md                       |    59 +
 .pine/tickets/FEAT-gvn62x.md                       |    57 +
 .pine/tickets/FEAT-gxppx1.md                       |    71 +
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |    56 +
 .pine/tickets/FEAT-jq84xk.md                       |    67 +
 .pine/tickets/FEAT-jwhdsy.md                       |   444 +
 .pine/tickets/FEAT-k3fmj1.md                       |   141 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |    60 +
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    56 +
 .pine/tickets/FEAT-kwxxd0.md                       |    64 +
 .pine/tickets/FEAT-m94hhx.md                       |    60 +
 .pine/tickets/FEAT-mvegj5.md                       |    56 +
 .pine/tickets/FEAT-n19dch.md                       |    66 +
 .pine/tickets/FEAT-n5fdz3.md                       |    69 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nc6z9r.md                       |    68 +
 .pine/tickets/FEAT-nch9dg.md                       |    67 +
 .pine/tickets/FEAT-nrfg6e.md                       |    69 +
 .pine/tickets/FEAT-nrfz6m.md                       |    64 +
 .pine/tickets/FEAT-nxxbs5.md                       |    77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-pnbt4z.md                       |    91 +
 .pine/tickets/FEAT-ptyh9w.md                       |    65 +
 .pine/tickets/FEAT-q81bq4.md                       |   115 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |   136 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-sdjdh2.md                       |    33 +
 .pine/tickets/FEAT-sfy1tq.md                       |    63 +
 .pine/tickets/FEAT-snxxny.md                       |   409 +
 .pine/tickets/FEAT-sp8cfm.md                       |   396 +
 .pine/tickets/FEAT-ss44d9.md                       |    67 +
 .pine/tickets/FEAT-t26rt7.md                       |    65 +
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |   432 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |    67 +
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |   327 +
 .pine/tickets/FEAT-xr7ga9.md                       |    76 +
 .pine/tickets/FEAT-xx6p22.md                       |    62 +
 .pine/tickets/FEAT-ybm2pd.md                       |    55 +
 .pine/tickets/FEAT-ykyfbd.md                       |    72 +
 .pine/tickets/FEAT-yx0qt6.md                       |    71 +
 .pine/tickets/FEAT-yyjfjq.md                       |   123 +
 .pine/tickets/FEAT-za118x.md                       |    61 +
 .pine/tickets/FEAT-zmfsjd.md                       |    71 +
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |   384 +
 Makefile                                           |    19 +
 README.md                                          |    33 +
 cmd/kilasflow/main.go                              |   128 +-
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
 internal/api/credentials_test.go                   |   357 +
 internal/api/handlers/credentials.go               |   298 +
 internal/api/handlers/executions.go                |     1 +
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |   119 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/routes.go                             |    28 +-
 internal/api/server.go                             |    26 +-
 internal/api/workflows_test.go                     |    73 +-
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/conditions/conditions.go                  |   542 +
 internal/conditions/conditions_test.go             |   231 +
 internal/conditions/doc.go                         |    26 +
 internal/config/config.go                          |   101 +-
 internal/config/config_test.go                     |    35 +
 internal/credentials/builtin.go                    |   153 +
 internal/credentials/credentials.go                |    98 +-
 internal/credentials/credentials_test.go           |   242 +
 internal/credentials/registry.go                   |   340 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   858 +-
 internal/engine/runner_test.go                     |  1229 +-
 internal/engine/service.go                         |    85 +-
 internal/engine/service_test.go                    |    47 +-
 internal/engine/worker_test.go                     |     4 +-
 internal/execution/records.go                      |    55 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    82 +-
 internal/expression/expression.go                  |   350 +-
 internal/expression/expression_test.go             |   374 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/interop/n8n/corpus/BASELINE.md            |   104 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   435 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   574 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |  1018 +-
 internal/interop/n8n/n8n_test.go                   |  1906 +++-
 internal/interop/n8n/parameters.go                 |  1266 ++-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   716 +-
 internal/node/registry_test.go                     |   763 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   299 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |    48 +-
 internal/repository/models.go                      |   113 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/schedules.go                   |   140 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/repository/workflows.go                   |    66 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   412 +
 internal/routing/response.go                       |   177 +
 internal/routing/routing.go                        |   373 +
 internal/routing/routing_test.go                   |   764 ++
 internal/scheduler/scheduler.go                    |   147 +-
 internal/scheduler/scheduler_test.go               |   194 +-
 internal/sqlnode/sqlnode.go                        |   305 +-
 internal/sqlnode/sqlnode_test.go                   |   106 +
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   322 +-
 internal/webhook/webhook_test.go                   |   473 +-
 internal/workflow/compiler.go                      |   432 +-
 internal/workflow/compiler_test.go                 |    97 +
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 nodes/ai.go                                        |    64 +-
 nodes/ai_test.go                                   |    28 +-
 nodes/annotation.go                                |    62 +
 nodes/assignments.go                               |   180 +
 nodes/bindings_test.go                             |   129 +
 nodes/code.go                                      |    32 +-
 nodes/code_test.go                                 |    74 +-
 nodes/conditions.go                                |   139 +
 nodes/core.go                                      |   204 +-
 nodes/database.go                                  |   257 +-
 nodes/database_test.go                             |   540 +-
 nodes/executors.go                                 |   598 +-
 nodes/executors_test.go                            |   480 +
 nodes/flow.go                                      |   457 +
 nodes/flow_test.go                                 |   464 +
 nodes/http.go                                      |   197 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/telegram.go                                  |   393 +
 nodes/telegram_download.go                         |   243 +
 nodes/telegram_lifecycle.go                        |   423 +
 nodes/telegram_test.go                             |   610 +
 nodes/transform.go                                 |   745 ++
 nodes/transform_test.go                            |   315 +
 nodes/unsupported.go                               |   116 +-
 nodes/webhook.go                                   |   162 +-
 packs/telegram/README.md                           |    40 +
 packs/telegram/pack.json                           |  1119 ++
 packs/telegram/telegram.go                         |    58 +
 packs/telegram/telegram_test.go                    |   466 +
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
 packs/waha/waha_test.go                            |  1196 ++
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/corpus-sync.sh                             |   225 +
 sdk/src/generated/models.ts                        |   673 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |   199 +-
 .../lib/api/generated/models/activationNotice.ts   |    13 +
 .../lib/api/generated/models/activationResource.ts |    23 +
 .../models/{unsupported.ts => assignment.ts}       |     9 +-
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
 web/src/lib/api/generated/models/field.ts          |     1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    30 +-
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
 .../lib/api/generated/models/propertyDefinition.ts |    14 +
 web/src/lib/api/generated/models/propertyGroup.ts  |    15 +
 .../api/generated/models/testCredentialResource.ts |    17 +
 .../lib/api/generated/models/testPayloadBody.ts    |    22 +
 .../api/generated/models/testPayloadBodyFields.ts  |    12 +
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
 .../workflow-editor/properties-panel.svelte        |    48 +-
 .../workflow-editor/property-field.svelte          |   264 +-
 .../workflow-editor/workflow-editor.svelte         |     4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |    82 +
 web/src/lib/workflow-editor/assignments.ts         |    89 +
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |     4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    86 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   218 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 352 files changed, 81230 insertions(+), 1596 deletions(-)
```
