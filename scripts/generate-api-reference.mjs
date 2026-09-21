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
 * - the embed verdict per operation mirrors permits() in
 *   internal/api/middleware/embed.go (default-deny, scoped to one workflow);
 * - the SSE behaviour prose (sequencing, terminal events, Last-Event-ID
 *   resume, the 20-second heartbeat) mirrors internal/api/handlers/executions.go
 *   and internal/events/events.go;
 * - the WorkflowValidationIssue payload mirrors
 *   internal/api/handlers/workflows.go.
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
	{ slug: 'workflows', title: 'Workflows', blurb: 'Create, read, update, run, activate, and version workflows.', operations: ['create-workflow', 'list-workflows', 'get-workflow', 'list-workflow-webhooks', 'update-workflow', 'delete-workflow', 'run-workflow', 'activate-workflow', 'deactivate-workflow', 'list-workflow-versions', 'get-workflow-version', 'publish-workflow-version', 'restore-workflow-version', 'list-workflow-publish-events', 'workflow-diagnostics'] },
	{ slug: 'executions', title: 'Executions', blurb: 'List, inspect, cancel, and follow executions on the event stream.', operations: ['list-executions', 'get-execution', 'cancel-execution', 'stream-execution-events'] },
	{ slug: 'credentials', title: 'Credentials', blurb: 'Credential types and stored credentials. Reads never return values.', operations: ['list-credential-types', 'test-credential-payload', 'list-credentials', 'create-credential', 'get-credential', 'update-credential', 'delete-credential', 'test-credential'] },
	{ slug: 'auth', title: 'Authentication and keys', blurb: 'Sessions, API keys, and stream tickets.', operations: ['login', 'logout', 'get-me', 'list-api-keys', 'create-api-key', 'revoke-api-key', 'create-stream-ticket'] },
	{ slug: 'schedules', title: 'Schedules', blurb: 'Cron-style triggers owned by a workflow.', operations: ['list-schedules', 'create-schedule', 'update-schedule', 'delete-schedule'] },
	{ slug: 'nodes', title: 'Node types', blurb: 'The node catalogue, icons, load-options, load-schema, and the expression grammar.', operations: ['list-node-types', 'get-node-icon', 'load-node-property-options', 'load-node-property-schema', 'get-expression-grammar'] },
	{ slug: 'datastores', title: 'Datastores', blurb: 'Tenant-owned row stores: tables, columns, rows, and CSV import and export.', operations: ['list-datastores', 'create-datastore', 'get-datastore', 'rename-datastore', 'delete-datastore', 'add-datastore-column', 'rename-datastore-column', 'delete-datastore-column', 'insert-datastore-row', 'get-datastore-row', 'list-datastore-rows', 'update-datastore-rows', 'upsert-datastore-row', 'delete-datastore-rows', 'clear-datastore', 'import-datastore-rows', 'export-datastore-rows'] },
	{ slug: 'tenants', title: 'Tenants and accounts', blurb: 'Operator surface: tenants, their users, and keys minted for another tenant.', operations: ['list-tenants', 'create-tenant', 'get-tenant', 'list-tenant-users', 'create-tenant-user', 'disable-tenant-user', 'enable-tenant-user', 'set-tenant-user-password', 'create-tenant-api-key'] },
	{ slug: 'interop', title: 'Interop', blurb: 'Import and export workflows across formats.', operations: ['import-workflow', 'export-workflow'] },
	{ slug: 'embed', title: 'Embed', blurb: 'Mint a session that confines an embedded editor to one workflow.', operations: ['create-embed-session'] },
	{ slug: 'system', title: 'System', blurb: 'Health and readiness.', operations: ['get-health', 'get-ready'] },
];

// The nine SSE event names, with the Go types huma registers for them. The run
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
	{ name: 'workflow.saved', schema: 'WorkflowSavedEvent', terminal: false },
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

// Embed verdict per operation, mirroring permits() in
// internal/api/middleware/embed.go: default-deny, scoped to the session's
// single workflow, with the catalogue readable because it carries no tenant
// data. Paths here are the full served paths (the /api/v1 prefix included).
function embedVerdict(method, path) {
	const rest = path.replace(/^\/api\/v1/, '');
	if (rest === '/node-types' || rest.startsWith('/node-types/')) {
		return 'Allow with `workflow:read` — the catalogue is narrowed to the session\u2019s own tenant. `load-options` is additionally bounded to the session\u2019s workflow by the handler.';
	}
	if (rest === '/credentials' && method === 'GET') {
		return 'Allow with `workflow:read` — names are needed to render a credential picker; values are never returned.';
	}
	if (rest === '/workflows/import') {
		return 'Deny — importing creates a new workflow, outside any session\u2019s single-workflow authority.';
	}
	if (rest.startsWith('/workflows/')) {
		const action = rest.slice('/workflows/'.length).split('/')[1] ?? '';
		if (action === 'run') return 'Allow with `workflow:run` — on the session\u2019s workflow only.';
		if (action === 'activate' || action === 'deactivate') return 'Deny — activation publishes a deployment-wide endpoint; an owner action, not an embed one.';
		if (method === 'GET') return 'Allow with `workflow:read` — on the session\u2019s workflow only.';
		if (method === 'DELETE') return 'Deny — an embed session cannot delete a workflow.';
		return 'Allow with `workflow:write` — on the session\u2019s workflow only.';
	}
	if (rest === '/executions' && method === 'GET') {
		return 'Allow with `workflow:read` — the query must name the session\u2019s workflow (`workflowId`).';
	}
	if (rest.startsWith('/executions/')) {
		return 'Allow with `workflow:read` — ownership is checked in the handler, which alone can know which workflow an execution belongs to.';
	}
	return 'Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.';
}

