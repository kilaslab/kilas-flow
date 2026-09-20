---
id: BUG-gaavr5
title: 'Importer node mapping long tail: AI edges, Date/Chain roles, exporter, Postgres op, Code, notes'
status: done
priority: medium
labels:
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:00Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 17 finding(s) from dims: find:importer-fidelity.

---
### Merge v2 `combinationMode` and v3 `options` (includeUnpaired, ...) are dropped silently, so merges fail or lose items [find:importer-fidelity] (high/bug) · area: importer/merge · confidence: high

mergeToKilas (parameters.go:464-510) copies mode and the v3 combineBy but never reads v2 combinationMode (mergeByFields default / mergeByPosition / multiplex), the v2 chooseBranch output, or any options. KilasFlow resolves 'combine' with no combineBy to combineByFields (executors.go:403-408). No import issue is raised.

Evidence: Live side by side (merge_probe*.py, sbs2_merge-probe*.json) with A=[{a:1},{a:2}] and B=[{b:1}]. Merge 2.1 combine/mergeByPosition: n8n [{a:1,b:1}]; the KilasFlow execution FAILS with 'combining by fields needs at least one field to match on'. Merge 2.1 multiplex: n8n [{a:1,b:1},{a:2,b:1}]; KilasFlow fails the same way. Merge 3 combineByPosition + includeUnpaired: n8n [{a:1,b:1},{a:2}]; KilasFlow [{a:1,b:1}]. Corpus: v2 mergeByPosition in 1862, 2315 and 2324; multiplex in 2320; includeUnpaired in 3066 (x2) and 3135; the chooseBranch output setting in 1934.

n8n behavior: v2 combinationMode maps to v3 combineBy (mergeByFields->combineByFields, mergeByPosition->combineByPosition, multiplex->combineAll). Options are honoured.

Impact: 7/100 templates; merges crash or lose items with no diagnostic.

Suggested fix: Translate v1/v2 modes to v3, and carry or report every option. Export them back.

Files: internal/interop/n8n/parameters.go, nodes/executors.go

---
### Sub-workflow input mappings are dropped silently (Execute Workflow `workflowInputs`, Execute Workflow Trigger declared inputs) [find:importer-fidelity] (high/bug) · area: importer/executeWorkflow · confidence: high

executeWorkflowToKilas never reads `workflowInputs`, the resourceMapper that defines what the caller sends, and raises no issue. executeWorkflowTriggerToKilas honours only an explicit inputSource, but n8n 1.1's default is workflowInputs (checked via /rest/node-types) and exports omit it, so triggers with declared inputs import as passthrough with the declarations discarded.

Evidence: 2878 'Initiate DeepResearch', 'Generate Report' and 'Generate Learnings' map data, jobType and requestId; the imported params are only {itemsPerCall, mode, workflowId}, and the only issue is about the workflow ID. Execute Workflow Triggers with declared inputs import as {"inputSource":"passthrough"} in 2085, 2878, 3050, 3135, 3514, 3770 and 3790; 3443 (jsonExample) likewise. The round trip loses them as well.

n8n behavior: With defineBelow, the sub-workflow receives exactly the mapped fields, evaluated in the caller.

Impact: Every modern sub-workflow call passes the wrong item, and `$json.requestId`-style references break (8+ templates).

Suggested fix: Carry workflowInputs.value into the native node's input mapping, or block with a precise issue. Default EWT >=1.1 to workflowInputs and keep the declared fields.

Files: internal/interop/n8n/parameters.go

Existing tickets: FEAT-az620p (done)

---
### Node names with leading or trailing whitespace lose every connection on import [find:importer-fidelity] (high/bug) · area: importer/connections · confidence: high

Import trims node names (n8n.go:611) and keys idByName by the trimmed name, but importConnections looks up the raw n8n connection keys and targets (n8n.go:882, 919). A node named 'Get ScreenShot ' therefore drops all of its in and out edges with 'a connection starts at/points at X, which is not a node in this workflow'. Export also writes the trimmed names.

