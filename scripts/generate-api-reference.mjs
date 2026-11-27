/**
 * Renders the public API reference into the docs site from a real binary.
 *
 * The OpenAPI document comes from a KilasFlow server built and booted right
 * here via scripts/openapi-spec.mjs — never from a checked-in copy — so the
 * reference cannot describe an endpoint the server does not serve. Both the
 * web client and the host SDK already consume that module; this is the third
 * consumer and inherits the same property.
 *
 * Usage:
 *
 *   node scripts/generate-api-reference.mjs          # regenerate the pages
 *   node scripts/generate-api-reference.mjs --check  # fail if any page is stale
 *
 * The pages are committed and the check runs in CI (the `drift` job), the
 * same arrangement as the web API client and the SDK types. The docs build
 * itself stays Node-only: it never boots a binary, so a prose change never
 * needs a Go toolchain.
 *
 * Two things the OpenAPI document cannot express are curated in this file
 * rather than derived from it, because they are behaviour, not shape:
 *
 * - the embed verdict per operation is read from
 *   scripts/embed-permits/embed-permits.json, which `go run ./scripts/embed-permits`
 *   writes by asking internal/api/middleware's EmbedPermit (the gate EmbedAuth
 *   enforces) about every operation; only the reasons behind a verdict are
 *   curated here, and the run fails when a reason contradicts the table;
 * - the SSE behaviour prose (sequencing, terminal events, Last-Event-ID
 *   resume, the 20-second heartbeat) mirrors internal/api/handlers/executions.go
 *   and internal/events/events.go;
 * - the WorkflowValidationIssue payload mirrors
 *   internal/api/handlers/workflows.go.
 *
 * The presentation is also curated here rather than generated blindly:
 *
 * - every operation gets a field table (request and success response),
 *   a minimal request/response example skeleton and a curl line, all
 *   resolved from the document's own schemas;
 * - twenty operations — the ones an integrator calls first, named in the
 *   `samples` map — additionally get TypeScript (real KilasFlowClient
 *   methods from sdk/src/server.ts) and Go (standard library only) tabs.
 *
 * The same run publishes the document itself as docs/public/openapi.json,
 * so the site serves the exact spec the pages were rendered from.
 *
 * If any of those sources changes shape, update the curated block beside it.
 * If the OpenAPI document gains an operation this file does not map to a
 * contract group, the run fails naming the operation id — that is the check
 * that keeps a new endpoint from shipping undocumented.
 */

import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { dumpOpenAPISpec } from './openapi-spec.mjs';

const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const docsDir = join(repoDir, 'docs', 'src', 'content', 'docs', 'reference');
const generatedDir = join(docsDir, 'api');
const tmpSpecPath = join(repoDir, '.tmp', 'api-reference-openapi.json');

// Contract groups, in the order docs/src/content/docs/reference/api-contract.md
// presents them. Every operation in the live document must appear in exactly
// one group; an unmapped operation id fails the run.
const groups = [
	{ slug: 'workflows', title: 'Workflows', blurb: 'Create, read, update, run, activate, and version workflows.', operations: ['create-workflow', 'validate-workflow-document', 'list-workflows', 'get-workflow', 'list-workflow-webhooks', 'update-workflow', 'delete-workflow', 'run-workflow', 'duplicate-workflow', 'activate-workflow', 'deactivate-workflow', 'list-workflow-versions', 'get-workflow-version', 'publish-workflow-version', 'restore-workflow-version', 'list-workflow-publish-events', 'workflow-diagnostics'] },
	{ slug: 'executions', title: 'Executions', blurb: 'List, inspect, cancel, retry, and follow executions on the event stream.', operations: ['list-executions', 'get-execution', 'cancel-execution', 'retry-execution', 'eval-expression', 'stream-execution-events'] },
	{ slug: 'credentials', title: 'Credentials', blurb: 'Credential types and stored credentials. Reads never return values.', operations: ['list-credential-types', 'test-credential-payload', 'list-credentials', 'create-credential', 'get-credential', 'update-credential', 'delete-credential', 'test-credential', 'start-credential-oauth'] },
	{ slug: 'auth', title: 'Authentication and keys', blurb: 'Sessions, API keys, and stream tickets.', operations: ['login', 'logout', 'get-me', 'list-api-keys', 'create-api-key', 'revoke-api-key', 'create-stream-ticket'] },
	{ slug: 'schedules', title: 'Schedules', blurb: 'Cron-style triggers owned by a workflow.', intro: ['Which time zone a cron is read in depends on how the schedule came to exist. A cron you send to these operations is read in UTC unless the expression starts with a `TZ=Area/City ` prefix, for example `TZ=Asia/Jakarta 0 9 * * *` (`CRON_TZ=` also works), and the expression may carry a leading seconds field. The resource does not report a zone. A schedule that activating a workflow registers from its Schedule Trigger node is different: it is read in the workflow\u2019s `timezone` setting, falling back to the instance\u2019s `execution.default_timezone`, and to UTC only when neither is set. Imported n8n workflows keep their zone this way.'], operations: ['list-schedules', 'create-schedule', 'update-schedule', 'delete-schedule'] },
	{ slug: 'variables', title: 'Variables', blurb: 'The tenant\u2019s key/value pairs every workflow reads as `$vars.KEY`.', operations: ['list-variables', 'create-variable', 'update-variable', 'delete-variable'] },
	{ slug: 'host-events', title: 'Host events', blurb: 'The recommended host → workflow path: publish one event, every active workflow listening for it runs.', operations: ['publish-event'] },
	{ slug: 'nodes', title: 'Node types', blurb: 'The node catalogue, icons, load-options, load-schema, and the expression grammar.', operations: ['list-node-types', 'get-node-icon', 'load-node-property-options', 'load-node-property-schema', 'get-expression-grammar'] },
	{ slug: 'datastores', title: 'Datastores', blurb: 'Tenant-owned row stores: tables, columns, rows, and CSV import and export.', operations: ['list-datastores', 'create-datastore', 'get-datastore', 'rename-datastore', 'delete-datastore', 'add-datastore-column', 'rename-datastore-column', 'delete-datastore-column', 'insert-datastore-row', 'get-datastore-row', 'list-datastore-rows', 'update-datastore-rows', 'upsert-datastore-row', 'increment-datastore-rows', 'delete-datastore-rows', 'clear-datastore', 'import-datastore-rows', 'export-datastore-rows'] },
	{ slug: 'tenants', title: 'Tenants and accounts', blurb: 'Operator surface: tenants, their users, and keys minted for another tenant.', operations: ['list-tenants', 'create-tenant', 'get-tenant', 'update-tenant', 'delete-tenant', 'list-tenant-users', 'create-tenant-user', 'disable-tenant-user', 'enable-tenant-user', 'set-tenant-user-password', 'create-tenant-api-key'] },
	{ slug: 'interop', title: 'Interop', blurb: 'Import and export workflows across formats.', operations: ['import-workflow', 'convert-workflow-fragment', 'export-workflow'] },
	{ slug: 'embed', title: 'Embed', blurb: 'Mint an embed session: one workflow or one datastore, a scope list, one origin.', operations: ['create-embed-session'] },
	{ slug: 'system', title: 'System', blurb: 'Health and readiness.', operations: ['get-health', 'get-ready'] },
];

