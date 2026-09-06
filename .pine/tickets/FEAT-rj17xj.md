---
id: FEAT-rj17xj
title: Add human approval with durable wait and resume
status: doing
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-sar60r
    - FEAT-q81bq4
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:13:37Z"
updated: "2026-09-06T04:18:02Z"
---

## Scope

KilasFlow executions run to completion or they die. `internal/execution/records.go` defines six statuses — `queued`, `running`, `cancelling`, `succeeded`, `failed`, `cancelled` — and none of them means "suspended". `internal/engine/runner.go` walks the compiled IR in a single topological pass holding `completed map[string]workflow.NodeOutput` in process memory, and `Service.runOnce` in `internal/engine/service.go` persists `result.NodeRuns` only after the whole run has returned. There is no point at which an execution can stop, give up its worker, and be picked up again later.

That closes off a whole class of real automations. A support workflow that drafts a WhatsApp reply and wants a human to approve it before sending, an onboarding flow that pauses until a document is signed, a nightly job that waits an hour between two steps: all of them need an execution to survive as durable state rather than as a goroutine. PRD §4 explicitly put "human approval orchestration beyond basic extension points" outside V1, and §64 kept "human approval node" on the after-V1 list. This ticket delivers it.

The node is one node with resume modes, matching what n8n's Wait node offers so imported workflows have somewhere to land: resume after a time interval, resume at a specified time, and resume on an inbound HTTP call to a per-execution URL. Human approval is a fourth mode built on the third — a hosted page that records approve or reject, who decided and when, and puts that on the node's output so the next node can branch on it. n8n's own approval nodes emit `{approved, respondedAt, …}`; matching that shape costs nothing and makes an import map cleanly.

The hard part is not the node, it is the resume. An execution that suspends must come back with exactly the data it had, and the two obvious shortcuts both fail. Replaying the graph from the start would re-fire every non-idempotent node that already ran — a second HTTP POST, a second WhatsApp message. Rehydrating from the persisted node runs would come back corrupted, because `payload()` in `internal/repository/executions.go` pushes every durable JSON write through `execution.Redact` by construction, and its own comment names the node-run trace as one of the covered paths. A checkpoint is required, and it must not travel that road.

## Acceptance criteria

- [ ] A `waiting` execution status exists end to end — repository, API responses, the event stream, the executions list — and a waiting execution can still be cancelled.
- [ ] `kilasflow.wait` suspends an execution, releases its worker and its lease, and the suspended run survives a process restart.
- [ ] Resume works by elapsed interval, at an absolute time, and by an inbound HTTP call to a per-execution resume URL.
- [ ] An approval mode presents a page that records approve or reject with the decider and the timestamp, and puts that decision on the node's output as structured data the next node can branch on.
- [ ] Resume continues from the suspension point with the exact upstream data the run held: no already-completed node executes twice, and no value returns as `[redacted]`.
- [ ] A resume URL is unguessable and single-use. A second call, a call for an execution that already resumed, and a call for one that timed out are each refused with their own message.
- [ ] A wait that exceeds its configured limit resolves — either down a timeout output or as a named failure — so no execution can stay suspended forever.

## Implementation Plan

Work outward from storage. Add the `waiting` status and a checkpoint column to the execution model in `internal/repository/models.go`, plus the resume-token and deadline fields the scheduler and the HTTP surface will query. Then teach `internal/engine/runner.go` to stop: an executor needs a way to say "suspend here" that is distinguishable from success and from failure, and `Runner.Run` needs to serialise `completed`, the pending frontier and the suspending node into a checkpoint rather than returning a `Result`. `Service.runOnce` persists the node runs produced so far, writes the checkpoint, sets the record to `waiting` and releases the lease.

Then resume: a path that loads a checkpoint, rehydrates `completed`, and continues the same pass. Then the node itself in `nodes/`, then the resume HTTP surface, then the approval page and a `resumeUrl` field on the `$execution` expression root — `rootValue` in `internal/expression/expression.go` currently returns only `{id, mode}` for it, and a workflow that cannot compose its own resume URL cannot send anyone a link.