Evidence: reimport.json: 2431 ('Clean Webdriver ', 'Google search Query ', 'Get ScreenShot ': 6 edges dropped), 2803 ('Check status of post ': 2 edges), 3291 ('Cleanup HTML ' Set: 2 edges). Each name exists byte-for-byte in the source JSON.

n8n behavior: Names are matched exactly; trailing spaces are legal and the graph stays intact.

Impact: 3/100 templates get a silently disconnected topology, and the round trip renames nodes.

Suggested fix: Keep names verbatim, or key idByName by both the raw and the trimmed name.

Files: internal/interop/n8n/n8n.go

---
### Export collapses Merge inputs 3..N onto input 0 (silent rewiring on round trip) [find:importer-fidelity] (high/bug) · area: exporter/connections · confidence: high

Import resolves Merge inputs input1..inputN through the catalogue, but Export computes the target index with the static inputIndexFor/inputPortsFor table, which only knows input1 and input2 (n8n.go:1085-1090, 1260, 1340). Every edge into input3+ is written with index 0, and no export diagnostic is raised.

Evidence: GET /api/v1/workflows/{id}/export for imported 2454 (Merge1 numberInputs 7), 3066 (4 inputs), 3135, 3291 (3 inputs), 3790 and 5338 (chooseBranch, 9 inputs, 7 edges moved to index 0). The stored documents have the correct ports. Files: work/importer-fidelity/roundtrip/<tid>.export.json.

n8n behavior: The edge index selects the Merge input; append order and chooseBranch depend on it.

Impact: All 6 templates using a Merge with 3+ inputs come back from KilasFlow wired wrongly.

Suggested fix: Resolve input indexes from the catalogue (definition.PortsFor inputs), mirroring outputIndexesFor, and add a round-trip test.

Files: internal/interop/n8n/n8n.go

---
### Typed AI edges touching an unsupported placeholder are discarded on import and therefore lost on export [find:importer-fidelity] (medium/bug) · area: importer/placeholders · confidence: high

The placeholder family kilasflow.unsupported (v1/2/4/8) declares only main ports, so every ai_embedding, ai_document, ai_textSplitter, ai_vectorStore and ai_outputParser edge to or from a placeholder is held back and never stored. The export still claims the placeholder was 'exported back with everything the import preserved'.

Evidence: An edge diff of source vs export for all 100 templates finds 49 AI edges missing (20 ai_embedding, 11 ai_textSplitter, 11 ai_document, 5 ai_vectorStore, 2 ai_outputParser) in 12 templates: 1951, 1960, 2165, 2415, 2465, 2621, 2752, 2753, 2846, 2982, 3986, 4827. For example, 1960's 'Embeddings OpenAI' -> 'Pinecone Vector Store' edge is missing.

n8n behavior: Sub-nodes are attached to their root by typed edges.

Impact: Every RAG template (12/100) round-trips broken, and in the editor the user cannot see which sub-node belonged to which root.

Suggested fix: Give placeholders ports for the observed AI kinds, or keep held-back edges in the capsule and re-emit them on export.

Files: internal/interop/n8n/n8n.go

---
### HTTP Request options are dropped silently, including ones KilasFlow supports (response format, never error, timeout); the auth mode is also lost [find:importer-fidelity] (medium/bug) · area: importer/httpRequest · confidence: high

httpToKilas reads only method, url, query, headers and body; `options` is never read and nothing is reported. The native node already has responseFormat, outputPropertyName, neverError and requestTimeoutSeconds. Unsupported options (fullResponse, allowUnauthorizedCerts, batching, redirect) vanish without a diagnostic. authentication/genericAuthType/nodeCredentialType are also discarded (only a generic lossy note is raised) and are not exported.

Evidence: Corpus usage: fullResponse 12 nodes in 3 templates (1748, 2557, 2878; the output shape changes), responseFormat 7/4, batching 4/2 (5035, 5338), allowUnauthorizedCerts 4/4 (2006, 2275, 2552), timeout 3/3 (2431, 2878, 5171), redirect 3/3, neverError 1 (2006). For example, 2006 'HTTP Request' with options {neverError, allowUnauthorizedCerts} imports as {method, url} with no issue. Auth-mode fields are lost on 79 HTTP nodes (silent.py).