// The SSE event names, with the Go types huma registers for them. The run
// asserts every one is present in the live document as a named schema.
const sseEvents = [
	{ name: 'execution.started', schema: 'ExecutionStartedEvent', terminal: false },
	{ name: 'execution.completed', schema: 'ExecutionCompletedEvent', terminal: true },
	{ name: 'execution.failed', schema: 'ExecutionFailedEvent', terminal: true },
	{ name: 'execution.cancelled', schema: 'ExecutionCancelledEvent', terminal: true },
	{ name: 'node.started', schema: 'NodeStartedEvent', terminal: false },
	{ name: 'node.output', schema: 'NodeOutputEvent', terminal: false },
	{ name: 'node.completed', schema: 'NodeCompletedEvent', terminal: false },
	{ name: 'node.failed', schema: 'NodeFailedEvent', terminal: false },
	{ name: 'execution.waiting', schema: 'ExecutionWaitingEvent', terminal: false },
	{ name: 'workflow.saved', schema: 'WorkflowSavedEvent', terminal: false },
	{ name: 'webhook.response', schema: 'WebhookResponseEvent', terminal: false },
	{ name: 'code.console', schema: 'CodeConsoleEvent', terminal: false },
	{ name: 'ai.model.started', schema: 'AIModelStartedEvent', terminal: false },
	{ name: 'ai.model.delta', schema: 'AIModelDeltaEvent', terminal: false },
	{ name: 'ai.model.completed', schema: 'AIModelCompletedEvent', terminal: false },
	{ name: 'ai.tool.started', schema: 'AIToolStartedEvent', terminal: false },
	{ name: 'ai.tool.completed', schema: 'AIToolCompletedEvent', terminal: false },
	{ name: 'ai.tool.failed', schema: 'AIToolFailedEvent', terminal: false },
	{ name: 'ai.agent.completed', schema: 'AIAgentCompletedEvent', terminal: false },
	{ name: 'ai.agent.failed', schema: 'AIAgentFailedEvent', terminal: false },
	{ name: 'execution.event', schema: 'OtherEvent', terminal: false },
];

function oneLine(text) {
	return String(text ?? '').replace(/\s+/g, ' ').replace(/\|/g, '\\|').trim();
}

function schemaName(schema) {
	if (!schema || typeof schema !== 'object') return '';
	if (typeof schema.$ref === 'string') return schema.$ref.split('/').pop();
	if (schema.type === 'array' && schema.items) return `array of ${schemaName(schema.items)}`;
	if (Array.isArray(schema.type)) return schema.type.join(' or ');
	return schema.type || '';
}

// The samples block names the operations an integrator actually calls first,
// which get TypeScript and Go beside the curl every operation gets. Twenty
// is a size, not a rule: the list is curated here exactly like the contract
// groups above, because "the top 20" is a judgement about readers rather
// than a fact in the document. The TypeScript lines name real
// `KilasFlowClient` methods (sdk/src/server.ts) — the SDK has one per
// operation — and the Go lines use nothing beyond the standard library.
const samples = {
	'create-workflow': {
		sdk: ['const workflow = await client.createWorkflow(document);'],
		go: ['payload, _ := json.Marshal(document)', 'resp, err := http.Post(baseURL+"/api/v1/workflows", "application/json", bytes.NewReader(payload))']
	},
	'list-workflows': {
		sdk: ['const workflows = await client.listWorkflows();'],
		go: ['resp, err := http.Get(baseURL + "/api/v1/workflows?limit=100")']
	},
	'get-workflow': {
		sdk: ['const workflow = await client.getWorkflow(workflowId);'],
		go: ['resp, err := http.Get(baseURL + "/api/v1/workflows/" + url.PathEscape(workflowId))']
	},
	'update-workflow': {
		sdk: ['const workflow = await client.updateWorkflow(workflowId, document);'],
		go: ['payload, _ := json.Marshal(document)', 'req, _ := http.NewRequest(http.MethodPut, baseURL+"/api/v1/workflows/"+url.PathEscape(workflowId), bytes.NewReader(payload))', 'req.Header.Set("Content-Type", "application/json")', 'resp, err := http.DefaultClient.Do(req)']
	},
	'run-workflow': {
		sdk: ['const { id: executionId } = await client.runWorkflow(workflowId, input);'],
		go: ['payload, _ := json.Marshal(map[string]any{"input": input})', 'req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/workflows/"+url.PathEscape(workflowId)+"/run", bytes.NewReader(payload))', 'resp, err := http.DefaultClient.Do(req) // 202: follow the execution on the event stream']
	},
	'activate-workflow': {
		sdk: ['await client.activateWorkflow(workflowId);'],
		go: ['resp, err := http.Post(baseURL+"/api/v1/workflows/"+url.PathEscape(workflowId)+"/activate", "application/json", nil)']
	},
	'list-executions': {
		sdk: ['for await (const execution of client.iterateExecutions({ workflowId })) {', '  console.log(execution.id, execution.status);', '}'],
		go: ['resp, err := http.Get(baseURL + "/api/v1/executions?workflowId=" + url.QueryEscape(workflowId) + "&limit=100")']
	},
	'get-execution': {
		sdk: ['const execution = await client.getExecution(executionId);'],
		go: ['resp, err := http.Get(baseURL + "/api/v1/executions/" + url.PathEscape(executionId))']
	},
	'cancel-execution': {
		sdk: ['await client.cancelExecution(executionId);'],
		go: ['req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/executions/"+url.PathEscape(executionId)+"/cancel", nil)', 'resp, err := http.DefaultClient.Do(req)']
	},
	'login': {
		sdk: ['const principal = await client.login({ email, password }); // session arrives as an HttpOnly cookie'],
		go: ['payload, _ := json.Marshal(map[string]string{"email": email, "password": password})', 'resp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(payload))']
	},
	'list-credentials': {
		sdk: ['const credentials = await client.listCredentials(); // names only; values never leave the server'],
		go: ['resp, err := http.Get(baseURL + "/api/v1/credentials")']
	},
	'create-credential': {
		sdk: ['const credential = await client.createCredential({', '  name: "Acme production", type: "httpHeaderAuth",', '  fields: { name: "X-Api-Key", value: apiKey },', '});'],
		go: ['payload, _ := json.Marshal(map[string]any{"name": "Acme production", "type": "httpHeaderAuth", "fields": map[string]string{"name": "X-Api-Key", "value": apiKey}})', 'resp, err := http.Post(baseURL+"/api/v1/credentials", "application/json", bytes.NewReader(payload))']
	},
	'create-datastore': {
		sdk: ['const datastore = await client.createDatastore({', '  name: "orders",', '  columns: [{ name: "email", type: "string" }],', '});'],
		go: ['payload, _ := json.Marshal(map[string]any{"name": "orders", "columns": []map[string]string{{"name": "email", "type": "string"}}})', 'resp, err := http.Post(baseURL+"/api/v1/datastores", "application/json", bytes.NewReader(payload))']
	},
	'insert-datastore-row': {
		sdk: ['const row = await client.insertDatastoreRow(datastoreId, { email: "user@example.com" },', '  { idempotencyKey: crypto.randomUUID() });'],
		go: ['payload, _ := json.Marshal(map[string]string{"email": "user@example.com"})', 'req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/datastores/"+url.PathEscape(datastoreId)+"/rows", bytes.NewReader(payload))', 'req.Header.Set("Idempotency-Key", idempotencyKey)', 'resp, err := http.DefaultClient.Do(req)']
	},
	'list-datastore-rows': {
		sdk: ['for await (const row of client.iterateDatastoreRows(datastoreId)) {', '  console.log(row);', '}'],
		go: ['resp, err := http.Get(baseURL + "/api/v1/datastores/" + url.PathEscape(datastoreId) + "/rows?limit=100")']
	},
	'upsert-datastore-row': {
		sdk: ['const result = await client.upsertDatastoreRow(datastoreId,', '  datastoreFilter("and", [{ column: "email", operator: "eq", value: "user@example.com" }]),', '  { email: "user@example.com", plan: "pro" },', '  { idempotencyKey: crypto.randomUUID() });'],
		go: ['payload, _ := json.Marshal(map[string]any{"filter": map[string]any{"type": "and", "filters": []any{map[string]any{"column": "email", "operator": "eq", "value": "user@example.com"}}}, "values": map[string]string{"email": "user@example.com", "plan": "pro"}})', 'req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/datastores/"+url.PathEscape(datastoreId)+"/rows/upsert", bytes.NewReader(payload))', 'req.Header.Set("Idempotency-Key", idempotencyKey)', 'resp, err := http.DefaultClient.Do(req)']
	},
	'create-api-key': {
		sdk: ['const key = await client.createApiKey("nightly-sync"); // key.token is shown exactly once'],
		go: ['payload, _ := json.Marshal(map[string]string{"label": "nightly-sync"})', 'resp, err := http.Post(baseURL+"/api/v1/api-keys", "application/json", bytes.NewReader(payload))']
	},
	'create-tenant': {
		sdk: ['const tenant = await client.createTenant({ id: "acme", name: "Acme" }); // operator credential'],
		go: ['payload, _ := json.Marshal(map[string]string{"id": "acme", "name": "Acme"})', 'resp, err := http.Post(baseURL+"/api/v1/tenants", "application/json", bytes.NewReader(payload))']
	},
	'create-embed-session': {
		sdk: ['const session = await client.createEmbedSession({', '  workflowId,', '  scopes: ["workflow:read", "workflow:write"],', '  origin: "https://app.example.com",', '});'],
		go: ['payload, _ := json.Marshal(map[string]any{"workflowId": workflowId, "scopes": []string{"workflow:read", "workflow:write"}, "origin": "https://app.example.com"})', 'resp, err := http.Post(baseURL+"/api/v1/embed-sessions", "application/json", bytes.NewReader(payload))']
	},
	'create-stream-ticket': {
		sdk: ['const { ticket } = await client.createStreamTicket(executionId); // spent as ?ticket= by EventSource'],
		go: ['resp, err := http.Post(baseURL+"/api/v1/executions/"+url.PathEscape(executionId)+"/stream-ticket", "application/json", nil)']
	},
};

