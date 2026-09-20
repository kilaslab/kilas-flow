// Datastore E2E fixtures (FEAT-cpdp8y). Shared helpers for the sqlite and
// postgres datastore suites. New files only: the harness in
// e2e/helpers/* and e2e/fixtures.ts is untouched.
//
// Vocabulary (design-refs/n8n-v2 shots 18-32, INDEX.md):
// - Create dialog is name-only (shot 19); columns are always a second step.
// - Fresh table already carries id, createdAt, updatedAt (shot 20).
// - Add Column is name + type select (shot 21); the four types verbatim from
//   the dropdown are string, number, boolean, datetime (shot 22). The wire
//   carries `date`; the server normalises the `datetime` alias.
// - Grid renders id, user columns in order, timestamps (shot 23).
// - Node is "Data table" (shot 25) with Row Actions (insert/get/update/
//   upsert/delete/ifExists/ifNotExists) and Table Actions (create/list/
//   rename/clear/deleteTable); table update is surfaced as Rename (shot 26).
// - NDV: Resource -> Operation -> dataTableId locator (From list / By Name /
//   By ID, shot 28) -> Mapping Column Mode (shot 29) -> filter Must Match
//   Any/All + Conditions + Return All + Limit Per Input Row (shot 30).
// - Node filter paths are filters.conditions[i].keyName/condition/keyValue
//   (shot 31), mapped by the importer to the service names
//   columnName/condition/value.
import { spawn, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createWriteStream } from 'node:fs';
import { mkdtemp } from 'node:fs/promises';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gate } from './gates';

export interface DatastoreColumn {
	name: string;
	type: string;
}

export interface DatastoreResource {
	id: string;
	name: string;
	columns: DatastoreColumn[];
}

export async function apiJson(
	baseURL: string,
	method: string,
	path: string,
	body?: unknown,
	wantStatus = 200
): Promise<any> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (response.status !== wantStatus) {
		throw new Error(
			`${method} ${path}: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`
		);
	}
	if (response.status === 204) return null;
	return response.json();
}