n8n behavior: The options change the output shape, error behaviour, TLS and timeouts.

Impact: Different outputs and errors in about 15 templates, and a manual auth rebuild after binding credentials.

Suggested fix: Map response.responseFormat/outputPropertyName/neverError and timeout (ms -> s), and report every other option by name. Carry the auth mode as a credential-type hint and export it.

Files: internal/interop/n8n/parameters.go

---
### Date & Time: v1 detection is keyed on the `action` field, the v1 output field and token dialect are wrong, and `{{$now}}` is rejected at run time [find:importer-fidelity] (medium/bug) · area: importer/dateTime + internal/datetime · confidence: high

(a) A v1 node that uses the default action 'format' has no `action` key and is imported as v2 getCurrentDate, discarding value and toFormat. (b) v1 writes its result to 'data', but the import defaults outputField to 'date'. (c) v1 toFormat uses moment tokens, which are passed through as Luxon. (d) A whole `{{$now}}` expression resolves to the expression engine's dateValue, which datetime.Parse rejects: 'item 1: 2026-09-19T08:43:46Z is not a date'.

Evidence: Template 1744 (activatable), run side by side (sbs_1744.json). n8n succeeds: '12 Hours from now' -> {"data":"...20:43:45Z"}, 'Format - MMMM DD YY' -> {"data":"September 19 2026"}. The KilasFlow execution fails at '12 Hours from now'. The imported Format node is {operation:getCurrentDate, outputField:date}. Round trip: the exported 1744 runs in n8n but writes `date` and never formats. Code: parameters.go:1173, 1255-1259; expression.go:599-602; parse.go:60-79.

n8n behavior: v1 action defaults to format, dataPropertyName defaults to 'data', tokens are moment-style; $now is a date.

Impact: 1744 is one of only 4 activatable templates and fails at run time. The $now bug affects any native Date & Time node fed from $now.

Suggested fix: Branch on typeVersion, default v1 to action=format and output 'data', and translate moment tokens to Luxon. Make datetime.Parse accept the expression date type.

Files: internal/interop/n8n/parameters.go, internal/datetime/parse.go, internal/expression/expression.go

---
### Summarize output field names and the concatenate separator differ from n8n [find:importer-fidelity] (medium/bug) · area: nodes/summarize + importer · confidence: high

The executor names outputs `<aggregation>_<lastSegment(field)>` (transform.go:519) and joins with a hard-coded ', ' (596-602). summarizeToKilas drops separateBy and all options. n8n uses the prefixes unique_count_, concatenated_ and appended_, plus the author's separator.

Evidence: Live side by side (flow_probe.py, sbs2_flow-probe.json). n8n: {"sum_score":12,"concatenated_name":"Ada|Bob|ada|Cy","unique_count_tag":3}. KilasFlow: {"sum_score":12,"concatenate_name":"Ada, Bob, ada, Cy","countUnique_tag":3}, with no import issue. The same probe showed If 2.2, Filter 2.2 (case-insensitive), Switch 3.2 with the extra fallback output, Remove Duplicates 1.1, Limit (lastItems) and Split Out matching n8n.

n8n behavior: Output names: count_, sum_, min_, max_, average_, unique_count_, concatenated_, appended_. The separator is configurable.

Impact: Downstream `$json.concatenated_x` / `appended_x` references resolve empty (2679, 2982 and other Summarize users).

Suggested fix: Use n8n's naming map with the full field path, honour separateBy/custom separator and outputFormat, and map or report the options.

Files: nodes/transform.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-jwhdsy (done)

---
### Basic LLM Chain prompt rows use n8n's role names verbatim, which KilasFlow refuses (and export writes KilasFlow's names back) [find:importer-fidelity] (medium/bug) · area: importer+exporter/chainLlm · confidence: high

n8n stores messageValues[].type as SystemMessagePromptTemplate (the default, omitted), HumanMessagePromptTemplate or AIMessagePromptTemplate. chainMessagesToKilas copies the type unchanged, but the chain accepts only system/human/ai (nodes/ai.go:529-531), and chainMessagesToN8N writes human/ai back, which n8n does not recognise. Separately, chainLlm <=1.3 stores the user prompt in `prompt`, which is dropped silently.