Five traps, in the order they will bite.

Redaction is the first and worst. `payload()` in `internal/repository/executions.go` calls `execution.Redact` on every durable JSON write, deliberately, so that no write path can forget it. A checkpoint written through that function comes back with anything matching `isSensitiveKey` or `looksLikeCredential` replaced by `[redacted]`, and the resumed run silently continues on corrupted data. Either land p1-6 first, so redaction lives at the read boundary, or give the checkpoint its own column with its own write path that never calls `payload` — and if the latter, say plainly in the code why this one column is exempt.

Leases are the second. `runOnce` claims with `leaseUntil = time.Now().UTC().Add(service.defaultTimeout)`, and `GORMExecutionStore.ClaimNext` reclaims a `running` record whose lease has expired. A suspended execution must not look like a crashed one: release the lease when suspending, and confirm the reclaim predicate cannot select a `waiting` row.

Third, evidence. Node runs are written in a loop after `service.run` returns, so a workflow that waits four hours shows nothing in its execution inspector for four hours. Persist the completed node runs at the suspension point.

Fourth, routing. `uidx_webhook_bindings_route` in `internal/repository/models.go` is unique on `(method, path)`, so a resume URL must not be modelled as a webhook binding — every suspended execution would contend for one route. Serve resume from its own prefix, keyed by an opaque per-execution token, alongside `/webhook` in `internal/api/routes.go`.

Fifth, the synchronous responder. `Handler.await` in `internal/webhook/webhook.go` polls the execution record until the status is terminal and gives up after `webhook.response_timeout`. A webhook-triggered workflow that suspends for approval cannot answer the original HTTP request. Decide it here and document it: a run that enters `waiting` answers the trigger immediately with an accepted response, and the approval outcome is delivered by whatever the workflow does after it resumes.

One choice to make explicitly: whether a short wait suspends at all. n8n does not — below roughly 65 seconds it holds the process and resumes in place, and offloads to the database only above that. Recommend the same rule with the threshold as a config value, because checkpointing a five-second wait costs two database round trips to save nothing.

## References

- Roadmap plan, p8 section, entry V2-p8-4: `.pine/roadmap.md`.
- PRD: `gflow-prd-v1.md` §4 ("human approval orchestration beyond basic extension points" out of scope for V1), §64 ("human approval node"), §35 Workflow Execution Model.
- n8n documentation (`/n8n-io/n8n-docs`, `docs/integrations/builtin/core-nodes/n8n-nodes-base.wait.md`): resume on After Time Interval, At Specified Time, On Webhook Call, On Form Submitted; execution data offloaded to the database above ~65 seconds; the Limit Wait Time option. Approval response shape from `n8n-nodes-base.slack/approvals.md`: `{data: {approved, respondedAt, …}}`.
- Code: `internal/execution/records.go` (`Status`), `internal/engine/runner.go` (`Runner.Run`, `completed`), `internal/engine/service.go` (`runOnce`, lease window, node-run persistence loop), `internal/repository/executions.go` (`ClaimNext`, `payload`), `internal/execution/redact.go`, `internal/repository/models.go` (`uidx_webhook_bindings_route`), `internal/webhook/webhook.go` (`await`), `internal/expression/expression.go` (`rootValue`, `$execution`).
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 09, 10 — n8n surfaces human review as a first-class picker category and lists Telegram under Human in the Loop. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Work evidence — LongtailSecrets (2026-09-06)

Shipped the wait mechanism as additive engine files only (runner.go/service.go
hot path untouched per Main; hook via the existing wait-node extension point
is integrator work).

- internal/engine/approval.go (new): WaitRegistry with Issue (unguessable
  16-byte single-use tokens, mandatory boundable deadline —
  DefaultApprovalTTL 24h, MaxWaitTTL 7d), Resume (tenant-scoped lookup,
  wrong-tenant answers exactly like unknown, embed-denied, consumed and
  expired each refused with their own stable message), Sweep, ApprovalDecision
  with n8n-shaped Output {approved, respondedAt, decidedBy, note},
  SuspendError (message never carries the token), execution.waiting SSE event
  (non-terminal; carries node+deadline, never token or payload).