// resolve follows one $ref into the document's components, returning the
// schema object and its registered name. Anything else is returned as it is
// with an empty name.
function resolveRef(schema, schemas) {
	if (!schema || typeof schema !== 'object' || typeof schema.$ref !== 'string') return [schema, ''];
	const name = schema.$ref.split('/').pop();
	return [schemas[name], name];
}

// fieldRows flattens an object schema into one row per field, nesting dotted
// paths for objects and `[]` for arrays, to the depth named. The OpenAPI
// document carries the descriptions the Go types declare, which is where the
// request bodies get their prose from; response resources carry fewer, and an
// empty description is rendered as an em dash rather than invented.
function fieldRows(schema, schemas, options = {}) {
	const rows = [];
	const visiting = options.visiting ?? new Set();
	const depth = options.depth ?? 0;
	const maxDepth = options.maxDepth ?? 3;
	const prefix = options.prefix ?? '';

	for (const [key, property] of Object.entries(schema.properties ?? {})) {
		if (key === '$schema') continue;
		const required = (schema.required ?? []).includes(key);
		const [resolved, name] = resolveRef(property, schemas);
		rows.push({
			path: `${prefix}${key}`,
			required,
			type: schemaName(property),
			description: oneLine(property.description ?? resolved?.description),
		});
		if (depth >= maxDepth || name === '' || visiting.has(name)) continue;
		// A field typed `["array", "null"]` is still an array for nesting
		// purposes: the null arm is the absent one, not the interesting one.
		const propertyTypes = Array.isArray(property.type) ? property.type : [property.type];
		const nested = resolved?.type === 'object' && resolved?.properties ? resolved : null;
		const itemSchema = propertyTypes.includes('array') ? property.items : null;
		const [itemResolved, itemName] = itemSchema ? resolveRef(itemSchema, schemas) : [null, ''];
		const nestedArray = propertyTypes.includes('array') && itemResolved?.type === 'object' && itemResolved?.properties;
		if (nested) {
			visiting.add(name);
			rows.push(...fieldRows(nested, schemas, { visiting, depth: depth + 1, maxDepth, prefix: `${prefix}${key}.` }));
			visiting.delete(name);
		} else if (nestedArray) {
			visiting.add(itemName);
			rows.push(...fieldRows(itemResolved, schemas, { visiting, depth: depth + 1, maxDepth, prefix: `${prefix}${key}[].` }));
			visiting.delete(itemName);
		}
	}
	return rows;
}