Evidence: 2878 'Clarifying Questions', 'DeepResearch Learnings' and 'Generate SERP Queries' fail activation with: message 1 has type "HumanMessagePromptTemplate", want system, human, or ai. No import issue is raised. 2271 'Assess if message needs a reply' (v1.3) imports with only its system message; `prompt` is gone.

n8n behavior: Role enum *MessagePromptTemplate; v1.0-1.3 use the `prompt` parameter.

Impact: Chains lose their user prompt or cannot activate (2/100 templates here).

Suggested fix: Map the role names in both directions (an omitted type means system), and map `prompt` to text/promptType=define for versions below 1.4.

Files: internal/interop/n8n/parameters.go, nodes/ai.go

Existing tickets: FEAT-347egc (done), FEAT-96p7m3 (done)

---
### Exported AI Agent and Basic LLM Chain omit hasOutputParser, so the wired output parser is detached in n8n [find:importer-fidelity] (medium/bug) · area: exporter/agent, chainLlm · confidence: high

agentToN8N and chainToN8N never write hasOutputParser, although the export includes the ai_outputParser edge. In n8n, agent 3.1 and chainLlm 1.9 compute their inputs from hasOutputParser (default false; confirmed in /rest/node-types), so the edge targets an input that does not exist.

Evidence: Round trip (roundtrip/run2.log): hasOutputParser is lost on 10 agents and 7 chains across the 100 exports, e.g. 3066 'Social Media Content Factory', 5338, 2320 'Apply Data Extraction Rules', 2878 'Generate SERP Queries'.

n8n behavior: The Output Parser input exists only when hasOutputParser is true.

Impact: Structured output silently stops working in any workflow exported back to n8n.

Suggested fix: Set hasOutputParser:true when an ai_outputParser edge targets the node, and needsFallback when a second model is wired.

Files: internal/interop/n8n/parameters.go

Existing tickets: FEAT-347egc (done)

---
### The exporter re-versions almost every mapped node to a pinned typeVersion, including downgrades, contrary to its documented intent [find:importer-fidelity] (medium/bug) · area: exporter/typeVersion · confidence: high

exportVersion (n8n.go:1284-1309) preserves the source version only when a mapping lists publishedVersions, and only Postgres and MySQL do. Every other imported node is written back at the pin, although the code comments say imported versions are preserved to avoid silent behaviour changes. 362 node instances change version on export, with no export diagnostic.

Evidence: All 100 exports, source -> exported: if 2.2->2 (50), agent 1.1-1.9->3.1 (52), lmChatOpenAi 1/1.1->1.2 (33), toolWorkflow 1.x/2/2.1->2.2 (39), outputParserStructured 1.2->1.3 (18), chainLlm 1.4/1.5->1.9 (17), set 3.3->3.4 (17), set 1/2/3.2->3.4 (15; this flips the include default, see the Set finding), merge 3.1/3.2->3 (downgrade), respondToWebhook 1.4->1.1 (downgrade, 5171), httpRequest 1/3/4.1->4.2, splitInBatches 1->3, dateTime 1->2. Exported 5170 and 1744 do run in n8n via the public API, but with the Set/DateTime differences.

n8n behavior: Behaviour and defaults differ per node version.

Impact: A KilasFlow -> n8n round trip is not a no-op even for fully supported workflows.

Suggested fix: Store the imported version and write it back when the translator can emit that shape; otherwise report the version change as a lossy export issue. Populate publishedVersions for every mapping.

Files: internal/interop/n8n/n8n.go

Existing tickets: FEAT-5kfctc (done), FEAT-k3fmj1 (done)

---
### Postgres node with no stored `operation` imports as Execute Query; n8n's default is Insert [find:importer-fidelity] (medium/bug) · area: importer/postgres · confidence: high