- Timeout interplay (BUG-v6tdjr caveat) stated in the file header: suspended
  waits release the worker so execution.default_timeout/MaxWaitDuration stop
  binding; short waits should stay in process (~65s rule), checkpointing a
  five-second wait costs two round trips to save nothing.
- Embed: denied (ticket silent → deny); Resume takes viaEmbed and refuses
  before consuming, so a denied call stays resumable.
- Integrator steps recorded in the file header, NOT done here: waiting status
  + checkpoint column (with its own non-redacting write path — payload()
  would corrupt it), lease release + ClaimNext exclusion, node-run persistence
  at suspension, resume HTTP surface (own prefix, never webhook bindings —
  uidx route collision), approval page + $execution resumeUrl, webhook-trigger
  accepted-response behavior.
- Tests: wait→resume→continue (exact payload preserved, decision output
  shape), second-call consumed, expiry refuses + stable + Sweep reaps,
  unknown vs wrong-tenant identical refusal, embed deny then legit resume,
  deadline bounds, SuspendError token-free, token uniqueness, SSE delivery
  without token/payload leak.
- Suites: internal/engine green (also unblocked AIAgentE2E make build-all —
  intermediate unused-import breakage fixed same pass, build+vet green).
- Docs: operate/security.md note (single-use tokens, expiry, tenancy, embed
  denial, event contents).

## Work evidence — SurfaceWiring (2026-09-06, partial)

Durable wait mechanism landed; HTTP surface + approval page + E2E remain for
the next batch. Ticket stays doing.

Shipped:
- `StatusWaiting` in internal/execution/records.go (non-terminal; feed stays open).
- `execution_waits` table via 000009 both dialects (spaces, quoted idents):
  token_hash unique lookup, retrievable resume_token (cleared on consume),
  verbatim checkpoint (never through redacting payload()), run_count for
  node-run sequence offsets, expiry index, RESTRICT FK to executions.
  postBaselineTables prepended (newest-first, child-first).
- internal/repository/waits.go: SuspendExecution (lease-fenced, one tx),
  ResumeWait (stable distinct refusals: unknown == wrong-tenant, consumed,
  expired; refusals never consume), SettleExpiredWait (timer requeue vs
  named `wait.expired` failure), FindWaitByToken/FindActiveWait,
  LoadResumeState (latest consumed-resumed by auto-increment key),
  ListExpiredWaits. Cancel on waiting completes terminally; ClaimNext
  exclusion documented + tested (allowlist never selects waiting).
- Engine: SuspendError carries Mode/ExpiresAt/NodeID/Checkpoint;
  Checkpoint codec v1 (completed, runs, nodeOutputs, nodeItems, leaves,
  suspend input+attempt); runner extracted to prepareGraph/runLoop/Resume
  with zero behavior change (full engine suite green before/after).
- ExecutionStore extended with the 7 wait methods; worker_test.go fake
  stubbed. Note: out-of-tree ExecutionStore implementers break (in-tree fixed).
- migrate_test adoption guard fixed: it substring-matched baseline names and
  false-positived on `REFERENCES executions(id)`; now matches the table the
  statement itself creates/drops.

Verified: 7 new repo waits tests (single-use, refusals, verbatim checkpoint
with sensitive keys, cancel, restart reopen); sqlite+PG migrate green;
AutoMigrate parity green both dialects (kf-pg-vector 55434); suites green:
engine, repository, database, config, execution, cmd; `go build ./...` green.

Remaining: service runOnce suspend/resume/sweep paths, Record resume-URL
enrichment, resume HTTP surface + waiting across list/SSE, approval page,
$execution resumeUrl, webhook accepted-response (untouched, documented
follow-up), suspend→resume E2E (no double-execute, no [redacted]), kill+resume
restart proof. Webhook/form wait modes and kilasflow.wait SuspendError
wiring are nodes-owned future work.