export async function apiStatus(
	baseURL: string,
	method: string,
	path: string,
	body?: unknown
): Promise<{ status: number; text: string; json: any }> {
	const response = await fetch(`${baseURL}/api/v1${path}`, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	const text = await response.text();
	let json: any = null;
	try {
		json = text ? JSON.parse(text) : null;
	} catch {
		json = null;
	}
	return { status: response.status, text, json };
}

export function uniqueName(prefix: string): string {
	return `${prefix} ${randomBytes(4).toString('hex')}`;
}

// --- Datastore management surface (mirrors internal/api/handlers/datastores.go) ---

export async function createDatastore(baseURL: string, name: string): Promise<DatastoreResource> {
	// Name-only creation: columns arrive empty and are added afterwards,
	// matching the create dialog that takes a name only.
	return (await apiJson(baseURL, 'POST', '/datastores', { name }, 201)) as DatastoreResource;
}

export async function getDatastore(baseURL: string, id: string): Promise<DatastoreResource> {
	return (await apiJson(baseURL, 'GET', `/datastores/${encodeURIComponent(id)}`)) as DatastoreResource;
}

export async function listDatastores(baseURL: string): Promise<DatastoreResource[]> {
	const payload = await apiJson(baseURL, 'GET', '/datastores');
	return (payload.items ?? []) as DatastoreResource[];
}

export async function renameDatastore(
	baseURL: string,
	id: string,
	name: string
): Promise<DatastoreResource> {
	return (await apiJson(baseURL, 'PUT', `/datastores/${encodeURIComponent(id)}`, { name })) as DatastoreResource;
}

export async function addColumn(
	baseURL: string,
	id: string,
	name: string,
	type: string
): Promise<DatastoreColumn> {
	return (await apiJson(baseURL, 'POST', `/datastores/${encodeURIComponent(id)}/columns`, {
		name,
		type
	})) as DatastoreColumn;
}

export async function insertRow(
	baseURL: string,
	id: string,
	values: Record<string, unknown>
): Promise<any> {
	return apiJson(baseURL, 'POST', `/datastores/${encodeURIComponent(id)}/rows`, { values }, 201);
}

export async function listRows(baseURL: string, id: string, limit = 100): Promise<any> {
	return apiJson(baseURL, 'GET', `/datastores/${encodeURIComponent(id)}/rows?limit=${limit}`);
}

// --- Datastore node documents (nodes/datastore.go) ---
//
// Locator shape is property.WriteLocator: {__rl:true, mode, value}.
// Mapper shapes use n8n's own mode names: autoMapInputData / defineBelow.
// Filter rows are filters.conditions[{keyName, condition, keyValue}].

export function datastoreLocator(mode: string, value: string): Record<string, unknown> {
	return { __rl: true, mode, value };
}

export function manualColumns(values: Record<string, unknown>): Record<string, unknown> {
	return { mappingMode: 'defineBelow', value: values, matchingColumns: [], schema: [] };
}

export function autoColumns(): Record<string, unknown> {
	return { mappingMode: 'autoMapInputData' };
}

export function conditionRow(
	keyName: string,
	condition: string,
	keyValue: unknown
): Record<string, unknown> {
	return { keyName, condition, keyValue };
}

interface SpecNode {
	id: string;
	name: string;
	type: string;
	typeVersion: number;
	position: { x: number; y: number };
	parameters?: Record<string, unknown>;
}

interface SpecConnection {
	id: string;
	kind: string;
	source: { nodeId: string; port: string };
	target: { nodeId: string; port: string };
}

function specNode(
	id: string,
	name: string,
	type: string,
	parameters?: Record<string, unknown>
): SpecNode {
	return { id, name, type, typeVersion: 1, position: { x: 0, y: 0 }, ...(parameters ? { parameters } : {}) };
}

function specConn(id: string, source: string, target: string): SpecConnection {
	return { id, kind: 'main', source: { nodeId: source, port: 'main' }, target: { nodeId: target, port: 'main' } };
}

const manualTrigger = () => specNode('manual', 'Manual Trigger', 'kilasflow.manual');

// Manual -> insert (manual columns) -> get (filtered read). The insert writes
// fixed values; the get reads them back with a filter so the execution record
// proves the filtered read, not just the write.
export function datastoreWriteReadDocument(
	name: string,
	datastoreId: string,
	values: Record<string, unknown>,
	filterColumn: string,
	filterValue: unknown
): Record<string, unknown> {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			manualTrigger(),
			specNode('insert', 'Insert', 'kilasflow.datastore', {
				resource: 'row',
				operation: 'insert',
				dataTableId: datastoreLocator('id', datastoreId),
				columns: manualColumns(values)
			}),
			specNode('read', 'Read', 'kilasflow.datastore', {
				resource: 'row',
				operation: 'get',
				dataTableId: datastoreLocator('id', datastoreId),
				match: 'any',
				filters: { conditions: [conditionRow(filterColumn, 'eq', filterValue)] },
				returnAll: true
			})
		],
		connections: [specConn('c1', 'manual', 'insert'), specConn('c2', 'insert', 'read')],
		settings: {}
	};
}

// Manual -> insert with auto-mapping fed by a Set node. Proves the
// Map-Automatically path (shot 29): the incoming item's fields that match the
// live schema land, unmapped fields are dropped.
export function datastoreAutoMapDocument(
	name: string,
	datastoreId: string,
	item: Record<string, unknown>
): Record<string, unknown> {
	return {
		schemaVersion: 1,
		name,
		nodes: [
			manualTrigger(),
			specNode('in', 'Set', 'kilasflow.set', { assignments: item }),
			specNode('insert', 'Insert', 'kilasflow.datastore', {
				resource: 'row',
				operation: 'insert',
				dataTableId: datastoreLocator('id', datastoreId),
				columns: autoColumns()
			})
		],
		connections: [specConn('c1', 'manual', 'in'), specConn('c2', 'in', 'insert')],
		settings: {}
	};
}

export async function createWorkflowDocument(
	baseURL: string,
	document: Record<string, unknown>
): Promise<{ id: string }> {
	const created = await apiJson(baseURL, 'POST', '/workflows', document, 201);
	return { id: created.id as string };
}

export async function startWorkflowRun(
	baseURL: string,
	workflowId: string
): Promise<{ status: number; body: any }> {
	const response = await fetch(`${baseURL}/api/v1/workflows/${workflowId}/run`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: '{}'
	});
	return { status: response.status, body: await response.json() };
}

export function nodeRun(record: any, nodeId: string): any {
	const run = (record.nodeRuns as any[]).find((entry) => entry.nodeId === nodeId);
	if (!run) throw new Error(`node ${nodeId} has no recorded run`);
	return run;
}

// --- CSV transfer (internal/api/handlers/datastores_csv.go) ---

// A sheet in the shape an n8n Data Table export hands over: a header of user
// columns plus plain records. System columns (id/createdAt/updatedAt) are
// never in an import file; the server refuses them outright.
export function n8nStyleCsv(): string {
	return 'email,score\nada@example.com,93\nlin@example.com,87\n';
}