function renderFieldTable(lines, label, schema, schemas) {
	const rows = fieldRows(schema, schemas);
	if (rows.length === 0) return false;
	lines.push(`${label}:`);
	lines.push('');
	lines.push('| Field | Required | Type | Description |');
	lines.push('| --- | --- | --- | --- |');
	for (const row of rows) {
		lines.push(`| \`${row.path}\` | ${row.required ? 'yes' : 'no'} | ${row.type ? `\`${row.type}\`` : ''} | ${row.description || '—'} |`);
	}
	lines.push('');
	return true;
}

// exampleValue renders one placeholder value for a property, from what the
// document itself carries first — a default, an example, an enum's first
// value — and from the property's shape and name second. The result is a
// skeleton, not a record: it shows which field goes where, and the prose
// above it says what the fields mean.
function exampleValue(name, schema, schemas, depth = 0) {
	if (!schema || typeof schema !== 'object') return null;
	// The document types many fields as `["array", "null"]` or similar unions
	// with null, because that is how Go's `[]T` marshals. For an example the
	// non-null arm is the one a reader can learn the shape from.
	const types = Array.isArray(schema.type) ? schema.type : [schema.type];
	if (schema.default !== undefined) return schema.default;
	const example = Array.isArray(schema.examples) ? schema.examples[0] : undefined;
	if (example !== undefined) return example;
	if (Array.isArray(schema.enum) && schema.enum.length > 0) return schema.enum[0];
	if (typeof schema.const !== 'undefined') return schema.const;
	if (types.includes('array')) {
		const item = exampleValue(name, schema.items, schemas, depth);
		return item === null || item === undefined ? [] : [item];
	}
	if (types.includes('object')) {
		if (depth >= 2 || !schema.properties) return {};
		const built = {};
		for (const [key, property] of Object.entries(schema.properties)) {
			if (key === '$schema') continue;
			built[key] = exampleValue(key, property, schemas, depth + 1);
		}
		return built;
	}
	if (types.includes('string')) {
		if (schema.format === 'date-time') return '2026-01-02T15:04:05Z';
		if (/url/i.test(name)) return 'https://flows.example';
		if (/email/i.test(name)) return 'user@example.com';
		return 'string';
	}
	if (types.includes('integer') || types.includes('number')) return 0;
	if (types.includes('boolean')) return false;
	if (schema.oneOf || schema.anyOf) {
		const first = (schema.oneOf ?? schema.anyOf)[0];
		return exampleValue(name, first, schemas, depth);
	}
	if (schema.$ref) return exampleValue(name, schemas[schema.$ref.split('/').pop()], schemas, depth);
	return null;
}

// minimalExample builds a request skeleton from the required fields (plus any
// field carrying a default), because a request that omits an optional field
// is the smaller and more honest example; a response skeleton from every
// top-level field, because that is what a reader is learning to read.
function minimalExample(schema, schemas) {
	const built = {};
	for (const [key, property] of Object.entries(schema.properties ?? {})) {
		if (key === '$schema') continue;
		const required = (schema.required ?? []).includes(key);
		if (!required && property.default === undefined) continue;
		built[key] = exampleValue(key, property, schemas);
	}
	return built;
}

function fullExample(schema, schemas) {
	const built = {};
	for (const [key, property] of Object.entries(schema.properties ?? {})) {
		if (key === '$schema') continue;
		built[key] = exampleValue(key, property, schemas);
	}
	return built;
}

// curlExample renders the shell line every operation gets. Path parameters
// keep their `{name}` braces so a reader can see what to substitute; the
// body is the same minimal skeleton the example section shows.
function curlExample(path, method, operation, requestExample) {
	const lines = [`curl -X ${method} "$KILASFLOW_HOST${path}"`];
	lines.push('  -H "Authorization: Bearer $KILASFLOW_API_KEY"');
	if (requestExample !== undefined) {
		lines.push('  -H "content-type: application/json"');
		const body = JSON.stringify(requestExample);
		lines.push(`  -d '${body}'`);
	}
	return lines.join(' \\\n');
}

// The embed permit table. Every allow and deny below comes from
// scripts/embed-permits/embed-permits.json, which `go run ./scripts/embed-permits`
// writes by asking the real gate (internal/api/middleware.EmbedPermit — the
// decision EmbedAuth enforces) about every operation, once per embed scope. Nothing
// in this file restates a rule: when the gate changes and the table is not
// regenerated, `make generate-embed-permits-check` fails, and when the table
// and these pages disagree, `make generate-api-reference-check` does.
//
// What the gate cannot see is curated in `embedNotes`: the checks the handlers
// add after the gate lets a request through, and the reason behind a refusal.
// Each note declares whether the operations it covers are allowed or denied,
// and the run fails when the table says otherwise, so a note cannot go on
// describing a verdict the server no longer gives.
const permitTablePath = join(repoDir, 'scripts', 'embed-permits', 'embed-permits.json');

async function loadPermitTable(byId) {
	let table;
	try {
		table = JSON.parse(await readFile(permitTablePath, 'utf8'));
	} catch (error) {
		throw new Error(`api-reference: cannot read ${permitTablePath}: ${error.message}. Run \`make generate-embed-permits\`.`);
	}
	const stale = [];
	for (const [id, found] of byId) {
		const entry = table.operations?.[id];
		if (!entry) stale.push(`${id} is missing from the table`);
		else if (entry.method !== found.method || entry.path !== found.path) {
			stale.push(`${id} is ${entry.method} ${entry.path} in the table but ${found.method} ${found.path} in the document`);
		}
	}
	for (const id of Object.keys(table.operations ?? {})) {
		if (!byId.has(id)) stale.push(`${id} is in the table but not in the document`);
	}
	if (stale.length > 0) {
		throw new Error(`api-reference: the embed permit table does not match the OpenAPI document (${stale.join('; ')}). Run \`make generate-embed-permits\`.`);
	}
	for (const note of embedNotes) {
		for (const id of note.operations) {
			const verdict = embedVerdictOf(table, id);
			if ((verdict.allowing.length > 0) !== note.allow) {
				throw new Error(`api-reference: the note for ${id} says the verdict is ${note.allow ? 'Allow' : 'Deny'} but the embed permit table says ${verdict.allowing.length > 0 ? 'Allow' : 'Deny'}. Update embedNotes in scripts/generate-api-reference.mjs.`);
			}
		}
	}
	return table;
}

// embedVerdictOf reads one operation's verdicts out of the table.
function embedVerdictOf(table, operationId) {
	const entry = table.operations[operationId];
	if (!entry) throw new Error(`api-reference: operation ${operationId} is not in the embed permit table. Run \`make generate-embed-permits\`.`);
	const scopes = table.subjects.flatMap((subject) => subject.scopes);
	const allowing = scopes.filter((scope) => entry.verdicts[scope]?.allow);
	return { entry, scopes, allowing };
}