postgresToKilas defaults a missing operation to executeQuery (parameters.go:1471). n8n's Postgres default is insert at v1, 2, 2.3, 2.5 and 2.6 (checked via /rest/node-types). The MySQL translator already patches the same default, with a comment wrongly claiming Postgres defaults to executeQuery.

Evidence: 2803 'insert data on db' (v2.5, with table and columns mapping but no operation) imports as executeQuery with no query. Activation fails with 'an execute query needs a query', the configured insert mapping is discarded, and no import issue is raised.

n8n behavior: operation defaults to insert.

Impact: Most Postgres inserts authored without touching the dropdown (1/100 templates here).

Suggested fix: Default to insert and write operation explicitly on export.

Files: internal/interop/n8n/parameters.go

---
### Structured Output Parser: an empty (default) node and JSON Schema type arrays are refused [find:importer-fidelity] (medium/bug) · area: importer/outputParserStructured · confidence: high

A parser with parameters {} means n8n's defaults (fromJson plus the built-in example), but the importer maps it to exampleJson with no example, which fails activation. A manual schema using "type": ["string","null"] (valid JSON Schema, accepted by n8n) is refused at activation.

Evidence: 2950 (v1.2, {}): 'exampleJson is required when the schema type is example JSON'. 2324 (v1.2): 'jsonSchema.properties.case_study_link.type must be a string'. n8n defaults come from /rest/node-types.

n8n behavior: The default schemaType is fromJson with a default example; type arrays are accepted.

Impact: 2/100 templates are blocked by the parser; nullable fields are the normal way to let an LLM answer 'unknown'.

Suggested fix: Fill in n8n's default example when neither key is present, and accept type arrays, anyOf and nullable in the schema validator.

Files: internal/interop/n8n/parameters.go, nodes/ai.go

Existing tickets: FEAT-347egc (done)

---
### Switch v1/v2 (legacy `rules.rules` with value1/dataType/output) imports with no rules, and outputs 1..N are cut [find:importer-fidelity] (medium/bug) · area: importer/switch · confidence: high

switchToKilas reads only v3 `rules.values[].conditions`. A legacy Switch becomes a node with no rules and one output, so edges from outputs >=1 are held back and activation fails with 'switch rules must be a list'.

Evidence: 1534 'Check Status' (2 edges dropped) and 1934 'CheckCommand' (fallbackOutput 3, 3 edges dropped). The exports lack those branches too.

n8n behavior: Legacy rules route on value1 by dataType/operation to the output index in each rule.

Impact: 2/100 templates, and common in older customer workflows.

Suggested fix: Translate the legacy rules and the numeric fallbackOutput, or import the node as a blocking placeholder that keeps its outputs.

Files: internal/interop/n8n/parameters.go

---
### Native KilasFlow nodes exist, but their n8n equivalents are not mapped (Calculator tool, MCP Client tool, Ollama and other OpenAI-compatible chat models) [find:importer-fidelity] (medium/parity-gap) · area: importer/mappings · confidence: high

The catalogue registers kilasflow.calculatorTool, kilasflow.mcpClientTool and kilasflow.chatModel (OpenAI-compatible, with a baseUrl), but the mapping table has no entry for toolCalculator, mcpClientTool, lmChatOllama/lmOllama, or providers that expose OpenAI-compatible endpoints (Gemini, DeepSeek, Groq, Mistral, xAI). These import as unsupported placeholders, even though the project's own e2e setup uses Ollama.

Evidence: Corpus: toolCalculator 5 nodes in 3 templates, mcpClientTool 3/2, lmChatOllama 3/3, lmOllama 1/1, lmChatGoogleGemini 12/8, lmChatAnthropic 3/1. All are kilasflow.unsupported in reimport.json. FEAT-c2a081 added mcpClientTool without an import mapping.

n8n behavior: These are first-class sub-nodes.

Impact: Cheap coverage wins: the 'more chat models' group alone blocks 14 templates.

Suggested fix: Map toolCalculator -> calculatorTool and mcpClientTool (endpointUrl/sseEndpoint, include/exclude tools, timeout) -> mcpClientTool. Map lmChatOllama and the OpenAI-compatible providers to chatModel with provider base URLs and credential rebinding.

