---
title: Code (JavaScript)
description: What runs when a Code node's JavaScript runs in KilasFlow, how it differs from n8n, the helpers and static data it offers, and the limits and configuration keys that bound it.
---

**Code (JavaScript)** (`kilasflow.jsCode`) runs n8n-style JavaScript: the body
of an n8n Code node you imported, or one you added from the palette. It is not
translated and no Node.js is involved. The body runs as written on
[goja](https://github.com/dop251/goja), an ECMAScript engine written in Go and
linked into the KilasFlow binary, in a worker process the server starts from its
own executable. Nothing is installed beside the server, and there is no npm.

This page is the reference for that node. What happens to a Code node when a
workflow is imported is in the [migration guide](/guides/n8n-migration/#the-code-node);
what confines the worker processes is in
[safety boundaries](/concepts/safety-boundaries/#the-javascript-code-node).

## What the code sees

The node keeps n8n's two modes and n8n's names for them. The body is the body
of an async function, so `await` works, and what it returns becomes the node's
items.

| Mode | The code gets | It returns |
| --- | --- | --- |
| Run once for all items (`runOnceForAllItems`, the default) | `items`, `$input.all()`, `$input.first()`, `$input.last()`, and `$json`, `$binary` and `$itemIndex` of the first item | a list of items; a single object is taken as one item |
| Run once for each item (`runOnceForEachItem`) | `$json`, `$binary`, `$itemIndex`, `$input.item` and `item`, the item itself, called once per item | one item, or `null` to drop it; a list is refused |

An item is `{ json: { … } }`, and a plain object returned where an item belongs
becomes that item's `json`. As in n8n, code that runs once for all items still
has the per-item roots, read at the first input item: `$json` is
`items[0].json` itself, `$binary` a copy of its files' metadata, and
`$itemIndex` (and the older `$position`) is 0; with no input items, `$json`
and `$binary` are undefined. `items`, and `item`, are what the other mode does
not have: undefined there, as in n8n, so `typeof items` is safe, and using
`items` anyway fails with a message saying what to use instead. The code may
declare a name a root already has, so `const items = $input.all()` works.

The rest of n8n's Code-node globals are there, reading the same data an
[expression](/concepts/expressions/) reads:

- `$('Name')` and `$node['Name']` read a node that ran earlier: `.first()`,
  `.last()`, `.all()`, `.item`, `.itemMatching(index)` and `.params`. `.item`
  follows the paired-item lineage and fails with the reason when it cannot be
  established, as it does in an expression. `.all()`, `.first()` and
  `.last()` read the output of the named node that this node is connected
  to, as n8n's do, however many nodes sit in between: after an IF's `false`
  branch they read the false items. A node this one is not connected to is
  read at its first output. `.all(branch, run)`, `.first(branch, run)` and
  `.last(branch, run)` read another output, such as the `true` branch as
  `.all(0)`. n8n's older
  `$items('Name', output, run)` reads output 0 unless told otherwise, and
  `$items()` the node's own input, as in an expression. Only a node's latest
  run is kept, numbered as `$runIndex` numbers runs: a run argument may name
  it by its number (so `$items('Name', 0, $runIndex)` in a loop that runs in
  step works) or as `-1`, and naming an earlier run is refused.
- `$workflow`, `$execution` (`id`, `mode`, `resumeUrl`), `$runIndex`,
  `$nodeVersion`, and `$env`, which holds only the variables an expression's
  `$env` holds. `$vars` is an empty object: KilasFlow has no variables for it
  to read.
- `$now` and `$today`, and Luxon's `DateTime`, `Duration` and `Interval`,
  default to the workflow's time zone.
- `console.log`, `info`, `warn`, `error` and `debug`. What the code prints is
  kept with the node's run and shown in the **Console** tab of that node on the
  execution page, and it is streamed live as the `code.console` event. It is
  kept when the code then fails too, which is usually when you need it.
- `Buffer`, `URL`, `URLSearchParams`, `TextEncoder`, `TextDecoder`, `atob`,
  `btoa`, `structuredClone`, `queueMicrotask`, `Intl`, `crypto`, and timers
  that end with the run.
- `require()` returns the modules the runtime ships: `lodash` (4.17), `luxon`
  (3.7), `crypto`, `util`, `buffer` and `url`. `crypto` is Node's API for
  hashes, HMACs, random values, `timingSafeEqual`, PBKDF2, scrypt and AES in
  the CBC, CTR and GCM modes; the rest of Node's crypto is refused by name.

A returned item keeps its lineage the way n8n decides it: an explicit
`pairedItem` wins; an item the code was given and returned, in whatever order,
keeps its own; anything else is paired by position.

## Helpers and static data

Three of n8n's `this.helpers` run, and each is carried out by the server, never
by the code's worker process: the code asks, the server does the work, and the
promise the code holds settles with the answer. While it waits the code runs
on — its timers fire and other promises settle — and requests started together
with `Promise.all` are in flight together.

- `this.helpers.httpRequest(options)` sends a request under the deployment's
  egress policy, exactly the one an HTTP Request node uses (`outbound.*`), so
  an internal address is refused unless the operator granted it. It takes
  n8n's options `url`, `baseURL`, `method`, `headers`, `qs` (with
  `arrayFormat`), `body`, `json`, `auth`, `timeout` (which can shorten the
  policy's, never lengthen it), `disableFollowRedirect`, `maxRedirects`,
  `returnFullResponse` (for `{ body, headers, statusCode, statusMessage }`),
  `encoding` (`arraybuffer` for a `Buffer`, `text` or `json`) and
  `ignoreHttpStatusErrors`. A status outside 2xx rejects as n8n's does, with
  an error named `AxiosError` whose message reads "Request failed with status
  code 404", whose `status` is the status and whose `code` is
  `ERR_BAD_REQUEST` for a 4xx and `ERR_BAD_RESPONSE` for any other. As in
  n8n, the error carries no `response`: to read the body of such a status,
  pass `ignoreHttpStatusErrors: true` with `returnFullResponse: true` and
  check `statusCode`. `proxy`,
  `skipSslCertificateValidation` and the rest of the options that would change
  what the request does are refused by name rather than ignored. No
  credential is ever reachable, as in n8n.
- `this.helpers.getBinaryDataBuffer(itemIndex, propertyName)` returns a
  `Buffer` of a file of the node's own input: the one item `itemIndex` holds
  under `propertyName`. No other file is readable.
- `this.helpers.prepareBinaryData(buffer, fileName?, mimeType?)` stores the
  bytes as a file of the execution and returns its reference, which the code
  can return in an item's `binary`. The type, when not given, is the one the
  name says, else what the bytes look like, else `text/plain`.

Every helper call counts against a budget of host calls
(`code.javascript_max_host_calls`, 100 by default): per run in *Run once for
all items* mode, and per item in *Run once for each item* mode, so a per-item
node may call an API for every item. A file or request body moves at most
32 MiB in one call, and a response larger than the policy's
`outbound.max_response_bytes` (or 32 MiB) stops the node. Each is a named
error. Time spent waiting for the server is not counted against the node's
time limit; the execution's own timeout still bounds it, and the host-call
budget bounds how often the code can wait. Any other helper, such as
`this.helpers.request`, is refused.

`$getWorkflowStaticData('global')` returns the workflow's static data, an
object the code can change, and `$getWorkflowStaticData('node')` the node's
own. Every Code node run in an execution sees what the ones before it left.
What it holds is saved, when a run changed it, as n8n saves it: when the
execution ends, whether it succeeded or failed, and when it parks at a Wait,
so the half that resumes reads what the half before changed.

- A **manual run** saves nothing: a test run reads and changes it for itself,
  as n8n documents. A run started through the API is queued as a manual run,
  as a run from the editor is, so it saves nothing either. That is a
  difference from n8n, where an API-started run is not a manual one.
- A cancelled run saves nothing, and nor does a Code node run that throws:
  what it changed before throwing is dropped.
- A retry runs under the trigger its original ran under, so a retried webhook
  run saves and a retried manual one does not; a retried sub-workflow run has
  no caller, so it runs, and counts, as a manual one.
- The data is capped at 256 KiB as JSON (a named error, on the node run that
  grew it past the cap), and it is deleted with its workflow. Static data an
  imported n8n workflow carried is not imported.

## What is refused, and when

Some JavaScript the engine would run differently from V8 — or not at all — and
running it anyway would mean a workflow that succeeds with the wrong answer. So
each such construct is refused by name, in the same sentence a Python Code node
gets:

> this node's code uses the regular-expression flag "v" (line 3), which this
> server does not run. Rewrite that part of the code, or do the same work with
> native nodes.

Most are found by reading the code — at import, as a blocking diagnostic, and
again when the workflow is saved — so a workflow that uses one never activates:

- an async generator, `for await`, and `import` or `export` (use `require()`);
- the regular-expression flags `v` and `d`, and `\p{…}` property escapes under
  the `u` flag, which the engine accepts and then matches nothing with — a
  pattern built at run time is checked when it is built;
- `this.getCredentials`, and any `this.helpers` function other than the
  three [above](#helpers-and-static-data): use an HTTP Request node before or
  after the Code node;
- `require()` of any module not in the list above;
- a `<!--` or `-->` in the rare code the runtime cannot read unambiguously
  around it, so it cannot tell for certain whether one starts a comment.
  Anywhere else these HTML-like comments are read as V8 reads them.

The rest fail by name the moment the code reaches them: `$jmespath`,
`$evaluateExpression`, `$prevNode`, `$input.params`, `$input.context`,
`$secrets`, `$execution.customData`, and `$('Name').all()` or
`$items('Name')` for a run earlier than the node's latest. So does a date formatted in a locale other than `en`, `en-US`, `en-CA` or `en-GB` (see [below](#differences-from-n8n)), and a `Proxy` handed to a
built-in that reads its length, which would run the proxy's traps once per
element inside one call that nothing can interrupt.

A run argument to `$items(…)` or `.all(…)` other than a literal `-1` or
`$runIndex` is not noted at import or when the node is saved. `$items('Name', 0, 0)`
is right on the node's first run and refused on a later one, and which run is
the latest is known only when the code reaches the read.

A body longer than 128 KiB, nested more than a thousand levels deep, holding
more than a thousand arrow functions, or holding a constant expression that
would take the engine seconds to fold, or a BigInt constant of more than a
million bits, is refused the same way, because reading it safely matters more
than running it.

Reading the code is not compiling it, so the few mistakes only a compiler sees
— a `let` declared twice in one scope, a `break` outside a loop — are reported
when the node runs, as a `SyntaxError` with its line, not when it is saved.

## Differences from n8n

- **It is an interpreter.** goja has no JIT, so CPU-heavy code — a tight loop
  of arithmetic, a hand-written parser, hashing in JavaScript — runs ten to
  fifty times slower than on V8. Code that shapes data runs at ordinary speed:
  a transform over 1,000 items takes about 20 ms end to end
  ([measured below](#performance)). A body that is mostly arithmetic in a loop
  is better done by native nodes or the Go Code node, and one that genuinely
  needs longer can be given it with `code.javascript_timeout`.
- **There is no npm install.** n8n's `NODE_FUNCTION_ALLOW_BUILTIN` and
  `NODE_FUNCTION_ALLOW_EXTERNAL` have no equivalent: `require()` answers from
  the fixed list above, and there is no module directory to add a package to.
  `moment` is not shipped; Luxon is. A module that is not on the list is
  refused when the workflow is imported or saved, not when it runs.
- **Dates format in four English locales.** Luxon and `Intl` format dates in
  `en`, `en-US`, `en-CA` and `en-GB`. Asking for any other date locale is a
  named error, never English passed off as that locale. Numbers format in the
  locale you ask for, in any locale CLDR knows. `Intl.RelativeTimeFormat`
  refuses every locale; Luxon's `toRelative()` gives English relative times
  by itself. Zone short and long names follow the higher offset of a zone
  that keeps two, so `Europe/Dublin` matches Node whichever form of the time
  zone database the host has.
- **Memory is bounded per worker, not per run.** goja keeps a script's objects
  on the Go heap and cannot account for one script's share of it. So memory
  is bounded in layers: the input, the returned items and the console output
  each have a cap, and each is a named error rather than a truncation; a
  built-in asked to allocate or loop as far as a number tells it refuses a
  huge number up front with a `RangeError`; and a worker whose live heap
  passes its ceiling (`code.javascript_heap_ceiling_mb`, 1 GiB by default)
  stops its script with a memory-limit error. One built-in call can still grow
  the heap between the watchdog's samples; on Linux the worker's address-space
  limit and the kernel end that, and it costs the worker, never the server.
- **Every run gets a fresh engine.** Nothing one execution sets — a global, a
  patched `Array.prototype`, a replaced `JSON.parse`, a library's settings —
  reaches the next, whoever it runs for, although a worker process runs many
  executions in turn.
- **Scripts run in workers, apart from the server.** A script that hangs in a
  built-in or runs out of memory costs its worker, which the server kills and
  replaces, never the server and never another script. On Linux each worker
  is also a privilege boundary, as far as the kernel grants one: see
  [what confines a worker](/concepts/safety-boundaries/#what-confines-a-worker).
- **Binary data is metadata.** An item's `binary` entries carry `id`,
  `fileName`, `mimeType`, `fileExtension` and `fileSize`, never the bytes, so
  code that reads `binary.data.data` fails instead of reading nothing; read
  the bytes with `this.helpers.getBinaryDataBuffer`. An item keeps a file only
  when the code returns it, as in n8n, and the code can pass on or rename a
  file it was given or stored with `prepareBinaryData`, but not name any other.
  An entry with no `id` whose `data` is base64 text, beside an optional
  `fileName` and `mimeType`, is a file given inline, the way n8n code written
  before `prepareBinaryData` makes one: the server stores it as
  `prepareBinaryData` stores a file, after the code has finished, and it
  counts as one host call. Base64 wrapped in lines, URL-safe or without its
  padding is read; `data` that is not base64, or a `mimeType` that is not a
  media type, is refused with the item named rather than stored as different
  bytes. A file already stored for a run that then fails — one
  `prepareBinaryData` wrote, or an inline file stored before a later one
  could not be — stays in that execution's storage, with nothing referencing
  it, until the execution's storage is removed. It does not outlive the
  execution.
- **The time limit counts the code's own running time.** Starting the engine,
  loading a library, handling the input and output, and waiting for the
  server to answer a helper are not counted. The deployment sets the ceiling
  (`code.javascript_timeout`, 10 seconds by default; n8n's task runner allows
  300), and the node's **Time limit** can lower it, never raise it.
- **Runaway recursion cannot be caught.** V8 throws a `RangeError` the code can
  catch; here, calling more than 10,000 functions deep ends the run with a
  named error.
- **An uncaught error fails the node, as it does in n8n.** An exception thrown
  from a timer's callback, or a promise rejected with nothing to handle it,
  fails the run with that error, marked *Uncaught*, if the code is still
  running once the queue of promise jobs drains. A helper's promise is the
  code's to handle, as in Node.
- **A promise that can never settle is an error, not a hang.** Code that
  awaits something nothing will ever resolve fails at once with a message
  saying so, rather than waiting out the time limit.
- **Continuing on failure works as n8n's does, without splitting the
  batch.** Other nodes that continue on failure are run once per item so one
  bad item fails alone. The Code node always sees its whole batch, so an
  all-items body that sums its items sums all of them, and a throw there fails
  the batch, as in n8n: the node answers with **one** error item, whatever
  the number of input items, so the node after it runs once. That item holds
  no input item's fields, on either output. In **Run Once for Each Item**
  mode the node goes on past an item whose code threw or returned something
  that is not an item: that item goes to the error output with its own
  fields beside the error (or on as an error item in its place, under
  *Continue*), and the other items pass through. Either way the error item's
  `error` is the text n8n's Code node writes: the error's message and the
  line it was thrown on, such as `out of stock [line 3]` or
  `Cannot read properties of undefined (reading 'id') [line 2]`, without the
  error's type or the item, so `{{ $json.error }}` reads that text. (The
  run's own error, on the node's row, still names both.) Other nodes' error
  items carry an object with `message` and `node` instead. A failure of the
  code itself is the node's answer and is not retried; the server failing to
  run the code at all (a worker that crashed or could not start) is retried
  and tolerated as any node's failure is, one error item per input item.
- **Most error messages match V8's; a few don't.** The errors code usually
  meets carry Node's wording: `JSON.parse` on text that is not JSON, reading a
  property of `undefined`, and calling something that is not a function
  (`items.map is not a function`). That holds whether the code catches the
  error or not. An error the code builds itself keeps the words it was given. The engine cannot tell `null` from `undefined` on a property
  read, so a read of `null` says "of undefined" where V8 says "of null".
  Rarer errors (destructuring or iterating `undefined`, the `in` operator on
  a primitive) keep the engine's own words, as does an error the code only
  sees in a promise's `.catch()` handler.
- **A refused status is an `Error`.** n8n's helper error reaches the code as
  a plain object, because it crosses from n8n's main process to its task
  runner as JSON; here it is an `Error` with the same `name`, `message`,
  `code` and `status`. So `err instanceof Error` is `true` where n8n says
  `false`, and the error has no `config`, n8n's copy of the request's
  settings.
- **Stack traces are text.** `err.stack` is a string, `Error.prepareStackTrace`
  is never called and `Error.captureStackTrace` does not exist, so code that
  inspects V8's call-site objects has nothing to inspect.

The Sort node's **Code** comparator runs on the same runtime, in the same
workers and under the same limits; how it differs from n8n's is in the
[migration guide](/guides/n8n-migration/#the-sort-nodes-comparator).

## Limits and configuration

Every bound a deployment can set is a key under `code.` (the environment
variable is `KILASFLOW_CODE_` and the rest of the key in capitals). The
[configuration reference](/operate/configuration-reference/) has each key's
full description.

| Bound | Default | Key |
| --- | --- | --- |
| Whether the node runs at all | on | `code.javascript_enabled` |
| The program's own running time | 10 s | `code.javascript_timeout` (at most 5 m) |
| Input, as JSON | 32 MiB | `code.javascript_max_input_bytes` |
| Returned items, as JSON | 16 MiB | `code.javascript_max_output_bytes` |
| Console output kept | 64 KiB | `code.javascript_max_console_bytes` |
| Helper calls (`this.helpers`), per run or, in per-item mode, per item | 100 | `code.javascript_max_host_calls` |
| Live heap, per worker | 1 GiB | `code.javascript_heap_ceiling_mb` |
| Scripts running at once, and so workers | one per CPU | `code.javascript_max_concurrent` |
| The user and group every worker runs as, on Linux | unset | `code.javascript_worker_uid`, `code.javascript_worker_gid` |

A node may **tighten** the time limit through its own **Time limit** parameter
and can never raise it. A limit reported for work that plainly does not take
that long means something has crept inside the clock, and is a bug.
`code.javascript_enabled: false` turns the node off: it is greyed out in the
editor and every run is refused, naming the key. More scripts than
`code.javascript_max_concurrent` wait their turn rather than fail. Budget up to
that many workers on top of the server, each idling at a few tens of MiB and
allowed a live heap up to its ceiling while it runs; see
[deployment](/operate/deployment/).

These bounds are fixed:

| Bound | Value |
| --- | --- |
| A body's length | 128 KiB |
| How deeply a body nests | 1,000 levels |
| Arrow functions in a body | 1,000 |
| How deeply functions call each other | 10,000 calls |
| What one built-in call may walk or build | 8,388,608 array elements; 33,554,432 characters from `repeat` and padding; 64 MiB of typed array or `ArrayBuffer` |
| Timers armed at once | 10,000 |
| A file or request body moved by one helper call | 32 MiB |
| Static data, per kind, as JSON | 256 KiB |

A worker still running at twice its time limit plus five seconds is stuck in
something its own clock cannot stop, and the server kills it; the run fails
with the time-limit error. A backtracking regular expression is the one piece
of work the clock cannot interrupt, so it can overrun its limit by up to the
ceiling, which is why the ceiling is capped at five minutes.

## Security

The node runs code a workflow's author wrote, in a server other tenants share,
so it is reviewed as hostile code. What holds, each backed by a test:

- **A script reaches only what the runtime installs.** The engine has no host
  API of its own, and `internal/jsrun`, where every global is written, may not
  import anything that opens a file, a socket or a process; a test in
  `internal/guardrails` enforces that. A test walks what a script can reach
  and checks it against a reviewed list: each global, including a
  symbol-keyed one, and the own properties of each sampled instance. The
  walk does not call a getter or a function, so a value that exists only as
  a call's result is on the list only when a sample builds it; array indexes
  are one entry; a function's length, name and untouched prototype are left
  out; a vendored library is listed by its root. A new global or property
  fails the build until someone has looked at it. No Go value is reachable
  with a field or method a script could read or call.
- **`eval` and `new Function` stay enabled**, as in n8n, because they reach
  nothing the body cannot: the same global object, the same `require()`, and
  no `process`, `module` or other Node handle, whichever constructor chain
  the code climbs to get a compiler.
- **Pollution stays in its run.** A script may change every built-in it can
  see, and its own result with them, but nothing it changes reaches another
  run, not even the next one in the same worker, and a worker never runs
  another tenant's code at all. The server checks the result it gets back
  itself, so no change to a built-in can make a result name a file the node
  was never given.
- **A worker is treated as hostile.** Every message it sends is checked
  against what its job's code could have produced; one that breaks the
  protocol fails its run and is never used again. See
  [worker processes](/concepts/safety-boundaries/#worker-processes).

## Performance

A Code node's cost is dominated by what its body does. Measured through the
real worker pool with `make js-load`, which runs executions at several
concurrency levels against a pool of one worker per CPU and reports the
latency each execution saw, from asking for its run to having its items:

| Body | At once | p50 | p95 | p99 | Executions per second |
| --- | --- | --- | --- | --- | --- |
| `return items` | 1 | 0.80 ms | 0.97 ms | 1.0 ms | 1,331 |
| `return items` | 10 | 2.2 ms | 3.5 ms | 4.0 ms | 4,540 |
| `return items` | 40 | 8.6 ms | 10.7 ms | 11.6 ms | 4,527 |
| 1,000-item transform | 1 | 18.7 ms | 19.7 ms | 21.0 ms | 53 |
| 1,000-item transform | 10 | 43.3 ms | 61.6 ms | 84.3 ms | 219 |
| 1,000-item transform | 40 | 177 ms | 204 ms | 226 ms | 219 |

Those are 400 executions per row on an Apple M4 (ten cores, four of them
performance cores) with 24 GiB, under macOS and Go 1.27, on a machine shared with
other work whose one-minute load average was 1.9 when the run started; read
them as proportions rather than promises. The transform trims, upper-cases,
multiplies, filters a list and formats a date for each of 1,000 items. The
workers are warm, as a running server's are; confinement on Linux adds about a
quarter of a millisecond to a worker's cold start. Past one execution per CPU,
executions queue for a worker, so throughput holds and latency grows with the
queue.

Of the JavaScript Code nodes in the 500 most-viewed n8n templates, 99.4% pass
parsing and analysis, and their median run takes about half a millisecond. The
scoreboard, rescored whenever the runtime changes, is
`internal/jsrun/corpus/BASELINE.md` in the repository.