const embedNotes = [
	{
		allow: false,
		operations: ['list-variables', 'create-variable', 'update-variable', 'delete-variable'],
		text: 'Variables are tenant-wide: every workflow of the tenant reads them, so a session confined to one workflow may neither list nor change them.'
	},
	{
		allow: true,
		operations: ['list-node-types', 'get-node-icon', 'load-node-property-schema'],
		text: 'The catalogue is narrowed to the session’s own tenant.'
	},
	{
		allow: true,
		operations: ['load-node-property-options'],
		text: 'It can reach a customer’s service, so the handler bounds it to the session’s workflow, not only its tenant.'
	},
	{
		allow: true,
		operations: ['list-credentials'],
		text: 'Names only, to render a credential picker; values are never returned. The handler narrows the list to the credentials the session’s confinement allows.'
	},
	{
		allow: true,
		operations: ['list-credential-types'],
		text: 'Schemas, never values: the editor reads it on every load to render a node’s credential field.'
	},
	{
		allow: false,
		operations: ['get-credential', 'create-credential', 'update-credential', 'delete-credential', 'test-credential', 'start-credential-oauth', 'test-credential-payload'],
		text: 'Creating, changing and testing credentials stays with the host’s own key. A confined session attaches only credentials its confinement already references or its host declared at mint — see [Embedding and multi-tenancy](/guides/embedding/).'
	},
	{
		allow: true,
		operations: ['convert-workflow-fragment'],
		text: 'It translates pasted n8n JSON and saves nothing; the paste reaches the workflow only through a save the session’s scopes govern.'
	},
	{
		allow: true,
		operations: ['validate-workflow-document'],
		text: 'A dry run that compiles the supplied document and saves nothing.'
	},
	{
		allow: false,
		operations: ['import-workflow', 'duplicate-workflow', 'create-workflow'],
		text: 'Creating a workflow is outside a session’s single-workflow authority.'
	},
	{
		allow: false,
		operations: ['delete-workflow'],
		text: 'A session cannot delete the workflow it edits.'
	},
	{
		allow: true,
		operations: ['activate-workflow', 'deactivate-workflow'],
		text: 'Activation registers deployment-wide triggers, so no other scope grants it: a host opts in by minting `workflow:activate`. The request reaches the same handler the dashboard uses, compiler and catalogue checks included.'
	},
	{
		allow: true,
		operations: ['publish-workflow-version'],
		text: 'Publishing pins the revision production traffic runs, so `workflow:write` alone is refused and a host opts in by minting `workflow:publish`. The revision is re-checked against the session’s confinement first.'
	},
	{
		allow: true,
		operations: ['run-workflow', 'retry-execution'],
		text: 'The document that would run is checked against the session’s confinement first; see [Embed sessions](/guides/embed-sessions/#confinement).'
	},
	{
		allow: true,
		operations: ['list-executions'],
		text: 'The query must name the session’s own workflow in `workflowId`.'
	},
	{
		allow: true,
		operations: ['get-execution', 'cancel-execution', 'eval-expression', 'stream-execution-events'],
		text: 'Which workflow an execution belongs to is known only after loading it, so the handler checks it. An execution of another workflow answers `404`, not `403`; a sub-workflow run started from the session’s own workflow is readable.'
	},
	{
		allow: false,
		operations: ['list-datastores', 'create-datastore'],
		text: 'Listing names every table in the tenant, and creating one is schema work. A datastore session is confined to the one table it was minted for.'
	},
	{
		allow: false,
		operations: ['rename-datastore', 'delete-datastore', 'add-datastore-column', 'rename-datastore-column', 'delete-datastore-column', 'clear-datastore'],
		text: 'Renaming, dropping, clearing and editing columns reshape the table itself, not its contents.'
	},
	{
		allow: true,
		operations: ['get-datastore', 'list-datastore-rows', 'get-datastore-row', 'export-datastore-rows', 'insert-datastore-row', 'update-datastore-rows', 'upsert-datastore-row', 'increment-datastore-rows', 'delete-datastore-rows', 'import-datastore-rows'],
		text: 'On the session’s own datastore only: the handler answers another datastore’s id with `404`, not `403`, so a session cannot probe ids.'
	}
];

// The sentence under every operation. The verdict is the table's; the note, if
// one exists, is the reason or the handler-level check the table cannot hold.
function embedVerdict(table, operationId) {
	const { entry, scopes, allowing } = embedVerdictOf(table, operationId);
	const note = embedNotes.find((candidate) => candidate.operations.includes(operationId));
	const noteText = note ? ` ${note.text}` : '';
	if (allowing.length === 0) {
		const refusals = [...new Set(scopes.map((scope) => `\`${entry.verdicts[scope].status}\` “${entry.verdicts[scope].detail}”`))];
		return `Deny — ${refusals.join(' or ')}${noteText}`;
	}
	const list = (names) => names.map((name) => `\`${name}\``).join(', ');
	let verdict;
	if (entry.requires) {
		verdict = `Allow with \`${entry.requires}\``;
		const kind = table.subjects.find((subject) => subject.scopes.includes(entry.requires));
		const others = allowing.filter((scope) => scope !== entry.requires);
		if (others.length === kind.scopes.length - 1) verdict += ` (every other ${kind.kind} scope carries it)`;
		else if (others.length > 0) verdict += ` (${list(others)} carry${others.length === 1 ? 'es' : ''} it too)`;
	} else {
		verdict = `Allow with any of ${list(allowing)}`;
	}
	if (entry.ownWorkflowOnly) verdict += ', on the session’s own workflow only — the gate refuses any other workflow id';
	return `${verdict}.${noteText}`;
}

// The scope × operation grid, rendered from the same table. Operations no
// scope opens are listed beneath it with the server's own sentence.
function renderEmbedPermits(version, table) {
	const scopes = table.subjects.flatMap((subject) => subject.scopes);
	const lines = [];
	lines.push('---');
	lines.push('title: Embed permit table');
	lines.push('description: "Which operations an embed session may call, per scope, and the exact refusal for the rest. Exported from the Go rules that enforce it."');
	lines.push('sidebar:');
	lines.push('  order: 14');
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from scripts/embed-permits/embed-permits.json by scripts/generate-api-reference.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(banner(version));
	lines.push('');
	lines.push('An [embed session](/guides/embed-sessions/) carries one or more scopes and is checked on every request, before the handler runs. This table is not written from the rules: `go run ./scripts/embed-permits` asks the code that enforces them (`EmbedPermit` in `internal/api/middleware`) about every operation in the OpenAPI document, once per scope, and a drift check fails CI when the answer changes.');
	lines.push('');
	lines.push('Each column is a session holding that one scope, on a path that names the session’s own workflow or datastore. Scopes combine by union, so a session holding two scopes may call whatever either column allows. `workflow:write`, `workflow:run`, `workflow:publish` and `workflow:activate` each carry `workflow:read`, and `datastore:write` carries `datastore:read`; no other scope implies another. A workflow session cannot hold a datastore scope or the reverse.');
	lines.push('');
	lines.push('What this table cannot show is what a handler adds after the gate lets a request through: an execution of another workflow answers `404`, another datastore’s id answers `404`, and a save, publish or run is refused with a `403` naming the node when the document reaches outside the session’s confinement. Those are on each operation’s page and in [Embed sessions](/guides/embed-sessions/).');
	lines.push('');
	lines.push('## Allowed operations');
	lines.push('');
	lines.push(`| Operation | ${scopes.map((scope) => `\`${scope}\``).join(' | ')} |`);
	lines.push(`| --- | ${scopes.map(() => ':---:').join(' | ')} |`);
	const denied = [];
	for (const group of groups) {
		for (const id of group.operations) {
			const { entry, allowing } = embedVerdictOf(table, id);
			if (allowing.length === 0) {
				denied.push({ id, entry, group });
				continue;
			}
			const cells = scopes.map((scope) => (entry.verdicts[scope].allow ? 'Allow' : '—'));
			lines.push(`| [\`${entry.method} ${entry.path}\`](/reference/api/${group.slug}/) | ${cells.join(' | ')} |`);
		}
	}
	lines.push('');
	lines.push('## Refused operations');
	lines.push('');
	lines.push('Every other operation is refused to every embed scope, with the sentence below. The status is `403` unless noted.');
	lines.push('');
	lines.push('| Operation | Refusal |');
	lines.push('| --- | --- |');
	for (const { id, entry, group } of denied) {
		const refusals = [...new Set(scopes.map((scope) => `${entry.verdicts[scope].status} “${entry.verdicts[scope].detail}”`))];
		lines.push(`| [\`${entry.method} ${entry.path}\`](/reference/api/${group.slug}/) | ${refusals.join('; ')} |`);
	}
	lines.push('');
	return lines.join('\n');
}

function banner(version) {
	return `> **Generated reference — KilasFlow \`${version}\`.** Produced from the OpenAPI document a real binary serves at \`/api/openapi.json\`, not hand-written. [Which reference to trust](/reference/api/).`;
}