export function n8nStyleCsvRows(): Array<Record<string, unknown>> {
	return [
		{ email: 'ada@example.com', score: 93 },
		{ email: 'lin@example.com', score: 87 }
	];
}

export async function importCsv(
	baseURL: string,
	datastoreId: string,
	csv: string,
	wantStatus = 200
): Promise<any> {
	const response = await fetch(
		`${baseURL}/api/v1/datastores/${encodeURIComponent(datastoreId)}/rows/import`,
		{ method: 'POST', headers: { 'content-type': 'text/csv' }, body: csv }
	);
	if (response.status !== wantStatus) {
		throw new Error(
			`POST /datastores/${datastoreId}/rows/import: status ${response.status}, want ${wantStatus} (body: ${await response.text()})`
		);
	}
	return response.json();
}

export async function exportCsv(
	baseURL: string,
	datastoreId: string,
	includeSystem = false
): Promise<string> {
	const query = includeSystem ? '?includeSystemColumns=true' : '';
	const response = await fetch(
		`${baseURL}/api/v1/datastores/${encodeURIComponent(datastoreId)}/rows/export${query}`
	);
	if (!response.ok) {
		throw new Error(`GET export: status ${response.status} (body: ${await response.text()})`);
	}
	return response.text();
}

// --- n8n Data Table import (internal/interop/n8n, FEAT-nch9dg) ---

// Minimal n8n export containing one n8n-nodes-base.dataTable node in By-ID
// mode. The id names a row in n8n's catalogue and has no local counterpart
// by construction, so the import carries the reference and reports a
// blocking diagnostic naming it.
export function n8nDataTableExport(): Record<string, unknown> {
	return {
		name: 'N8N Data Table Import',
		nodes: [
			{
				id: 'a',
				name: 'Manual',
				type: 'n8n-nodes-base.manualTrigger',
				typeVersion: 1,
				position: [0, 0],
				parameters: {}
			},
			{
				id: 'b',
				name: 'Table',
				type: 'n8n-nodes-base.dataTable',
				typeVersion: 1,
				position: [220, 0],
				parameters: {
					resource: 'row',
					operation: 'insert',
					dataTableId: { mode: 'id', value: 'dt_metrics_01', cachedResultName: 'Metrics', __rl: true },
					columns: {
						mappingMode: 'defineBelow',
						value: { email: 'ada@example.com' },
						matchingColumns: [],
						schema: []
					}
				}
			}
		],
		connections: {
			Manual: { main: [[{ node: 'Table', type: 'main', index: 0 }]] }
		}
	};
}

export interface ImportedWorkflow {
	workflow: { id: string; name: string };
	unsupported: Array<{ severity: string; field?: string; reason: string }>;
}

export async function importN8nWorkflow(
	baseURL: string,
	n8nWorkflow: Record<string, unknown>,
	name?: string
): Promise<ImportedWorkflow> {
	return (await apiJson(
		baseURL,
		'POST',
		'/workflows/import',
		{ format: 'n8n', workflow: n8nWorkflow, ...(name ? { name } : {}) },
		201
	)) as ImportedWorkflow;
}

export async function fetchWorkflowDocument(baseURL: string, workflowId: string): Promise<any> {
	const stored = await apiJson(baseURL, 'GET', `/workflows/${workflowId}`);
	return stored.latestVersion.document;
}

export async function saveWorkflowDocument(
	baseURL: string,
	workflowId: string,
	document: any
): Promise<void> {
	await apiJson(baseURL, 'PUT', `/workflows/${workflowId}`, {
		schemaVersion: document.schemaVersion,
		name: document.name,
		nodes: document.nodes,
		connections: document.connections,
		settings: document.settings ?? {}
	});
}

// --- Postgres driver gating ---
//
// SQLite always runs (the harness default). Postgres joins when an explicit
// DSN names a live server, the same gate the Go engine tests use
// (KILASFLOW_TEST_POSTGRES_DSN). A second variable is accepted so the E2E
// suite can point at a throwaway database without disturbing Go runs.

export function pgDsn(): string | null {
	return process.env.KILASFLOW_TEST_POSTGRES_DSN ?? process.env.KILASFLOW_E2E_POSTGRES_DSN ?? null;
}