Files: internal/interop/n8n/n8n.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-c2a081 (done)

---
### Code nodes: none of 79 are auto-translated, although 37 are diagnosed as replaceable by one native node; legacy Function nodes get no hint [find:importer-fidelity] (medium/parity-gap) · area: importer/code · confidence: high

JS Code is the largest single blocker (35/100 templates: 79 Code nodes plus 6 Function/FunctionItem nodes). The trivial Code->Set rewrite (trivialcode.go, FEAT-c32499) matched none of the 79 Code nodes. The importer's own analysis labels 37 of them as doable by one native node (Set x12, Date & Time x8, Filter x6, Sort x5, Aggregate/Summarize x4, Limit x2) but leaves the rebuild to the user. function/functionItem nodes (1073) import as the generic placeholder rather than foreignCode.

Evidence: reimport.json: 79 Code nodes -> kilasflow.foreignCode, and function/functionItem (6 nodes) -> kilasflow.unsupported. The blocking-issue texts are grouped by suggested replacement above. JS is the sole blocker in 3 templates.

n8n behavior: Runs JS and Python.

Impact: 35% of templates.

Suggested fix: Extend the rewrite to the patterns the importer already classifies, emitting the suggested native node when the pattern is fully understood. Route function/functionItem through foreignCode. Make an explicit roadmap decision on a sandboxed JS subset.

Files: internal/interop/n8n/trivialcode.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-c32499 (done), FEAT-8qyfh1 (done)

---
### Per-node notes are discarded (77 notes in 12 templates), losing template documentation [find:importer-fidelity] (low/parity-gap) · area: importer/editor · confidence: high

n8n node `notes` (with notesInFlow to show them on the canvas) are how template authors document each step. KilasFlow has no node-note field, so import reports them as dropped and export returns nodes without them.

Evidence: reimport.json has 77 issues reading 'the node's note was parsed but has no KilasFlow equivalent' across 12 templates. The round trip lost 50 notes across the 17 tested templates (e.g. every Set node in 5170).

n8n behavior: Notes are shown in the node panel and optionally on the canvas.

Impact: Imported tutorials and templates lose their inline explanations.

Suggested fix: Add an optional notes field (plus a show-on-canvas flag) to the canonical node, render it, and round-trip it.

Files: internal/interop/n8n/n8n.go, internal/workflow/document.go

Existing tickets: FEAT-nbqye0 (done)

## Acceptance criteria