function renderOperation(path, method, operation, schemas, permits) {
	const lines = [];
	const summary = oneLine(operation.summary || operation.operationId);
	lines.push(`## ${summary} (\`${operation.operationId}\`)`);
	lines.push('');
	lines.push(`\`${method} ${path}\``);
	lines.push('');
	if (operation.description) lines.push(`${oneLine(operation.description)}`);
	lines.push('');
	if (Array.isArray(operation.parameters) && operation.parameters.length > 0) {
		lines.push('Parameters:');
		lines.push('');
		lines.push('| Name | In | Required | Type | Description |');
		lines.push('| --- | --- | --- | --- | --- |');
		for (const parameter of operation.parameters) {
			lines.push(`| \`${parameter.name}\` | ${parameter.in} | ${parameter.required ? 'yes' : 'no'} | ${oneLine(schemaName(parameter.schema))} | ${oneLine(parameter.description)} |`);
		}
		lines.push('');
	}
	let requestExample;
	const requestContent = operation.requestBody?.content ?? {};
	const requestTypes = Object.keys(requestContent);
	if (requestTypes.length > 0) {
		const first = requestContent[requestTypes[0]];
		const [resolved, name] = resolveRef(first?.schema, schemas);
		lines.push(`Request body: \`${requestTypes.join('`, `')}\`${name ? ` — \`${name}\`` : ''}${operation.requestBody?.required ? ' (required)' : ''}`);
		lines.push('');
		if (resolved?.type === 'object' && resolved.properties) {
			renderFieldTable(lines, 'Request fields', resolved, schemas);
			requestExample = minimalExample(resolved, schemas);
		}
	}
	const responses = Object.entries(operation.responses ?? {});
	const success = responses.find(([status]) => status.startsWith('2') && status !== '204');
	lines.push('Responses:');
	lines.push('');
	lines.push('| Status | Description | Body |');
	lines.push('| --- | --- | --- |');
	for (const [status, response] of responses) {
		const bodies = Object.keys(response.content ?? {}).map((type) => `\`${type}\``).join(', ');
		const ref = schemaName(response.content?.['application/json']?.schema);
		lines.push(`| \`${status}\` | ${oneLine(response.description)} | ${bodies}${ref ? ` — \`${ref}\`` : ''} |`);
	}
	lines.push('');
	let responseExample;
	if (success) {
		const [resolved, name] = resolveRef(success[1].content?.['application/json']?.schema, schemas);
		if (resolved?.type === 'object' && resolved.properties) {
			renderFieldTable(lines, `Fields of the \`${name || 'response'}\` body`, resolved, schemas);
			responseExample = fullExample(resolved, schemas);
		} else if (resolved?.type === 'array') {
			const [itemResolved] = resolveRef(resolved.items, schemas);
			if (itemResolved?.type === 'object' && itemResolved.properties) {
				renderFieldTable(lines, `Fields of each item in the \`${name || 'response'}\` array`, itemResolved, schemas);
				responseExample = [fullExample(itemResolved, schemas)];
			}
		}
	}
	const exampleLines = [];
	if (requestExample !== undefined) {
		exampleLines.push(`Request (minimal — required fields only):`);
		exampleLines.push('');
		exampleLines.push('```json');
		exampleLines.push(JSON.stringify(requestExample, null, 2));
		exampleLines.push('```');
	}
	if (responseExample !== undefined) {
		if (exampleLines.length > 0) exampleLines.push('');
		exampleLines.push(`Response (skeleton — every field, placeholder values):`);
		exampleLines.push('');
		exampleLines.push('```json');
		exampleLines.push(JSON.stringify(responseExample, null, 2));
		exampleLines.push('```');
	}
	if (exampleLines.length > 0) {
		lines.push(...exampleLines);
		lines.push('');
	}
	const curl = curlExample(path, method, operation, requestExample);
	const sample = samples[operation.operationId];
	if (sample) {
		lines.push(':::code');
		lines.push('');
		lines.push('```sh title="curl"');
		lines.push(curl);
		lines.push('```');
		lines.push('');
		lines.push('```ts title="TypeScript"');
		lines.push('// import { KilasFlowClient } from "@kilasflow/sdk/server";');
		lines.push(...sample.sdk);
		lines.push('```');
		lines.push('');
		lines.push('```go title="Go"');
		lines.push(...sample.go);
		lines.push('```');
		lines.push('');
		lines.push(':::');
	} else {
		lines.push('```sh');
		lines.push(curl);
		lines.push('```');
	}
	lines.push('');
	lines.push(`Embed: ${embedVerdict(permits, operation.operationId)}`);
	lines.push('');
	return lines.join('\n');
}

function renderGroupPage(group, version, operations, schemas, permits) {
	const lines = [];
	lines.push('---');
	lines.push(`title: ${group.title}`);
	// JSON.stringify rather than a bare interpolation: a blurb is prose and may
	// contain a colon, and `description: Tenant-owned row stores: tables` is a
	// YAML parse error that only shows up when the docs site builds.
	lines.push(`description: ${JSON.stringify(`${group.blurb} Generated from the live OpenAPI document.`)}`);
	lines.push('sidebar:');
	lines.push(`  order: ${groups.indexOf(group) + 1}`);
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(banner(version));
	lines.push('');
	lines.push(group.blurb);
	lines.push('');
	for (const paragraph of group.intro ?? []) {
		lines.push(paragraph);
		lines.push('');
	}
	for (const operation of operations) {
		lines.push(renderOperation(operation.path, operation.method, operation.operation, schemas, permits));
	}
	return lines.join('\n');
}

// authSentence reads the document instead of asserting a fixed posture.
//
// This sentence used to say "no operation requires authentication" in every
// generation, which was true only because the reference is generated from a
// binary started with auth off — and it stayed true on the page after
// auth.enabled became a supported deployment, so a reader concluded the API was
// open by design. The document now carries a root-level `security` requirement
// exactly when the instance enforces one (see openAPIConfig), so the page can
// describe the image it was generated from and name the difference.
function authSentence(document) {
	const enforced = Array.isArray(document?.security) && document.security.length > 0;
	if (enforced) {
		return ('This reference was generated from an instance with authentication enabled: every operation below needs either a Bearer API key (`Authorization: Bearer <key>`) or a session cookie, except `GET /api/v1/health`, `GET /api/v1/ready`, `POST /api/v1/auth/login` and `POST /api/v1/auth/logout`, which are public. The webhook and resume prefixes are outside this document and carry their own credentials. See [Security posture](/operate/security/).');
	}
	return ('This reference was generated from an instance with authentication disabled — `auth.enabled` defaults to `false` — so no operation below requires a credential **on that instance**. With `auth.enabled` set, every operation needs either a Bearer API key (`Authorization: Bearer <key>`) or a session cookie, except health, readiness, login and logout; the document the server serves then declares both schemes. See [Security posture](/operate/security/) for how to decide.');
}