function banner(version) {
	return `> **Generated reference — KilasFlow \`${version}\`.** Produced from the OpenAPI document a real binary serves at \`/api/openapi.json\`, not hand-written. [Which reference to trust](/reference/api/).`;
}

function renderOperation(path, method, operation) {
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
	const requestContent = operation.requestBody?.content ?? {};
	const requestTypes = Object.keys(requestContent);
	if (requestTypes.length > 0) {
		const first = requestContent[requestTypes[0]];
		const ref = schemaName(first?.schema);
		lines.push(`Request body: \`${requestTypes.join('`, `')}\`${ref ? ` — \`${ref}\`` : ''}${operation.requestBody?.required ? ' (required)' : ''}`);
		lines.push('');
	}
	lines.push('Responses:');
	lines.push('');
	lines.push('| Status | Description | Body |');
	lines.push('| --- | --- | --- |');
	for (const [status, response] of Object.entries(operation.responses ?? {})) {
		const bodies = Object.keys(response.content ?? {}).map((type) => `\`${type}\``).join(', ');
		const ref = schemaName(response.content?.['application/json']?.schema);
		lines.push(`| \`${status}\` | ${oneLine(response.description)} | ${bodies}${ref ? ` — \`${ref}\`` : ''} |`);
	}
	lines.push('');
	lines.push(`Embed: ${embedVerdict(method, path)}`);
	lines.push('');
	return lines.join('\n');
}

function renderGroupPage(group, version, operations) {
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
	for (const operation of operations) {
		lines.push(renderOperation(operation.path, operation.method, operation.operation));
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
	lines.push('- [Events](/reference/api/events/) — the nine server-sent event types and the `Last-Event-ID` resume behaviour, which an OpenAPI document describes the endpoint of but not the vocabulary carried over it.');
	lines.push('- [Webhooks](/reference/api/webhooks/) — the inbound `/webhook/{route}` surface, whose behaviour is defined by the trigger node rather than by a route definition.');
	lines.push('- [API contract and stability](/reference/api-contract/) — what `/api/v1` promises, what it explicitly does not, and how to tell whether an upgrade will break you.');
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
	lines.push('The operations that accept an `Idempotency-Key` request header — `run-workflow`, `insert-datastore-row` and `upsert-datastore-row` — answer a reuse with a `409` whose error detail carries a typed `value.code`:');
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
	lines.push('description: The nine server-sent event types an execution emits, and how to resume the stream.');
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
		'workflow.saved': 'The workflow document changed under a running execution.',
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
		const known = new Set(groups.flatMap((group) => group.operations));
		const unmapped = [...byId.keys()].filter((id) => !known.has(id));
		if (unmapped.length > 0) {
			throw new Error(`api-reference: operations without a contract group: ${unmapped.sort().join(', ')}. Map them in scripts/generate-api-reference.mjs and update the contract page.`);
		}
		const outputs = new Map();
		for (const group of groups) {
			const operations = group.operations.map((id) => {
				const found = byId.get(id);
				if (!found) throw new Error(`api-reference: contract group ${group.slug} lists unknown operation ${id}`);
				return found;
			});
			outputs.set(join(generatedDir, `${group.slug}.md`), renderGroupPage(group, version, operations));
		}
		outputs.set(join(generatedDir, 'errors.md'), renderErrors(version, serverDocument.components?.schemas ?? {}));
		outputs.set(join(generatedDir, 'events.md'), renderEvents(version, serverDocument.components?.schemas ?? {}));
		outputs.set(join(docsDir, 'api.md'), renderOverview(version, serverDocument.info?.title ?? 'KilasFlow API', serverDocument.openapi ?? '3.1', byId.size, serverDocument));
		const names = [...outputs.keys()].sort();
		if (!check) {
			await mkdir(generatedDir, { recursive: true });
			for (const name of names) {
				await writeFile(name, outputs.get(name));
			}
			console.log(`api-reference: wrote ${names.length} pages (KilasFlow ${version}, ${byId.size} operations)`);
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
		console.log(`api-reference: ${names.length} pages fresh (KilasFlow ${version}, ${byId.size} operations)`);
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