- [ ] Merge v2 `combinationMode` and v3 `options` (includeUnpaired, ...) are dropped silently, so merges fail or los
- [ ] Sub-workflow input mappings are dropped silently (Execute Workflow `workflowInputs`, Execute Workflow Trigger 
- [ ] Node names with leading or trailing whitespace lose every connection on import
- [ ] Export collapses Merge inputs 3..N onto input 0 (silent rewiring on round trip)
- [ ] Typed AI edges touching an unsupported placeholder are discarded on import and therefore lost on export
- [ ] HTTP Request options are dropped silently, including ones KilasFlow supports (response format, never error, ti
- [ ] Date & Time: v1 detection is keyed on the `action` field, the v1 output field and token dialect are wrong, and
- [ ] Summarize output field names and the concatenate separator differ from n8n
- [ ] Basic LLM Chain prompt rows use n8n's role names verbatim, which KilasFlow refuses (and export writes KilasFlo
- [ ] Exported AI Agent and Basic LLM Chain omit hasOutputParser, so the wired output parser is detached in n8n
- [ ] The exporter re-versions almost every mapped node to a pinned typeVersion, including downgrades, contrary to i
- [ ] Postgres node with no stored `operation` imports as Execute Query; n8n's default is Insert
- [ ] Structured Output Parser: an empty (default) node and JSON Schema type arrays are refused
- [ ] Switch v1/v2 (legacy `rules.rules` with value1/dataType/output) imports with no rules, and outputs 1..N are cu
- [ ] Native KilasFlow nodes exist, but their n8n equivalents are not mapped (Calculator tool, MCP Client tool, Olla
- [ ] Code nodes: none of 79 are auto-translated, although 37 are diagnosed as replaceable by one native node; legac
- [ ] Per-node notes are discarded (77 notes in 12 templates), losing template documentation
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## ImporterTail slice — 2026-09-20

Status: `testing`. Commits: `47544b7` (content; swept into a peer's commit),
`41f1d50`.

Landed:
- HTTP Request: `options` read (timeout, response.neverError/responseFormat/
  fullResponse/outputPropertyName, redirect.followRedirects/maxRedirects),
  key/value bodies carried as `bodyFields` with expressions intact rather than
  marshalled to text, tool-variant collections (`parametersQuery`/
  `parametersHeaders`) read, and pagination/batching/multipart/binary/unauthorized
  certs named (multipart, binary and allowUnauthorizedCerts blocking).
- Webhook: `httpMethod` defaults to GET, arrays become `httpMethods`, HEAD
  accepted, `responseData`/`responseCode`/`options` carried verbatim, `jwtAuth`
  kept instead of rewritten to none.
- Respond to Webhook: missing `respondWith` defaults to `firstIncomingItem`,
  `options.responseHeaders.entries` and `responseKey` carried.
- Postgres: a missing `operation` defaults to insert (n8n's default), not
  executeQuery.
- Switch: legacy `rules.rules` rows and the top-level `fallbackOutput` translated.
- Chain: role names mapped both ways (System/Human/AIMessagePromptTemplate ↔
  system/human/ai) and pre-1.4 `prompt` becomes the user prompt.
- Memory: `contextWindowLength` (interactions) → `maxMessages` (messages) ×2.
- Node names kept verbatim, so trailing-space names keep their edges.
- Export: Merge inputs past the second resolve from the catalogue (`inputIndexesFor`)
  instead of collapsing onto slot 0; `hasOutputParser`/`needsFallback` written
  from the graph; a version change the translator cannot honour is reported as
  lossy instead of happening silently.
- Placeholders: every typed AI kind (embedding, document, splitter, vector store,
  output parser, retriever, reranker, agent, chain) is declared, so RAG edges are
  no longer held back and lost on export.
- Native mappings added for toolCalculator, mcpClientTool, lmChatOllama/lmOllama
  and the OpenAI-compatible providers (Gemini, DeepSeek, Groq, Mistral, xAI).
- Structured Output Parser: an empty node gets n8n's built-in example.

Scoped proof: `go test ./internal/interop/n8n/ -count=1` green (20 new regression
tests); `go test ./nodes/ -count=1 -run 'Subworkflow|Assignment|Unsupported|Loop|Telegram'` green.

Remaining:
- Code auto-translation: the customer-specific keyword rewrite was **removed**
  (see BUG-wdypd2) and 79 Code nodes still import as blocking `foreignCode` with
  their source and a suggested replacement. Extending the rewrite to the patterns
  the importer classifies (Set/DateTime/Filter/Sort/Aggregate/Limit) is unstarted.
- Per-node notes need a canonical field on `workflow.Node` (EngineFlow's
  `internal/workflow/document.go`) plus rendering; the dropped diagnostic stays
  until then.
- `publishedVersions` per mapping is unpopulated: preserving a source version is
  only safe where the translator emits that version's shape, and the core
  translators emit the pinned shape. Version changes are reported instead.
- Structured Output Parser JSON Schema `type` arrays / `anyOf` are refused by the
  validator in `nodes/ai.go` (AINodes2's file; asked).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (4):
  - `a6f63041` — BUG-8t94wn BUG-gaavr5 BUG-wdypd2 BUG-qq4xva FEAT-j5s2n4: record testing state — importer tail slice
  - `41f1d50e` — BUG-gaavr5 BUG-wdypd2: AI tool/model mappings, Telegram Additional Fields, calculator/MCP/Ollama
  - `47544b77` — BUG-8dmp5y: throttle login, revalidate sessions, bound credential scope through redirects — auth/security
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  534 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  794 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  795 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 +++++++
 .pine/tickets/BUG-fv5fer.md                        |  635 +++++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 64250 insertions(+), 4725 deletions(-)
```
