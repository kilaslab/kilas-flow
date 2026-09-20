---
id: BUG-gaavr5
title: 'Importer node mapping long tail: AI edges, Date/Chain roles, exporter, Postgres op, Code, notes'
status: testing
priority: medium
labels:
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:47:48Z"
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