function renderOverview(version, title, openapi, total, document) {
	const lines = [];
	lines.push('---');
	lines.push('title: HTTP API');
	lines.push('description: The full operation reference, generated from the OpenAPI document a real server produces.');
	lines.push('sidebar:');
	lines.push('  order: 1');
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(`> **Generated reference — KilasFlow \`${version}\`.** Produced from the OpenAPI document a real binary serves at \`/api/openapi.json\` (\`${title}\`, OpenAPI \`${openapi}\`), not hand-written. To regenerate, run \`make generate-api-reference\`.`);
	lines.push('');
	lines.push('## Which reference to trust');
	lines.push('');
	lines.push('A running server\u2019s `/docs` page is authoritative for the instance you are talking to. It is rendered from the OpenAPI document the binary generates from the same Go types that serve the requests, and it loads nothing from the internet, so it works air-gapped. This site is authoritative for the current release.');
	lines.push('');
	lines.push('When the two disagree, compare versions: every page here states the server version it was generated from, and the binary reports its own in the document\u2019s `info.version` and in its `-version` flag. The raw document is at `/api/openapi.json` — also `.yaml`, and `/api/openapi-3.0.json` and `.yaml` for tools that cannot yet read 3.1.');
	lines.push('');
	lines.push('## Operations');
	lines.push('');
	lines.push(`${total} operations, all under \`/api/v1\`, in ${groups.length} groups. Paths, HTTP methods, and operation ids are stable — see [API contract and stability](/reference/api-contract/).`);
	lines.push('');
	lines.push('| Group | Operations | Contents |');
	lines.push('| --- | --- | --- |');
	for (const group of groups) {
		lines.push(`| [${group.title}](/reference/api/${group.slug}/) | ${group.operations.length} | ${group.blurb} |`);
	}
	lines.push('');
	lines.push('Two behaviours are worth knowing before reading any operation page, because they are easy to misread from a signature alone. Running a workflow answers `202` and does not return results — it enqueues an execution, and you follow it on the event stream at `GET /api/v1/executions/{id}/events`. ' + authSentence(document));
	lines.push('');
	lines.push('## Beyond the operation pages');
	lines.push('');
	lines.push('- [Errors](/reference/api/errors/) — every non-success response is an RFC 9457 problem document, and workflow compile failures carry a structured `WorkflowValidationIssue`.');
	lines.push('- [Events](/reference/api/events/) — the server-sent event types and the `Last-Event-ID` resume behaviour, which an OpenAPI document describes the endpoint of but not the vocabulary carried over it.');
	lines.push('- [Embed permit table](/reference/api/embed-permits/) — which operations an embed session may call, per scope, with the exact refusal for the rest. It is exported from the Go rules that enforce it.');
	lines.push('- [Pagination](/reference/api/pagination/) — the two cursor conventions the listing operations use, which endpoints use which, and the SDK helpers that page for you.');
	lines.push('- [Webhooks](/reference/api/webhooks/) — the inbound `/webhook/{route}` surface, whose behaviour is defined by the trigger node rather than by a route definition.');
	lines.push('- [API contract and stability](/reference/api-contract/) — what `/api/v1` promises, what it explicitly does not, and how to tell whether an upgrade will break you.');
	lines.push('');
	lines.push(`The document itself is downloadable: [openapi.json](/openapi.json) (OpenAPI 3.1, KilasFlow \`${version}\` — the same version this page's banner names, because both come from the one run that generated these pages). Point a client generator, a diff tool or an editor at it instead of a running server when that is what you have.`);
	return lines.join('\n') + '\n';
}

function renderErrors(version, schemas) {
	const model = schemas.ErrorModel ?? {};
	const detail = schemas.ErrorDetail ?? {};
	const lines = [];
	lines.push('---');
	lines.push('title: Errors');
	lines.push('description: How every error in this API is expressed — RFC 9457 problems and structured workflow validation issues.');
	lines.push('sidebar:');
	lines.push('  order: 10');
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(banner(version));
	lines.push('');
	lines.push('Every non-success response is an RFC 9457 problem document (`application/problem+json`). The shape is stable; the human-readable strings inside it are not — never match on `detail` text. Operations declare their failures as the `default` response, so the table below applies to the whole API; an operation that declares one status of its own (currently `get-ready`, whose `503` carries the fleet spread when a migration is outstanding, as its page describes) serves the same problem fields plus the ones that page names.');
	lines.push('');
	lines.push('Problem fields, as served:');
	lines.push('');
	lines.push('| Field | Type | Description |');
	lines.push('| --- | --- | --- |');
	for (const [name, field] of Object.entries(model.properties ?? {})) {
		lines.push(`| \`${name}\` | ${oneLine(schemaName(field))} | ${oneLine(field.description)} |`);
	}
	lines.push('');
	lines.push('Each entry of `errors` carries:');
	lines.push('');
	lines.push('| Field | Type | Description |');
	lines.push('| --- | --- | --- |');
	for (const [name, field] of Object.entries(detail.properties ?? {})) {
		lines.push(`| \`${name}\` | ${oneLine(schemaName(field))} | ${oneLine(field.description)} |`);
	}
	lines.push('');
	lines.push('## Workflow validation issues');
	lines.push('');
	lines.push('Workflow compile failure is structured: a `422` whose error details carry a `WorkflowValidationIssue` value with `code`, `nodeId`, and `connectionId`, so a client can map failures back to graph elements without parsing a message. Draft validation failure is likewise a `422` with a `body`-located detail. The `code` vocabulary grows additively; unknown codes must be rendered generically, keyed off `nodeId` when present. See `WorkflowValidationIssue` in `internal/api/handlers/workflows.go` for the source of this contract.');
	lines.push('');
	lines.push('## Idempotency conflicts');
	lines.push('');
	lines.push('The operations that accept an `Idempotency-Key` request header — `run-workflow`, `publish-event`, `insert-datastore-row` and `upsert-datastore-row` — answer a reuse with a `409` whose error detail carries a typed `value.code`:');
	lines.push('');
	lines.push('| `value.code` | Meaning | What to do |');
	lines.push('| --- | --- | --- |');
	lines.push('| `idempotency_key_reused` | The key was first used for a different request or resource. | Send a new key for the new request. |');
	lines.push('| `idempotency_key_in_flight` | The first request with this key is still running. | Retry after the seconds named in the `Retry-After` response header; the key frees when that request finishes or its two-minute lease expires. |');
	lines.push('');
	lines.push('A retry that matches the first request is answered from the recorded outcome with the `Idempotent-Replayed: true` response header and repeats no side effect; the header is absent on a first response. Above a 1 MiB recorded outcome the replay carries the durable identity instead of the full body: an inserted row replays its `Location` from the row id, and an upsert replays its `inserted` and `matched` counts with an empty `rows` list. Keys are per tenant and expire after `idempotency.retention`.');
	return lines.join('\n') + '\n';
}