export function pgSkipReason(): string {
	return gate(
		'postgres',
		'set KILASFLOW_TEST_POSTGRES_DSN (or KILASFLOW_E2E_POSTGRES_DSN) to a live pgvector ' +
			'PostgreSQL DSN to run it (e.g. postgres://kilas:hunter2@127.0.0.1:55434/kilasflow?sslmode=disable)'
	);
}

export interface PgServer {
	baseURL: string;
	port: number;
	logPath: string;
	close: () => Promise<void>;
}

async function pgFreePort(): Promise<number> {
	const listener = net.createServer();
	listener.listen(0, '127.0.0.1');
	await once(listener, 'listening');
	const address = listener.address();
	if (!address || typeof address === 'string') throw new Error('Unable to reserve a local port');
	listener.close();
	await once(listener, 'close');
	return address.port;
}

async function pgWaitForReady(child: ChildProcess, readyURL: string, logs: string[]): Promise<void> {
	for (let attempt = 0; attempt < 80; attempt += 1) {
		if (child.exitCode !== null) {
			throw new Error(`KilasFlow (postgres) exited before readiness:\n${logs.join('')}`);
		}
		try {
			const response = await fetch(readyURL);
			if (response.ok) return;
		} catch {
			// The binary is still starting.
		}
		await new Promise((resolveDelay) => setTimeout(resolveDelay, 250));
	}
	throw new Error(`KilasFlow (postgres) did not become ready:\n${logs.join('')}`);
}

async function pgStop(child: ChildProcess): Promise<void> {
	if (!child || child.exitCode !== null) return;
	child.kill('SIGTERM');
	let timer: ReturnType<typeof setTimeout> | undefined;
	await Promise.race([
		once(child, 'exit'),
		new Promise((resolveDelay) => {
			timer = setTimeout(resolveDelay, 5_000);
		})
	]);
	if (timer) clearTimeout(timer);
	if (child.exitCode === null) child.kill('SIGKILL');
}

// Boots the same binary the harness builds (global-setup runs `make
// build-all` first) against the named Postgres server. Mirrors
// e2e/helpers/server.ts: fresh port, random embed + encryption keys, guarded
// outbound (allow_private_networks is never set). Auth stays off, so the
// instance is its own default tenant in the shared Postgres database; tests
// isolate by unique datastore/workflow names and ID-scoped assertions, never
// by assuming an empty catalogue.
export async function startPostgresServer(
	dsn: string,
	options: { stubEndpoint?: string; allowedOrigin?: string } = {}
): Promise<PgServer> {
	const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
	const binary = join(repoDir, 'bin', 'kilasflow');
	const dataDir = await mkdtemp(join(tmpdir(), 'kilasflow-e2e-pg-'));
	const port = await pgFreePort();
	const baseURL = `http://127.0.0.1:${port}`;
	const logPath = join(dataDir, `kilasflow-pg-${port}.log`);

	const logStream = createWriteStream(logPath, { flags: 'a' });
	const chunks: string[] = [];
	const child: ChildProcess = spawn(binary, ['-config', ''], {
		cwd: repoDir,
		env: {
			...process.env,
			KILASFLOW_SERVER_HOST: '127.0.0.1',
			KILASFLOW_SERVER_PORT: String(port),
			KILASFLOW_DATABASE_DRIVER: 'postgres',
			KILASFLOW_DATABASE_DSN: dsn,
			KILASFLOW_OUTBOUND_ALLOWED_HOSTS: '127.0.0.1',
			...(options.stubEndpoint
				? { KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS: options.stubEndpoint }
				: {}),
			KILASFLOW_EMBED_SIGNING_KEY: randomBytes(32).toString('base64'),
			KILASFLOW_ENCRYPTION_KEY: randomBytes(32).toString('base64'),
			...(options.allowedOrigin ? { KILASFLOW_EMBED_ALLOWED_ORIGINS: options.allowedOrigin } : {})
		},
		stdio: ['ignore', 'pipe', 'pipe']
	});
	child.stdout?.on('data', (chunk: Buffer) => {
		chunks.push(chunk.toString());
		if (!logStream.writableEnded) logStream.write(chunk);
	});
	child.stderr?.on('data', (chunk: Buffer) => {
		chunks.push(chunk.toString());
		if (!logStream.writableEnded) logStream.write(chunk);
	});

	try {
		await pgWaitForReady(child, `${baseURL}/api/v1/ready`, chunks);
	} catch (error) {
		logStream.end();
		await pgStop(child);
		throw error;
	}

	let closed = false;
	return {
		baseURL,
		port,
		logPath,
		close: async () => {
			if (closed) return;
			closed = true;
			logStream.end();
			await pgStop(child);
		}
	};
}