function renderEvents(version, schemas) {
	const missing = sseEvents.filter((event) => !(event.schema in schemas));
	if (missing.length > 0) {
		throw new Error(`api-reference: event schemas missing from the OpenAPI document: ${missing.map((event) => event.schema).join(', ')}`);
	}
	const shape = schemas.ExecutionStartedEvent ?? {};
	const lines = [];
	lines.push('---');
	lines.push('title: Events');
	lines.push('description: The server-sent event types an execution emits, and how to resume the stream.');
	lines.push('sidebar:');
	lines.push('  order: 11');
	lines.push('---');
	lines.push('');
	lines.push('<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->');
	lines.push('');
	lines.push(banner(version));
	lines.push('');
	lines.push('`GET /api/v1/executions/{id}/events` is a server-sent event stream. The OpenAPI document registers the endpoint; the vocabulary carried over it is below. The names are stable — see [API contract and stability](/reference/api-contract/).');
	lines.push('');
	lines.push('| Event | Closes the stream | Meaning |');
	lines.push('| --- | --- | --- |');
	const meanings = {
		'execution.started': 'The run left the queue and started.',
		'execution.completed': 'The run finished successfully.',
		'execution.failed': 'The run finished with an error.',
		'execution.cancelled': 'The run was cancelled.',
		'node.started': 'A node started.',
		'node.output': 'A node emitted an intermediate output.',
		'node.completed': 'A node finished successfully.',
		'node.failed': 'A node finished with an error.',
		'execution.waiting': 'The run suspended at a Wait and is parked until it resumes.',
		'workflow.saved': 'The workflow document changed under a running execution.',
		'webhook.response': 'A Respond to Webhook node answered the caller.',
		'code.console': 'A Code node\'s code printed. `data.lines` carries each line\'s `level` (log, info, warn, error or debug), `text` and `at`; `data.truncated` is true when output past the node\'s console limit was dropped. The same lines are kept on the node run as `console`.',
		'ai.model.started': 'An AI node sent one request to its model. `data` names the model and the tool-loop iteration.',
		'ai.model.delta': 'Streamed model output. `data.delta` is the next chunk of text, coalesced to about four updates per second.',
		'ai.model.completed': 'One model request finished. `data.usage` carries token counts when the provider reports them.',
		'ai.tool.started': 'An agent called a tool. `data.tool` names it and `data.detail` carries the redacted arguments.',
		'ai.tool.completed': 'A tool call returned. `data.detail` carries the redacted result.',
		'ai.tool.failed': 'A tool call failed. `data.error` says why; the agent sees the error and may retry.',
		'ai.agent.completed': 'An agent produced its final answer.',
		'ai.agent.failed': 'An agent run ended with an error.',
		'execution.event': 'Any event type added after this client was built. The payload\'s `type` carries its real name; ignore what you do not recognise.',
	};
	for (const event of sseEvents) {
		lines.push(`| \`${event.name}\` | ${event.terminal ? 'yes' : 'no'} | ${meanings[event.name]} |`);
	}
	lines.push('');
	lines.push('Every event shares one shape, as served:');
	lines.push('');
	lines.push('| Field | Type | Description |');
	lines.push('| --- | --- | --- |');
	for (const [name, field] of Object.entries(shape.properties ?? {})) {
		lines.push(`| \`${name}\` | ${oneLine(schemaName(field))} | ${oneLine(field.description)} |`);
	}
	lines.push('');
	lines.push('Delivery promises: each event carries a monotonic numeric `id`; a reconnecting client resends it as `Last-Event-ID` (or `?from=` where a header cannot be set) and resumes instead of restarting — retained events replay from that point. A comment heartbeat arrives every twenty seconds to keep idle connections open. The server closes the stream after a terminal event (`execution.completed`, `execution.failed`, `execution.cancelled`), so a client must stop reconnecting then. Event `data` is redacted on publish — a subscriber can never see credential material even if a node returned it. Delivery is best effort: durable execution and node-run records are the source of truth, and a run succeeds whether or not anyone is watching. New event types may be added in a minor release; consumers must ignore names they do not recognise.');
	return lines.join('\n') + '\n';
}

function collectOperations(document) {
	const byId = new Map();
	for (const [path, item] of Object.entries(document.paths ?? {})) {
		for (const [method, operation] of Object.entries(item ?? {})) {
			if (!operation || typeof operation !== 'object' || !operation.operationId) continue;
			if (byId.has(operation.operationId)) {
				throw new Error(`api-reference: duplicate operationId ${operation.operationId}`);
			}
			byId.set(operation.operationId, { path, method: method.toUpperCase(), operation });
		}
	}
	return byId;
}

async function main() {
	const check = process.argv.includes('--check');
	let serverDocument;
	try {
		serverDocument = await dumpOpenAPISpec(tmpSpecPath);
	} catch (error) {
		throw new Error(`api-reference: cannot produce the OpenAPI document from a real binary (docs contributors without Go see this when the drift check runs): ${error.message}`);
	}
	try {
		const version = serverDocument?.info?.version ?? 'unknown';
		const byId = collectOperations(serverDocument);
		const schemas = serverDocument.components?.schemas ?? {};
		const known = new Set(groups.flatMap((group) => group.operations));
		const unmapped = [...byId.keys()].filter((id) => !known.has(id));
		if (unmapped.length > 0) {
			throw new Error(`api-reference: operations without a contract group: ${unmapped.sort().join(', ')}. Map them in scripts/generate-api-reference.mjs and update the contract page.`);
		}
		const permits = await loadPermitTable(byId);
		const outputs = new Map();
		for (const group of groups) {
			const operations = group.operations.map((id) => {
				const found = byId.get(id);
				if (!found) throw new Error(`api-reference: contract group ${group.slug} lists unknown operation ${id}`);
				return found;
			});
			outputs.set(join(generatedDir, `${group.slug}.md`), renderGroupPage(group, version, operations, schemas, permits));
		}
		outputs.set(join(generatedDir, 'embed-permits.md'), renderEmbedPermits(version, permits));
		outputs.set(join(generatedDir, 'errors.md'), renderErrors(version, schemas));
		outputs.set(join(generatedDir, 'events.md'), renderEvents(version, schemas));
		outputs.set(join(docsDir, 'api.md'), renderOverview(version, serverDocument.info?.title ?? 'KilasFlow API', serverDocument.openapi ?? '3.1', byId.size, serverDocument));
		// The document the pages were rendered from, published beside them so a
		// reader without a running server has the same artifact to point a tool
		// at. Same bytes, same version, one run — the check compares it like
		// any other page.
		outputs.set(join(repoDir, 'docs', 'public', 'openapi.json'), `${JSON.stringify(serverDocument, null, 2)}\n`);
		const names = [...outputs.keys()].sort();
		if (!check) {
			await mkdir(generatedDir, { recursive: true });
			for (const name of names) {
				await writeFile(name, outputs.get(name));
			}
			console.log(`api-reference: wrote ${names.length} files (KilasFlow ${version}, ${byId.size} operations)`);
			return;
		}
		let failed = false;
		for (const name of names) {
			let got;
			try {
				got = await readFile(name, 'utf8');
			} catch {
				console.error(`api-reference: ${name} is missing. Run \`node scripts/generate-api-reference.mjs\` and commit the result.`);
				failed = true;
				continue;
			}
			if (got !== outputs.get(name)) {
				console.error(`api-reference: ${name} is stale. Run \`node scripts/generate-api-reference.mjs\` and commit the result.`);
				failed = true;
			}
		}
		if (failed) throw new Error('generated API reference is stale');
		console.log(`api-reference: ${names.length} files fresh (KilasFlow ${version}, ${byId.size} operations)`);
	} finally {
		await rm(tmpSpecPath, { force: true });
	}
}

try {
	await main();
} catch (error) {
	console.error(error.message);
	process.exit(1);
}
